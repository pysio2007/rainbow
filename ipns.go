package main

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ipfs/boxo/ipns"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
	"golang.org/x/time/rate"
)

const ipnsAPIPath = "/_rainbow/api/v1/ipns"

const (
	ipnsTimeout     = 2 * time.Second
	ipnsSlotsLimit  = 2
	ipnsRecordLimit = 16 << 10
	ipnsTargetLimit = 2 << 10
	ipnsJSONLimit   = 16 << 10
	ipnsClientLimit = 256
)

type ipnsValueGetter interface {
	GetValue(context.Context, string, ...routing.Option) ([]byte, error)
}

type ipnsHandlerOptions struct {
	timeout time.Duration
}

type ipnsAPI struct {
	store   ipnsValueGetter
	timeout time.Duration
	slots   chan struct{}
	mu      sync.Mutex
	clients map[string]*ipnsClient
	lru     *list.List
}

type ipnsClient struct {
	key      string
	limiter  *rate.Limiter
	lastSeen time.Time
}

type ipnsResponse struct {
	Version int        `json:"version"`
	Name    string     `json:"name"`
	Record  ipnsRecord `json:"record"`
}

type ipnsRecord struct {
	Status   string      `json:"status"`
	Target   *ipnsTarget `json:"target,omitempty"`
	Sequence string      `json:"sequence,omitempty"`
	EOL      string      `json:"eol,omitempty"`
	TTLNanos string      `json:"ttlNanos,omitempty"`
}

type ipnsTarget struct {
	Status string `json:"status"`
	Path   string `json:"path,omitempty"`
}

func newIPNSHandler(store ipnsValueGetter) *ipnsAPI {
	return newIPNSHandlerWithOptions(store, ipnsHandlerOptions{timeout: ipnsTimeout})
}

func newIPNSHandlerWithOptions(store ipnsValueGetter, options ipnsHandlerOptions) *ipnsAPI {
	if options.timeout <= 0 {
		options.timeout = ipnsTimeout
	}
	return &ipnsAPI{store: store, timeout: options.timeout, slots: make(chan struct{}, ipnsSlotsLimit), clients: make(map[string]*ipnsClient), lru: list.New()}
}

func (a *ipnsAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeIPNSError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	name, ok := ipnsQuery(r)
	if !ok {
		writeIPNSError(w, http.StatusBadRequest, "invalid_name")
		return
	}
	if !a.allowClient(ipnsRemoteHost(r)) {
		w.Header().Set("Retry-After", "10")
		writeIPNSError(w, http.StatusTooManyRequests, "rate_limited")
		return
	}
	select {
	case a.slots <- struct{}{}:
		defer func() { <-a.slots }()
	default:
		w.Header().Set("Retry-After", "1")
		writeIPNSError(w, http.StatusTooManyRequests, "busy")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), a.timeout)
	defer cancel()
	body := inspectIPNS(ctx, a.store, name)
	data, err := json.Marshal(body)
	if err != nil || len(data) > ipnsJSONLimit {
		body.Record = ipnsRecord{Status: "invalid_record"}
		data, _ = json.Marshal(body)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func writeIPNSError(w http.ResponseWriter, status int, code string) {
	body, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code}})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func ipnsQuery(r *http.Request) (ipns.Name, bool) {
	if r.URL.RawQuery == "" || strings.Contains(r.URL.RawQuery, "&") {
		return ipns.Name{}, false
	}
	values := r.URL.Query()
	if len(values) != 1 || len(values["name"]) != 1 || len(values.Get("name")) == 0 || len(values.Get("name")) > 256 {
		return ipns.Name{}, false
	}
	pid, err := peer.Decode(values.Get("name"))
	if err != nil || pid.String() != values.Get("name") {
		return ipns.Name{}, false
	}
	return ipns.NameFromPeer(pid), true
}

func ipnsRemoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

func (a *ipnsAPI) allowClient(key string) bool {
	now := time.Now()
	a.mu.Lock()
	for e := a.lru.Back(); e != nil; {
		prev := e.Prev()
		client := e.Value.(*ipnsClient)
		if now.Sub(client.lastSeen) >= 5*time.Minute {
			delete(a.clients, client.key)
			a.lru.Remove(e)
		}
		e = prev
	}
	client := a.clients[key]
	if client == nil {
		if len(a.clients) >= ipnsClientLimit {
			old := a.lru.Back()
			if old != nil {
				delete(a.clients, old.Value.(*ipnsClient).key)
				a.lru.Remove(old)
			}
		}
		client = &ipnsClient{key: key, limiter: rate.NewLimiter(rate.Limit(6.0/60.0), 2)}
		a.clients[key] = client
		a.lru.PushFront(client)
	} else {
		for e := a.lru.Front(); e != nil; e = e.Next() {
			if e.Value.(*ipnsClient) == client {
				a.lru.MoveToFront(e)
				break
			}
		}
	}
	client.lastSeen = now
	a.mu.Unlock()
	return client.limiter.Allow()
}

func inspectIPNS(ctx context.Context, store ipnsValueGetter, name ipns.Name) ipnsResponse {
	body := ipnsResponse{Version: 1, Name: name.Peer().String(), Record: ipnsRecord{Status: "unavailable"}}
	if store == nil {
		return body
	}
	raw, err := store.GetValue(ctx, string(name.RoutingKey()))
	if err != nil {
		body.Record.Status = ipnsErrorStatus(ctx, err)
		return body
	}
	if len(raw) > ipnsRecordLimit {
		body.Record.Status = "invalid_record"
		return body
	}
	record, err := ipns.UnmarshalRecord(raw)
	if err != nil {
		body.Record.Status = "invalid_record"
		return body
	}
	if err := ipns.ValidateWithName(record, name); err != nil {
		if errors.Is(err, ipns.ErrExpiredRecord) {
			body.Record.Status = "expired_record"
		} else {
			body.Record.Status = "invalid_record"
		}
		return body
	}
	path, err := record.Value()
	if err != nil || len(path.String()) > ipnsTargetLimit {
		body.Record.Status = "invalid_record"
		return body
	}
	sequence, err := record.Sequence()
	if err != nil {
		body.Record.Status = "invalid_record"
		return body
	}
	eol, err := record.Validity()
	if err != nil {
		body.Record.Status = "invalid_record"
		return body
	}
	ttl, err := record.TTL()
	if err != nil {
		body.Record.Status = "invalid_record"
		return body
	}
	body.Record = ipnsRecord{Status: "validated", Target: &ipnsTarget{Status: "reported", Path: path.String()}, Sequence: strconv.FormatUint(sequence, 10), EOL: eol.UTC().Format(time.RFC3339Nano), TTLNanos: strconv.FormatInt(ttl.Nanoseconds(), 10)}
	return body
}

func ipnsErrorStatus(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return "timeout"
	}
	if strings.Contains(strings.ToLower(err.Error()), "not found") {
		return "not_found"
	}
	return "unavailable"
}
