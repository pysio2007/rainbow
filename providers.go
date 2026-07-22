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
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
	"github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
	"golang.org/x/time/rate"
)

const providersAPIPath = "/_rainbow/api/v1/providers"

const (
	providerLookupTimeout       = 5 * time.Second
	providerLookupLimit         = 16
	providerLimiterCapacity     = 256
	providerLimiterIdleTimeout  = 5 * time.Minute
	maxProviderInspectAddresses = 32
	maxProviderAddresses        = 8
	maxProviderAddressLength    = 512
	maxProviderEventSize        = 16 << 10
)

type providerEvent struct {
	PeerID    string   `json:"peerId"`
	Addresses []string `json:"addresses"`
}

type providerCompleteEvent struct {
	Count    int   `json:"count"`
	Duration int64 `json:"durationMs"`
	Cached   bool  `json:"cached"`
	TimedOut bool  `json:"timedOut"`
}

type providerHandlerOptions struct {
	lookupTimeout      time.Duration
	limiterIdleTimeout time.Duration
}

type providerAPI struct {
	discovery          routing.ContentDiscovery
	cache              *providerCache
	timeout            time.Duration
	limiterIdleTimeout time.Duration
	live               chan struct{}

	mu        sync.Mutex
	flights   map[string]*providerFlight
	clients   map[string]*providerClient
	clientLRU *list.List

	// afterLastFlightRelease is only used by package tests to make the release
	// ordering observable. It is nil in production.
	afterLastFlightRelease func()
}

type providerClient struct {
	limiter  *rate.Limiter
	lastSeen time.Time
	key      string
}

type providerFlight struct {
	ctx    context.Context
	cancel context.CancelFunc

	mu        sync.Mutex
	results   []providerRecord
	notify    chan struct{}
	complete  bool
	timedOut  bool
	duration  time.Duration
	refs      int
	abandoned bool
}

func newProviderHandler(discovery routing.ContentDiscovery) http.Handler {
	return newProviderHandlerWithOptions(discovery, providerHandlerOptions{lookupTimeout: providerLookupTimeout})
}

func newProviderHandlerWithOptions(discovery routing.ContentDiscovery, options providerHandlerOptions) http.Handler {
	if options.lookupTimeout <= 0 {
		options.lookupTimeout = providerLookupTimeout
	}
	return &providerAPI{
		discovery: discovery,
		cache:     newProviderCache(),
		timeout:   options.lookupTimeout,
		live:      make(chan struct{}, 2),
		flights:   make(map[string]*providerFlight),
		clients:   make(map[string]*providerClient, providerLimiterCapacity),
		clientLRU: list.New(),
		limiterIdleTimeout: func() time.Duration {
			if options.limiterIdleTimeout > 0 {
				return options.limiterIdleTimeout
			}
			return providerLimiterIdleTimeout
		}(),
	}
}

