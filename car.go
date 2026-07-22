package main

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ipfs/boxo/gateway"
	boxopath "github.com/ipfs/boxo/path"
	"github.com/ipfs/go-cid"
	carv2 "github.com/ipld/go-car/v2"
	"github.com/multiformats/go-multihash"
	"golang.org/x/time/rate"
)

const carAPIPath = "/_rainbow/api/v1/car/check"

const (
	carTimeout      = 3 * time.Second
	carHeaderLimit  = 8 << 10
	carSectionLimit = 512 << 10
	carStreamLimit  = 1 << 20
	carJSONLimit    = 8 << 10
	carClientLimit  = 256
)

type carGetter interface {
	GetCAR(context.Context, boxopath.ImmutablePath, gateway.CarParams) (gateway.ContentPathMetadata, io.ReadCloser, error)
}

type carHandlerOptions struct {
	timeout   time.Duration
	remoteCAR bool
}

type carAPI struct {
	getter    carGetter
	timeout   time.Duration
	remoteCAR bool
	mu        sync.Mutex
	clients   map[string]*carClient
	lru       *list.List
}

type carClient struct {
	key      string
	limiter  *rate.Limiter
	lastSeen time.Time
}

type carResponse struct {
	Version      int             `json:"version"`
	CID          string          `json:"cid"`
	Verification carVerification `json:"verification"`
}

type carVerification struct {
	Status              string `json:"status"`
	Scope               string `json:"scope"`
	CARVersion          *int   `json:"carVersion,omitempty"`
	DeclaredRootMatches *bool  `json:"declaredRootMatches,omitempty"`
	RootBlockPresent    *bool  `json:"rootBlockPresent,omitempty"`
	RootBlockVerified   *bool  `json:"rootBlockVerified,omitempty"`
	BlocksRead          *int   `json:"blocksRead,omitempty"`
	BytesRead           *int   `json:"bytesRead,omitempty"`
}

func newCARHandler(getter carGetter, remoteCAR bool) *carAPI {
	return newCARHandlerWithOptions(getter, carHandlerOptions{timeout: carTimeout, remoteCAR: remoteCAR})
}

func newCARHandlerWithOptions(getter carGetter, options carHandlerOptions) *carAPI {
	if options.timeout <= 0 {
		options.timeout = carTimeout
	}
	return &carAPI{getter: getter, timeout: options.timeout, remoteCAR: options.remoteCAR, clients: make(map[string]*carClient), lru: list.New()}
}

func (a *carAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeCARError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	c, ok := carQuery(r)
	if !ok {
		writeCARError(w, http.StatusBadRequest, "invalid_cid")
		return
	}
	if !a.allowClient(carRemoteHost(r)) {
		w.Header().Set("Retry-After", "60")
		writeCARError(w, http.StatusTooManyRequests, "rate_limited")
		return
	}
	select {
	case dagCARHeavySlots <- struct{}{}:
		defer func() { <-dagCARHeavySlots }()
	default:
		w.Header().Set("Retry-After", "1")
		writeCARError(w, http.StatusTooManyRequests, "busy")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), a.timeout)
	defer cancel()
	body := inspectCAR(ctx, a.getter, a.remoteCAR, c)
	data, err := json.Marshal(body)
	if err != nil || len(data) > carJSONLimit {
		body.Verification.Status = "limit"
		data, _ = json.Marshal(body)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func writeCARError(w http.ResponseWriter, status int, code string) {
	body, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code}})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func carQuery(r *http.Request) (cid.Cid, bool) {
	if r.URL.RawQuery == "" || strings.Contains(r.URL.RawQuery, "&") {
		return cid.Undef, false
	}
	values := r.URL.Query()
	if len(values) != 1 || len(values["cid"]) != 1 {
		return cid.Undef, false
	}
	c, err := cid.Parse(values.Get("cid"))
	if err != nil || !c.Defined() || c.String() != values.Get("cid") || c.Prefix().MhType == uint64(multihash.IDENTITY) {
		return cid.Undef, false
	}
	return c, true
}

func carRemoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

