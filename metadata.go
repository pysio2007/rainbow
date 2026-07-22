package main

import (
	"bytes"
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ipfs/boxo/blockstore"
	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
	unixfsdata "github.com/ipfs/go-unixfsnode/data"
	dagpb "github.com/ipld/go-codec-dagpb"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/multiformats/go-multicodec"
	"github.com/multiformats/go-multihash"
	"golang.org/x/time/rate"
)

const metadataAPIPath = "/_rainbow/api/v1/metadata"

const (
	metadataTimeout       = 2 * time.Second
	metadataSlotsCapacity = 2
	metadataClientLimit   = 256
	metadataCIDLimit      = 512
	metadataBlockLimit    = 512 << 10
	metadataLinkLimit     = 64
	metadataLinkNameLimit = 256
	metadataJSONLimit     = 64 << 10
)

type rootBlockGetter interface {
	GetBlock(context.Context, cid.Cid) (blocks.Block, error)
}
type metadataHandlerOptions struct {
	timeout   time.Duration
	remoteCAR bool
}
type metadataAPI struct {
	getter    rootBlockGetter
	timeout   time.Duration
	remoteCAR bool
	slots     chan struct{}

	mu        sync.Mutex
	clients   map[string]*metadataClient
	clientLRU *list.List
}
type metadataClient struct {
	limiter  *rate.Limiter
	lastSeen time.Time
	key      string
}
type metadataResponse struct {
	Version        int               `json:"version"`
	ParsedCID      metadataParsedCID `json:"parsedCid"`
	Root           metadataRoot      `json:"root"`
	CanonicalLinks metadataLinks     `json:"canonicalLinks"`
}
type metadataParsedCID struct {
	Canonical    string `json:"canonical"`
	Version      uint64 `json:"version"`
	Codec        string `json:"codec"`
	Multihash    string `json:"multihash"`
	DigestLength int    `json:"digestLength"`
}
type metadataRoot struct {
	Status        string              `json:"status"`
	BlockBytes    int                 `json:"blockBytes"`
	BlockVerified bool                `json:"blockVerified"`
	UnixFS        *metadataUnixFS     `json:"unixfs,omitempty"`
	DirectLinks   metadataDirectLinks `json:"directLinks"`
}
type metadataUnixFS struct {
	Type             string `json:"type"`
	DeclaredFileSize *int64 `json:"declaredFileSize,omitempty"`
	Mode             *int64 `json:"mode,omitempty"`
	Mtime            *int64 `json:"mtime,omitempty"`
}
type metadataDirectLinks struct {
	Total     int                  `json:"total"`
	Shown     int                  `json:"shown"`
	Truncated bool                 `json:"truncated"`
	Items     []metadataDirectLink `json:"items"`
}
type metadataDirectLink struct {
	Name string `json:"name,omitempty"`
	CID  string `json:"cid"`
}
type metadataLinks struct {
	IPFS          string `json:"ipfs"`
	NativeGateway string `json:"nativeGateway"`
	Explorer      string `json:"explorer"`
	Inspector     string `json:"inspector"`
	CAR           string `json:"car"`
}

func newMetadataHandler(getter rootBlockGetter) *metadataAPI {
	return newMetadataHandlerWithOptions(getter, metadataHandlerOptions{timeout: metadataTimeout})
}

func newMetadataHandlerWithOptions(getter rootBlockGetter, options metadataHandlerOptions) *metadataAPI {
	if options.timeout <= 0 {
		options.timeout = metadataTimeout
	}
	return &metadataAPI{
		getter:    getter,
		timeout:   options.timeout,
		remoteCAR: options.remoteCAR,
		slots:     make(chan struct{}, metadataSlotsCapacity),
		clients:   make(map[string]*metadataClient, metadataClientLimit),
		clientLRU: list.New(),
	}
}