func (p *providerAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeProviderError(w, r, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	requestedCID, ok := providerCID(r)
	if !ok {
		writeProviderError(w, r, http.StatusBadRequest, "invalid_cid")
		return
	}
	if p.discovery == nil {
		writeProviderError(w, r, http.StatusServiceUnavailable, "unavailable")
		return
	}
	if !p.allowClient(providerRemoteHost(r)) {
		w.Header().Set("Retry-After", "10")
		writeProviderError(w, r, http.StatusTooManyRequests, "rate_limited")
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		return
	}

	key := requestedCID.KeyString()
	if flight := p.flightFor(key); flight != nil {
		defer p.releaseFlight(key, flight)
		streamProviderFlight(w, r, flight)
		return
	}
	if results, ok := p.cache.get(key, time.Now()); ok {
		writeProviderStream(w, r, results, providerCompleteEvent{Count: len(results), Cached: true})
		return
	}

	flight, ok := p.getFlight(key, requestedCID)
	if !ok {
		w.Header().Set("Retry-After", "1")
		writeProviderError(w, r, http.StatusTooManyRequests, "lookup_busy")
		return
	}
	defer p.releaseFlight(key, flight)
	streamProviderFlight(w, r, flight)
}

func (p *providerAPI) flightFor(key string) *providerFlight {
	p.mu.Lock()
	defer p.mu.Unlock()
	flight := p.flights[key]
	if flight != nil {
		flight.addRef()
	}
	return flight
}

func (p *providerAPI) releaseFlight(key string, flight *providerFlight) {
	p.mu.Lock()
	flight.mu.Lock()
	flight.refs--
	if flight.refs == 0 {
		if !flight.complete {
			flight.abandoned = true
			flight.cancel()
		}
		if p.afterLastFlightRelease != nil {
			p.afterLastFlightRelease()
		}
		if p.flights[key] == flight {
			delete(p.flights, key)
		}
	}
	flight.mu.Unlock()
	p.mu.Unlock()
}

func (p *providerAPI) allowClient(hostname string) bool {
	now := time.Now()
	p.mu.Lock()
	for element := p.clientLRU.Back(); element != nil; {
		previous := element.Prev()
		client := element.Value.(*providerClient)
		if now.Sub(client.lastSeen) >= p.limiterIdleTimeout {
			delete(p.clients, client.key)
			p.clientLRU.Remove(element)
		}
		element = previous
	}
	client := p.clients[hostname]
	if client == nil {
		if len(p.clients) >= providerLimiterCapacity {
			oldest := p.clientLRU.Back()
			if oldest != nil {
				oldClient := oldest.Value.(*providerClient)
				delete(p.clients, oldClient.key)
				p.clientLRU.Remove(oldest)
			}
		}
		client = &providerClient{
			limiter: rate.NewLimiter(rate.Limit(6.0/60.0), 2),
			key:     hostname,
		}
		p.clients[hostname] = client
		p.clientLRU.PushFront(client)
	} else {
		for element := p.clientLRU.Front(); element != nil; element = element.Next() {
			if element.Value.(*providerClient) == client {
				p.clientLRU.MoveToFront(element)
				break
			}
		}
	}
	client.lastSeen = now
	p.mu.Unlock()
	return client.limiter.Allow()
}

func providerRemoteHost(r *http.Request) string {
	// RemoteAddr is intentionally used as the client identity. Without an
	// explicitly configured trusted proxy boundary, forwarding headers such as
	// X-Forwarded-For are attacker-controlled and must not affect rate limits.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

func providerCID(r *http.Request) (cid.Cid, bool) {
	values, ok := r.URL.Query()["cid"]
	if !ok || len(values) != 1 || values[0] == "" {
		return cid.Undef, false
	}
	c, err := cid.Parse(values[0])
	if err != nil || !c.Defined() {
		return cid.Undef, false
	}
	return c, true
}

func (p *providerAPI) getFlight(key string, c cid.Cid) (*providerFlight, bool) {
	p.mu.Lock()
	if flight, ok := p.flights[key]; ok {
		flight.addRef()
		p.mu.Unlock()
		return flight, true
	}
	select {
	case p.live <- struct{}{}:
	default:
		p.mu.Unlock()
		return nil, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	flight := &providerFlight{ctx: ctx, cancel: cancel, notify: make(chan struct{}), refs: 1}
	p.flights[key] = flight
	p.mu.Unlock()
	go p.runFlight(key, c, flight)
	return flight, true
}

func (f *providerFlight) addRef() {
	f.mu.Lock()
	f.refs++
	f.mu.Unlock()
}

func (p *providerAPI) runFlight(key string, c cid.Cid, f *providerFlight) {
	lookupCtx, cancel := context.WithTimeout(f.ctx, p.timeout)
	defer cancel()
	started := time.Now()
	providerCh := p.discovery.FindProvidersAsync(lookupCtx, c, providerLookupLimit)
	seenPeers := make(map[string]struct{}, providerLookupLimit)
	seenResults := make(map[string]struct{}, providerLookupLimit)
	normal := false
	timedOut := false

	for {
		select {
		case info, ok := <-providerCh:
			if !ok {
				normal = lookupCtx.Err() == nil
				goto done
			}
			if info.ID == "" {
				continue
			}
			peerID := info.ID.String()
			if _, exists := seenPeers[peerID]; exists {
				continue
			}
			seenPeers[peerID] = struct{}{}
			if record, ok := publicProviderRecord(info); ok {
				if _, exists := seenResults[record.PeerID]; !exists {
					seenResults[record.PeerID] = struct{}{}
					f.addResult(record)
				}
			}
			if len(seenPeers) == providerLookupLimit {
				cancel()
				drainProviderChannel(providerCh)
				normal = true
				goto done
			}
		case <-lookupCtx.Done():
			timedOut = errors.Is(lookupCtx.Err(), context.DeadlineExceeded)
			drainProviderChannel(providerCh)
			goto done
		}
	}

done:
	f.mu.Lock()
	abandoned := f.abandoned
	f.mu.Unlock()
	if normal && !abandoned {
		f.mu.Lock()
		cachedResults := cloneProviderRecords(f.results)
		f.mu.Unlock()
		p.cache.put(key, cachedResults, time.Now())
	}
	f.finish(timedOut, time.Since(started))
	p.mu.Lock()
	f.mu.Lock()
	unreferenced := f.refs == 0
	if unreferenced && p.flights[key] == f {
		delete(p.flights, key)
	}
	f.mu.Unlock()
	p.mu.Unlock()
	<-p.live
}

func drainProviderChannel(ch <-chan peer.AddrInfo) {
	if ch == nil {
		return
	}
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-timer.C:
			return
		}
	}
}

func (f *providerFlight) addResult(result providerRecord) {
	f.mu.Lock()
	f.results = append(f.results, cloneProviderRecords([]providerRecord{result})...)
	close(f.notify)
	f.notify = make(chan struct{})
	f.mu.Unlock()
}

func (f *providerFlight) finish(timedOut bool, duration time.Duration) {
	f.mu.Lock()
	f.timedOut = timedOut
	f.duration = duration
	f.complete = true
	close(f.notify)
	f.notify = make(chan struct{})
	f.mu.Unlock()
}

func publicProviderRecord(info peer.AddrInfo) (providerRecord, bool) {
	if info.ID == "" {
		return providerRecord{}, false
	}
	inspect := info.Addrs
	if len(inspect) > maxProviderInspectAddresses {
		inspect = inspect[:maxProviderInspectAddresses]
	}
	addrs := make([]string, 0, min(len(inspect), maxProviderAddresses))
	seen := make(map[string]struct{}, min(len(inspect), maxProviderAddresses))
	for _, addr := range inspect {
		if len(addrs) == maxProviderAddresses {
			break
		}
		transport, _ := peer.SplitAddr(addr)
		if transport == nil || !isPublicProviderAddr(transport) {
			continue
		}
		value := transport.String()
		if len(value) > maxProviderAddressLength {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		addrs = append(addrs, value)
	}
	if len(addrs) == 0 {
		return providerRecord{}, false
	}
	return boundedProviderRecord(providerRecord{PeerID: info.ID.String(), Addresses: addrs})
}

func boundedProviderRecord(result providerRecord) (providerRecord, bool) {
	if result.PeerID == "" {
		return providerRecord{}, false
	}
	inspect := result.Addresses
	if len(inspect) > maxProviderInspectAddresses {
		inspect = inspect[:maxProviderInspectAddresses]
	}
	addresses := make([]string, 0, min(len(inspect), maxProviderAddresses))
	seen := make(map[string]struct{}, min(len(inspect), maxProviderAddresses))
	for _, address := range inspect {
		if len(address) == 0 || len(address) > maxProviderAddressLength || len(addresses) == maxProviderAddresses {
			continue
		}
		parsed, err := multiaddr.NewMultiaddr(address)
		if err != nil || !isPublicProviderAddr(parsed) {
			continue
		}
		if _, ok := seen[address]; ok {
			continue
		}
		seen[address] = struct{}{}
		addresses = append(addresses, address)
	}
	for len(addresses) > 0 {
		candidate := providerRecord{PeerID: result.PeerID, Addresses: addresses}
		data, err := json.Marshal(providerEvent{PeerID: candidate.PeerID, Addresses: candidate.Addresses})
		if err == nil && len(data) <= maxProviderEventSize {
			return candidate, true
		}
		addresses = addresses[:len(addresses)-1]
	}
	return providerRecord{}, false
}

func isPublicProviderAddr(addr multiaddr.Multiaddr) bool {
	return manet.IsPublicAddr(addr) &&
		!manet.IsIPLoopback(addr) &&
		!manet.IsIP6LinkLocal(addr) &&
		!manet.IsIPUnspecified(addr)
}

func writeProviderStream(w http.ResponseWriter, r *http.Request, results []providerRecord, complete providerCompleteEvent) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	flusher, _ := w.(http.Flusher)
	for _, result := range results {
		if !writeProviderEvent(w, "provider", providerEvent{PeerID: result.PeerID, Addresses: result.Addresses}) {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	if !writeProviderEvent(w, "complete", complete) {
		return
	}
	if flusher != nil {
		flusher.Flush()
	}
}

func streamProviderFlight(w http.ResponseWriter, r *http.Request, flight *providerFlight) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	index := 0
	for {
		flight.mu.Lock()
		if index < len(flight.results) {
			result := cloneProviderRecords(flight.results[index : index+1])[0]
			index++
			flight.mu.Unlock()
			if !writeProviderEvent(w, "provider", providerEvent{PeerID: result.PeerID, Addresses: result.Addresses}) {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			continue
		}
		if flight.complete {
			complete := providerCompleteEvent{
				Count:    len(flight.results),
				Duration: flight.duration.Milliseconds(),
				TimedOut: flight.timedOut,
			}
			flight.mu.Unlock()
			if writeProviderEvent(w, "complete", complete) && flusher != nil {
				flusher.Flush()
			}
			return
		}
		wait := flight.notify
		flight.mu.Unlock()
		select {
		case <-r.Context().Done():
			return
		case <-wait:
		}
	}
}

func writeProviderEvent(w http.ResponseWriter, name string, value any) bool {
	data, err := json.Marshal(value)
	if err != nil {
		return false
	}
	_, err = w.Write([]byte("event: " + name + "\ndata: " + string(data) + "\n\n"))
	return err == nil
}

func writeProviderError(w http.ResponseWriter, r *http.Request, status int, code string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	body := []byte(`{"error":{"code":"` + strings.ReplaceAll(code, `"`, "") + `"}}`)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}