func (a *carAPI) allowClient(key string) bool {
	now := time.Now()
	a.mu.Lock()
	for e := a.lru.Back(); e != nil; {
		prev := e.Prev()
		client := e.Value.(*carClient)
		if now.Sub(client.lastSeen) >= 5*time.Minute {
			delete(a.clients, client.key)
			a.lru.Remove(e)
		}
		e = prev
	}
	client := a.clients[key]
	if client == nil {
		if len(a.clients) >= carClientLimit {
			old := a.lru.Back()
			if old != nil {
				delete(a.clients, old.Value.(*carClient).key)
				a.lru.Remove(old)
			}
		}
		client = &carClient{key: key, limiter: rate.NewLimiter(rate.Limit(1.0/60.0), 1)}
		a.clients[key] = client
		a.lru.PushFront(client)
	} else {
		for e := a.lru.Front(); e != nil; e = e.Next() {
			if e.Value.(*carClient) == client {
				a.lru.MoveToFront(e)
				break
			}
		}
	}
	client.lastSeen = now
	a.mu.Unlock()
	return client.limiter.Allow()
}

func inspectCAR(ctx context.Context, getter carGetter, remoteCAR bool, root cid.Cid) carResponse {
	body := carResponse{Version: 1, CID: root.String(), Verification: carVerification{Status: "unavailable", Scope: string(gateway.DagScopeBlock)}}
	if remoteCAR {
		body.Verification.Status = "unsupported_mode"
		return body
	}
	if getter == nil {
		return body
	}
	_, stream, err := getter.GetCAR(ctx, boxopath.FromCid(root), gateway.CarParams{Scope: gateway.DagScopeBlock})
	if err != nil {
		body.Verification.Status = carErrorStatus(ctx, err)
		return body
	}
	if stream == nil {
		body.Verification.Status = "unavailable"
		return body
	}
	defer stream.Close()
	reader := &carCountingReader{reader: io.LimitReader(stream, carStreamLimit+1)}
	blockReader, err := carv2.NewBlockReader(reader, carv2.MaxAllowedHeaderSize(carHeaderLimit), carv2.MaxAllowedSectionSize(carSectionLimit))
	if err != nil {
		body.Verification.Status = carBlockError(err)
		return body
	}
	if blockReader.Version != 1 || len(blockReader.Roots) != 1 || !blockReader.Roots[0].Equals(root) {
		body.Verification.Status = "root_mismatch"
		return body
	}
	carVersion := int(blockReader.Version)
	body.Verification.CARVersion = &carVersion
	declared := true
	body.Verification.DeclaredRootMatches = &declared
	blocksRead, bytesRead, rootCount := 0, 0, 0
	for {
		block, err := blockReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			body.Verification.Status = carBlockError(err)
			return body
		}
		blocksRead++
		bytesRead += len(block.RawData())
		if block.Cid().Equals(root) {
			rootCount++
		}
	}
	if reader.bytes > carStreamLimit {
		body.Verification.Status = "limit"
		return body
	}
	present := rootCount > 0
	verified := rootCount == 1
	body.Verification.RootBlockPresent = &present
	if blocksRead != 1 || rootCount != 1 {
		verified = false
	}
	body.Verification.RootBlockVerified = &verified
	body.Verification.BlocksRead = &blocksRead
	body.Verification.BytesRead = &bytesRead
	if rootCount == 0 {
		body.Verification.Status = "root_missing"
	} else if blocksRead != 1 || rootCount != 1 {
		body.Verification.Status = "unexpected_block_count"
	} else {
		body.Verification.Status = "verified"
	}
	return body
}

type carCountingReader struct {
	reader io.Reader
	bytes  int
}

func (r *carCountingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.bytes += n
	return n, err
}

func carErrorStatus(ctx context.Context, err error) string {
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

func carBlockError(err error) string {
	if strings.Contains(strings.ToLower(err.Error()), "integrity") || strings.Contains(strings.ToLower(err.Error()), "mismatch") {
		return "integrity_error"
	}
	if strings.Contains(strings.ToLower(err.Error()), "maximum") || strings.Contains(strings.ToLower(err.Error()), "limit") {
		return "limit"
	}
	return "invalid_car"
}
