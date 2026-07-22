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

	"github.com/ipfs/go-cid"
	"github.com/multiformats/go-multicodec"
	"github.com/multiformats/go-multihash"
	"golang.org/x/time/rate"
)

const dagAPIPath = "/_rainbow/api/v1/dag"

const (
	dagTimeout      = 3 * time.Second
	dagNodeLimit    = 16
	dagLinkLimit    = 4
	dagBlockLimit   = 256 << 10
	dagParsedLimit  = 1 << 20
	dagJSONLimit    = 48 << 10
	dagNestingLimit = 32
	dagVisitLimit   = 4096
	dagClientLimit  = 256
)

var dagCARHeavySlots = make(chan struct{}, 1)

type dagHandlerOptions struct {
	timeout   time.Duration
	remoteCAR bool
}

type dagAPI struct {
	getter    rootBlockGetter
	timeout   time.Duration
	remoteCAR bool
	mu        sync.Mutex
	clients   map[string]*dagClient
	lru       *list.List
}

type dagClient struct {
	key      string
	limiter  *rate.Limiter
	lastSeen time.Time
}

type dagResponse struct {
	Version        int            `json:"version"`
	Root           string         `json:"root"`
	RequestedDepth int            `json:"requestedDepth"`
	Observation    dagObservation `json:"observation"`
	Nodes          []dagNode      `json:"nodes"`
}

type dagObservation struct {
	Status         string   `json:"status"`
	Truncated      bool     `json:"truncated"`
	LimitsHit      []string `json:"limitsHit"`
	NodesAttempted int      `json:"nodesAttempted"`
	ParsedBytes    int      `json:"parsedBytes"`
}

type dagNode struct {
	CID           string   `json:"cid"`
	Depth         int      `json:"depth"`
	Codec         string   `json:"codec"`
	Status        string   `json:"status"`
	BlockBytes    int      `json:"blockBytes"`
	BlockVerified bool     `json:"blockVerified"`
	Links         dagLinks `json:"links"`
}

type dagLinks struct {
	Total     int       `json:"total"`
	Shown     int       `json:"shown"`
	Truncated bool      `json:"truncated"`
	Items     []dagLink `json:"items"`
}

type dagLink struct {
	Name string `json:"name,omitempty"`
	CID  string `json:"cid"`
}

type dagQueueItem struct {
	cid   cid.Cid
	depth int
}

func newDAGHandler(getter rootBlockGetter, remoteCAR bool) *dagAPI {
	return newDAGHandlerWithOptions(getter, dagHandlerOptions{timeout: dagTimeout, remoteCAR: remoteCAR})
}

func newDAGHandlerWithOptions(getter rootBlockGetter, options dagHandlerOptions) *dagAPI {
	if options.timeout <= 0 {
		options.timeout = dagTimeout
	}
	return &dagAPI{getter: getter, timeout: options.timeout, remoteCAR: options.remoteCAR, clients: make(map[string]*dagClient), lru: list.New()}
}

func (d *dagAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeDAGError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	c, depth, ok := dagQuery(r)
	if !ok {
		writeDAGError(w, http.StatusBadRequest, "invalid_cid")
		return
	}
	if !d.allowClient(dagRemoteHost(r)) {
		w.Header().Set("Retry-After", "30")
		writeDAGError(w, http.StatusTooManyRequests, "rate_limited")
		return
	}
	select {
	case dagCARHeavySlots <- struct{}{}:
		defer func() { <-dagCARHeavySlots }()
	default:
		w.Header().Set("Retry-After", "1")
		writeDAGError(w, http.StatusTooManyRequests, "busy")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), d.timeout)
	defer cancel()
	body := inspectDAG(ctx, d.getter, d.remoteCAR, c, depth)
	data, err := marshalDAG(&body)
	if err != nil {
		body.Observation.Truncated = true
		addDAGLimitReason(&body.Observation, "json_limit")
		body.Nodes = body.Nodes[:0]
		data, _ = json.Marshal(body)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func writeDAGError(w http.ResponseWriter, status int, code string) {
	body, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code}})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func dagQuery(r *http.Request) (cid.Cid, int, bool) {
	if r.URL.RawQuery == "" || strings.Count(r.URL.RawQuery, "&") != 1 {
		return cid.Undef, 0, false
	}
	values := r.URL.Query()
	if len(values) != 2 || len(values["cid"]) != 1 || len(values["depth"]) != 1 {
		return cid.Undef, 0, false
	}
	c, err := cid.Parse(values.Get("cid"))
	if err != nil || c.String() != values.Get("cid") || !c.Defined() || c.Prefix().MhType == uint64(multihash.IDENTITY) {
		return cid.Undef, 0, false
	}
	depth, err := strconv.Atoi(values.Get("depth"))
	if err != nil || depth < 1 || depth > 2 {
		return cid.Undef, 0, false
	}
	return c, depth, true
}

func dagRemoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