func (m *metadataAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeMetadataError(w, r, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	c, ok := metadataCIDQuery(r)
	if !ok {
		writeMetadataError(w, r, http.StatusBadRequest, "invalid_cid")
		return
	}
	if !m.allowClient(metadataRemoteHost(r)) {
		w.Header().Set("Retry-After", "10")
		writeMetadataError(w, r, http.StatusTooManyRequests, "busy")
		return
	}
	select {
	case m.slots <- struct{}{}:
		defer func() { <-m.slots }()
	default:
		w.Header().Set("Retry-After", "1")
		writeMetadataError(w, r, http.StatusTooManyRequests, "busy")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), m.timeout)
	defer cancel()
	body := inspectMetadata(ctx, m.getter, c, m.remoteCAR)
	data, err := marshalMetadataResponse(&body)
	if err != nil {
		body.Root.Status = "response_oversize"
		body.Root.DirectLinks.Items = nil
		body.Root.DirectLinks.Shown = 0
		body.Root.DirectLinks.Truncated = body.Root.DirectLinks.Total > 0
		data, _ = json.Marshal(body)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

func writeMetadataError(w http.ResponseWriter, r *http.Request, status int, code string) {
	body, _ := json.Marshal(struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}{Error: struct {
		Code string `json:"code"`
	}{Code: code}})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func metadataCIDQuery(r *http.Request) (cid.Cid, bool) {
	if r.URL.RawQuery == "" || strings.Contains(r.URL.RawQuery, "&") {
		return cid.Undef, false
	}
	parts := strings.SplitN(r.URL.RawQuery, "=", 2)
	if len(parts) != 2 || parts[0] != "cid" || parts[1] == "" || len(parts[1]) > metadataCIDLimit {
		return cid.Undef, false
	}
	values, ok := r.URL.Query()["cid"]
	if !ok || len(values) != 1 || values[0] == "" {
		return cid.Undef, false
	}
	c, err := cid.Parse(values[0])
	if err != nil || !c.Defined() || c.String() != values[0] || c.Prefix().MhType == uint64(multihash.IDENTITY) {
		return cid.Undef, false
	}
	return c, true
}

func metadataRemoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

func (m *metadataAPI) allowClient(key string) bool {
	now := time.Now()
	m.mu.Lock()
	for element := m.clientLRU.Back(); element != nil; {
		previous := element.Prev()
		client := element.Value.(*metadataClient)
		if now.Sub(client.lastSeen) >= 5*time.Minute {
			delete(m.clients, client.key)
			m.clientLRU.Remove(element)
		}
		element = previous
	}
	client := m.clients[key]
	if client == nil {
		if len(m.clients) >= metadataClientLimit {
			oldest := m.clientLRU.Back()
			if oldest != nil {
				oldClient := oldest.Value.(*metadataClient)
				delete(m.clients, oldClient.key)
				m.clientLRU.Remove(oldest)
			}
		}
		client = &metadataClient{key: key, limiter: rate.NewLimiter(rate.Limit(6.0/60.0), 2)}
		m.clients[key] = client
		m.clientLRU.PushFront(client)
	} else {
		for element := m.clientLRU.Front(); element != nil; element = element.Next() {
			if element.Value.(*metadataClient) == client {
				m.clientLRU.MoveToFront(element)
				break
			}
		}
	}
	client.lastSeen = now
	m.mu.Unlock()
	return client.limiter.Allow()
}

func inspectMetadata(ctx context.Context, getter rootBlockGetter, c cid.Cid, remoteCAR bool) metadataResponse {
	body := metadataResponse{
		Version:   1,
		ParsedCID: metadataParsedCIDInfo(c),
		Root:      metadataRoot{DirectLinks: metadataDirectLinks{Items: make([]metadataDirectLink, 0)}},
		CanonicalLinks: metadataLinks{
			IPFS:          "ipfs://" + c.String(),
			NativeGateway: "/ipfs/" + c.String(),
			Explorer:      "/explore/" + c.String(),
			Inspector:     "/inspect/" + c.String(),
			CAR:           "/ipfs/" + c.String() + "?format=car",
		},
	}
	if remoteCAR || getter == nil {
		body.Root.Status = "unsupported_mode"
		return body
	}
	block, err := getter.GetBlock(ctx, c)
	if err != nil {
		body.Root.Status = metadataFetchStatus(ctx, err)
		return body
	}
	if block == nil {
		body.Root.Status = "error"
		return body
	}
	body.Root.BlockBytes = len(block.RawData())
	if body.Root.BlockBytes > metadataBlockLimit {
		body.Root.Status = "block_too_large"
		return body
	}
	if !block.Cid().Equals(c) {
		body.Root.Status = "integrity_error"
		return body
	}
	computed, err := c.Prefix().Sum(block.RawData())
	if err != nil || !computed.Equals(c) {
		body.Root.Status = "integrity_error"
		return body
	}
	body.Root.BlockVerified = true
	switch c.Type() {
	case uint64(multicodec.Raw):
		body.Root.Status = "fetched"
	case uint64(multicodec.DagPb):
		links, unixfs, err := parseMetadataDAGPB(block.RawData())
		if err != nil {
			body.Root.Status = "malformed"
		} else {
			body.Root.Status = "fetched"
			body.Root.DirectLinks = links
			body.Root.UnixFS = unixfs
		}
	default:
		body.Root.Status = "unsupported_codec"
	}
	return body
}

func metadataParsedCIDInfo(c cid.Cid) metadataParsedCID {
	decoded, err := multihash.Decode(c.Hash())
	if err != nil {
		return metadataParsedCID{Canonical: c.String(), Version: c.Version(), Codec: multicodec.Code(c.Type()).String(), Multihash: fmt.Sprintf("code-%d", c.Prefix().MhType)}
	}
	multihashName := decoded.Name
	if multihashName == "" || strings.EqualFold(multihashName, "unknown") {
		multihashName = fmt.Sprintf("code-%d", decoded.Code)
	}
	return metadataParsedCID{
		Canonical:    c.String(),
		Version:      c.Version(),
		Codec:        multicodec.Code(c.Type()).String(),
		Multihash:    multihashName,
		DigestLength: len(decoded.Digest),
	}
}

func metadataFetchStatus(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if strings.Contains(strings.ToLower(err.Error()), "not found") {
		return "not_found"
	}
	if errors.Is(err, blockstore.ErrHashMismatch) {
		return "integrity_error"
	}
	return "error"
}

func parseMetadataDAGPB(raw []byte) (metadataDirectLinks, *metadataUnixFS, error) {
	linksResult := metadataDirectLinks{Items: make([]metadataDirectLink, 0)}
	assembler := dagpb.Type.PBNode.NewBuilder()
	if err := dagpb.Decode(assembler, bytes.NewReader(raw)); err != nil {
		return linksResult, nil, err
	}
	node, ok := assembler.Build().(dagpb.PBNode)
	if !ok {
		return linksResult, nil, errors.New("invalid dag-pb node")
	}
	linksNode, err := node.LookupByString("Links")
	if err != nil {
		return linksResult, nil, err
	}
	iterator := linksNode.ListIterator()
	for !iterator.Done() {
		_, linkNode, err := iterator.Next()
		if err != nil {
			return linksResult, nil, err
		}
		linksResult.Total++
		if linksResult.Shown >= metadataLinkLimit {
			continue
		}
		hashNode, err := linkNode.LookupByString("Hash")
		if err != nil {
			return linksResult, nil, err
		}
		link, err := hashNode.AsLink()
		if err != nil {
			return linksResult, nil, err
		}
		child, ok := link.(cidlink.Link)
		if !ok {
			return linksResult, nil, errors.New("non-cid dag-pb link")
		}
		item := metadataDirectLink{CID: child.Cid.String()}
		if nameNode, err := linkNode.LookupByString("Name"); err == nil && !nameNode.IsAbsent() {
			if name, err := nameNode.AsString(); err == nil && len([]byte(name)) <= metadataLinkNameLimit {
				item.Name = name
			}
		}
		linksResult.Items = append(linksResult.Items, item)
		linksResult.Shown++
	}
	linksResult.Truncated = linksResult.Total > linksResult.Shown

	dataNode, err := node.LookupByString("Data")
	if err != nil || dataNode.IsAbsent() || dataNode.IsNull() {
		return linksResult, nil, nil
	}
	data, err := dataNode.AsBytes()
	if err != nil {
		return linksResult, nil, err
	}
	unixfs, err := unixfsdata.DecodeUnixFSData(data)
	if err != nil {
		return linksResult, nil, err
	}
	return linksResult, metadataUnixFSInfo(unixfs), nil
}

func metadataUnixFSInfo(node unixfsdata.UnixFSData) *metadataUnixFS {
	result := &metadataUnixFS{}
	if value, err := node.LookupByString("DataType"); err == nil {
		if dataType, err := value.AsInt(); err == nil {
			result.Type = strings.ToLower(unixfsdata.DataTypeNames[dataType])
		}
	}
	if result.Type == "" {
		result.Type = "unknown"
	}
	if value, err := node.LookupByString("FileSize"); err == nil && !value.IsAbsent() {
		if fileSize, err := value.AsInt(); err == nil {
			result.DeclaredFileSize = &fileSize
		}
	}
	if value, err := node.LookupByString("Mode"); err == nil && !value.IsAbsent() {
		if mode, err := value.AsInt(); err == nil {
			result.Mode = &mode
		}
	}
	if value, err := node.LookupByString("Mtime"); err == nil && !value.IsAbsent() {
		if seconds, err := value.LookupByString("Seconds"); err == nil {
			if mtime, err := seconds.AsInt(); err == nil {
				result.Mtime = &mtime
			}
		}
	}
	return result
}

func marshalMetadataResponse(body *metadataResponse) ([]byte, error) {
	for {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		if len(data) <= metadataJSONLimit {
			return data, nil
		}
		if len(body.Root.DirectLinks.Items) == 0 {
			return nil, errors.New("metadata response too large")
		}
		body.Root.DirectLinks.Items = body.Root.DirectLinks.Items[:len(body.Root.DirectLinks.Items)-1]
		body.Root.DirectLinks.Shown = len(body.Root.DirectLinks.Items)
		body.Root.DirectLinks.Truncated = true
	}
}