func (d *dagAPI) allowClient(key string) bool {
	now := time.Now()
	d.mu.Lock()
	for e := d.lru.Back(); e != nil; {
		prev := e.Prev()
		client := e.Value.(*dagClient)
		if now.Sub(client.lastSeen) >= 5*time.Minute {
			delete(d.clients, client.key)
			d.lru.Remove(e)
		}
		e = prev
	}
	client := d.clients[key]
	if client == nil {
		if len(d.clients) >= dagClientLimit {
			old := d.lru.Back()
			if old != nil {
				delete(d.clients, old.Value.(*dagClient).key)
				d.lru.Remove(old)
			}
		}
		client = &dagClient{key: key, limiter: rate.NewLimiter(rate.Limit(2.0/60.0), 1)}
		d.clients[key] = client
		d.lru.PushFront(client)
	} else {
		for e := d.lru.Front(); e != nil; e = e.Next() {
			if e.Value.(*dagClient) == client {
				d.lru.MoveToFront(e)
				break
			}
		}
	}
	client.lastSeen = now
	d.mu.Unlock()
	return client.limiter.Allow()
}

func inspectDAG(ctx context.Context, getter rootBlockGetter, remoteCAR bool, root cid.Cid, depth int) dagResponse {
	body := dagResponse{
		Version:        1,
		Root:           root.String(),
		RequestedDepth: depth,
		Observation:    dagObservation{LimitsHit: make([]string, 0)},
		Nodes:          make([]dagNode, 0, dagNodeLimit),
	}
	queue := []dagQueueItem{{cid: root, depth: 0}}
	visited := map[string]struct{}{root.KeyString(): {}}
	if remoteCAR || getter == nil {
		body.Observation.Status = "unsupported_mode"
		body.Nodes = append(body.Nodes, dagNode{CID: root.String(), Codec: multicodec.Code(root.Type()).String(), Status: "unsupported_mode", Links: dagLinks{Items: []dagLink{}}})
		return body
	}
	for len(queue) > 0 && len(body.Nodes) < dagNodeLimit {
		item := queue[0]
		queue = queue[1:]
		body.Observation.NodesAttempted++
		node := dagNode{CID: item.cid.String(), Depth: item.depth, Codec: multicodec.Code(item.cid.Type()).String(), Links: dagLinks{Items: []dagLink{}}}
		block, err := getter.GetBlock(ctx, item.cid)
		if err != nil {
			node.Status = dagErrorStatus(ctx, err)
			body.Nodes = append(body.Nodes, node)
			if body.Observation.Status == "" {
				body.Observation.Status = node.Status
			}
			continue
		}
		if block == nil {
			node.Status = "error"
			body.Nodes = append(body.Nodes, node)
			if body.Observation.Status == "" {
				body.Observation.Status = node.Status
			}
			continue
		}
		raw := block.RawData()
		node.BlockBytes = len(raw)
		if len(raw) > dagBlockLimit {
			node.Status = "block_too_large"
			addDAGLimitReason(&body.Observation, "block_limit")
			body.Nodes = append(body.Nodes, node)
			if body.Observation.Status == "" {
				body.Observation.Status = node.Status
			}
			continue
		}
		if !block.Cid().Equals(item.cid) {
			node.Status = "integrity_error"
			body.Nodes = append(body.Nodes, node)
			if body.Observation.Status == "" {
				body.Observation.Status = node.Status
			}
			continue
		}
		computed, err := item.cid.Prefix().Sum(raw)
		if err != nil || !computed.Equals(item.cid) {
			node.Status = "integrity_error"
			body.Nodes = append(body.Nodes, node)
			if body.Observation.Status == "" {
				body.Observation.Status = node.Status
			}
			continue
		}
		node.BlockVerified = true
		if body.Observation.ParsedBytes+len(raw) > dagParsedLimit {
			node.Status = "limit"
			addDAGLimitReason(&body.Observation, "parsed_bytes_limit")
			body.Nodes = append(body.Nodes, node)
			if body.Observation.Status == "" {
				body.Observation.Status = node.Status
			}
			continue
		}
		body.Observation.ParsedBytes += len(raw)
		links, status, reason := parseDAGLinks(item.cid, raw)
		node.Links = links
		if links.Truncated {
			addDAGLimitReason(&body.Observation, "link_limit")
		}
		node.Status = status
		if reason != "" {
			addDAGLimitReason(&body.Observation, reason)
		}
		body.Nodes = append(body.Nodes, node)
		if status != "fetched" && body.Observation.Status == "" {
			body.Observation.Status = status
		}
		if status != "fetched" || item.depth >= depth {
			continue
		}
		for _, link := range links.Items {
			child, err := cid.Parse(link.CID)
			if err != nil {
				continue
			}
			if _, exists := visited[child.KeyString()]; exists {
				continue
			}
			if len(visited) >= dagNodeLimit {
				addDAGLimitReason(&body.Observation, "node_limit")
				body.Observation.Truncated = true
				break
			}
			visited[child.KeyString()] = struct{}{}
			queue = append(queue, dagQueueItem{cid: child, depth: item.depth + 1})
		}
	}
	if len(queue) > 0 {
		addDAGLimitReason(&body.Observation, "node_limit")
	}
	if body.Observation.Status == "" {
		body.Observation.Status = "fetched"
	}
	return body
}

func dagErrorStatus(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if strings.Contains(strings.ToLower(err.Error()), "not found") {
		return "not_found"
	}
	return "error"
}
