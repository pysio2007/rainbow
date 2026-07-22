package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/multiformats/go-multihash"
	"github.com/stretchr/testify/require"
)

type providerFinderFunc func(context.Context, cid.Cid, int) <-chan peer.AddrInfo

func (f providerFinderFunc) FindProvidersAsync(ctx context.Context, c cid.Cid, limit int) <-chan peer.AddrInfo {
	return f(ctx, c, limit)
}

func testProviderCID() cid.Cid {
	mh, err := multihash.Sum([]byte("providers"), multihash.SHA2_256, -1)
	if err != nil {
		panic(err)
	}
	return cid.NewCidV1(cid.Raw, mh)
}

func testProviderAddr(t *testing.T, value string) multiaddr.Multiaddr {
	t.Helper()
	a, err := multiaddr.NewMultiaddr(value)
	require.NoError(t, err)
	return a
}

func providerRequest(t *testing.T, handler http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	return providerRequestFrom(t, handler, method, target, "198.51.100.7:1234")
}

func providerRequestFrom(t *testing.T, handler http.Handler, method, target, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = remoteAddr
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func TestProvidersHandlerRejectsInvalidCIDAndUnavailableRouting(t *testing.T) {
	var calls atomic.Int32
	finder := providerFinderFunc(func(context.Context, cid.Cid, int) <-chan peer.AddrInfo {
		calls.Add(1)
		return make(chan peer.AddrInfo)
	})

	for _, tc := range []struct {
		name   string
		h      http.Handler
		target string
		status int
		code   string
	}{
		{"invalid", newProviderHandler(finder), providersAPIPath + "?cid=undefined", http.StatusBadRequest, "invalid_cid"},
		{"missing", newProviderHandler(finder), providersAPIPath, http.StatusBadRequest, "invalid_cid"},
		{"unavailable", newProviderHandler(nil), providersAPIPath + "?cid=" + testProviderCID().String(), http.StatusServiceUnavailable, "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := providerRequest(t, tc.h, http.MethodGet, tc.target)
			require.Equal(t, tc.status, res.Code)
			require.Equal(t, `{"error":{"code":"`+tc.code+`"}}`, strings.TrimSpace(res.Body.String()))
		})
	}
	require.Zero(t, calls.Load())
}

func TestProvidersHandlerSSEOrderAndPublicAddressFiltering(t *testing.T) {
	c := testProviderCID()
	p1 := peer.ID("peer-one")
	p2 := peer.ID("peer-two")
	finder := providerFinderFunc(func(ctx context.Context, got cid.Cid, limit int) <-chan peer.AddrInfo {
		require.Equal(t, c, got)
		require.Equal(t, 16, limit)
		out := make(chan peer.AddrInfo, 3)
		out <- peer.AddrInfo{ID: p1, Addrs: []multiaddr.Multiaddr{
			testProviderAddr(t, "/ip4/8.8.8.8/tcp/4001"),
			testProviderAddr(t, "/ip4/192.168.1.2/tcp/4001"),
			testProviderAddr(t, "/ip6/fe80::1/tcp/4001"),
		}}
		out <- peer.AddrInfo{ID: p1, Addrs: []multiaddr.Multiaddr{testProviderAddr(t, "/ip4/1.1.1.1/tcp/4001")}}
		out <- peer.AddrInfo{ID: p2, Addrs: []multiaddr.Multiaddr{testProviderAddr(t, "/ip4/127.0.0.1/tcp/4001")}}
		close(out)
		return out
	})

	res := providerRequest(t, newProviderHandler(finder), http.MethodGet, providersAPIPath+"?cid="+c.String())
	require.Equal(t, http.StatusOK, res.Code)
	require.Equal(t, "text/event-stream", res.Header().Get("Content-Type"))
	require.Equal(t, "no", res.Header().Get("X-Accel-Buffering"))
	body := res.Body.String()
	lines := bufio.NewScanner(strings.NewReader(body))
	var events []string
	for lines.Scan() {
		if strings.HasPrefix(lines.Text(), "event: ") {
			events = append(events, strings.TrimPrefix(lines.Text(), "event: "))
		}
	}
	require.Equal(t, []string{"provider", "complete"}, events)
	require.Contains(t, body, `"peerId":"`+p1.String()+`"`)
	require.Contains(t, body, `"addresses":["/ip4/8.8.8.8/tcp/4001"]`)
	require.NotContains(t, body, `"multiaddrs"`)
	require.NotContains(t, body, "192.168.1.2")
	require.NotContains(t, body, "fe80::1")
	require.NotContains(t, body, "127.0.0.1")
	require.Contains(t, body, `"count":1`)
}

func TestProvidersHandlerCacheAndConcurrentCIDMerge(t *testing.T) {
	c := testProviderCID()
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	finder := providerFinderFunc(func(ctx context.Context, got cid.Cid, limit int) <-chan peer.AddrInfo {
		calls.Add(1)
		close(started)
		out := make(chan peer.AddrInfo, 1)
		go func() {
			defer close(out)
			select {
			case out <- peer.AddrInfo{ID: peer.ID("cached-peer"), Addrs: []multiaddr.Multiaddr{testProviderAddr(t, "/ip4/8.8.4.4/tcp/4001")}}:
			case <-ctx.Done():
				return
			}
			select {
			case <-release:
			case <-ctx.Done():
			}
		}()
		return out
	})
	h := newProviderHandler(finder)
	api := h.(*providerAPI)
	responses := make(chan *httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			responses <- providerRequest(t, h, http.MethodGet, providersAPIPath+"?cid="+c.String())
		}()
	}
	<-started
	require.Eventually(t, func() bool {
		api.mu.Lock()
		defer api.mu.Unlock()
		for _, flight := range api.flights {
			flight.mu.Lock()
			refs := flight.refs
			flight.mu.Unlock()
			return refs == 2
		}
		return false
	}, time.Second, time.Millisecond)
	close(release)
	wg.Wait()
	close(responses)
	for res := range responses {
		require.Equal(t, http.StatusOK, res.Code)
		require.Contains(t, res.Body.String(), `"cached":false`)
	}
	require.Equal(t, int32(1), calls.Load())

	cached := providerRequestFrom(t, h, http.MethodGet, providersAPIPath+"?cid="+c.String(), "198.51.100.9:1234")
	require.Equal(t, http.StatusOK, cached.Code)
	require.Contains(t, cached.Body.String(), `"cached":true`)
	require.Equal(t, int32(1), calls.Load())
}

func TestProvidersHandlerLimitTimeoutCancellationAndRateLimit(t *testing.T) {
	c := testProviderCID()
	var cancelled atomic.Int32
	finder := providerFinderFunc(func(ctx context.Context, _ cid.Cid, limit int) <-chan peer.AddrInfo {
		require.Equal(t, 16, limit)
		out := make(chan peer.AddrInfo)
		go func() {
			defer close(out)
			for i := range 32 {
				select {
				case out <- peer.AddrInfo{ID: peer.ID("peer-" + string(rune('a'+i))), Addrs: []multiaddr.Multiaddr{testProviderAddr(t, "/ip4/8.8.8.8/tcp/4001")}}:
				case <-ctx.Done():
					cancelled.Add(1)
					return
				}
			}
		}()
		return out
	})
	h := newProviderHandlerWithOptions(finder, providerHandlerOptions{lookupTimeout: 20 * time.Millisecond})
	res := providerRequest(t, h, http.MethodGet, providersAPIPath+"?cid="+c.String())
	require.Equal(t, http.StatusOK, res.Code)
	require.Equal(t, 16, strings.Count(res.Body.String(), "event: provider"))
	require.Contains(t, res.Body.String(), `"timedOut":false`)

	blocked := providerFinderFunc(func(ctx context.Context, _ cid.Cid, _ int) <-chan peer.AddrInfo {
		out := make(chan peer.AddrInfo)
		go func() { <-ctx.Done(); cancelled.Add(1); close(out) }()
		return out
	})
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, providersAPIPath+"?cid="+testProviderCID().String(), nil).WithContext(ctx)
	req.RemoteAddr = "198.51.100.8:1234"
	done := make(chan struct{})
	go func() {
		newProviderHandler(blocked).ServeHTTP(httptest.NewRecorder(), req)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled request did not exit")
	}

	rateHandler := newProviderHandler(providerFinderFunc(func(context.Context, cid.Cid, int) <-chan peer.AddrInfo {
		out := make(chan peer.AddrInfo)
		close(out)
		return out
	}))
	for i := 0; i < 2; i++ {
		require.Equal(t, http.StatusOK, providerRequest(t, rateHandler, http.MethodGet, providersAPIPath+"?cid="+testProviderCID().String()).Code)
	}
	require.Equal(t, http.StatusTooManyRequests, providerRequest(t, rateHandler, http.MethodGet, providersAPIPath+"?cid="+testProviderCID().String()).Code)
	require.GreaterOrEqual(t, cancelled.Load(), int32(1))
}

func TestProvidersHandlerCancelledFlightIsRemovedBeforeLookupEnds(t *testing.T) {
	c := testProviderCID()
	var calls atomic.Int32
	started := make(chan struct{})
	finder := providerFinderFunc(func(ctx context.Context, _ cid.Cid, _ int) <-chan peer.AddrInfo {
		call := calls.Add(1)
		out := make(chan peer.AddrInfo)
		if call == 1 {
			close(started)
			go func() {
				<-ctx.Done()
				close(out)
			}()
			return out
		}
		close(out)
		return out
	})
	api := newProviderHandler(finder).(*providerAPI)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, providersAPIPath+"?cid="+c.String(), nil).WithContext(ctx)
	req.RemoteAddr = "198.51.100.8:1234"
	done := make(chan struct{})
	go func() {
		api.ServeHTTP(httptest.NewRecorder(), req)
		close(done)
	}()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled request did not exit")
	}
	require.Eventually(t, func() bool {
		api.mu.Lock()
		defer api.mu.Unlock()
		return len(api.flights) == 0
	}, time.Second, time.Millisecond)

	second := providerRequestFrom(t, api, http.MethodGet, providersAPIPath+"?cid="+c.String(), "198.51.100.8:1234")
	require.Equal(t, http.StatusOK, second.Code)
	require.Equal(t, int32(2), calls.Load())
	require.Eventually(t, func() bool {
		return len(api.live) == 0
	}, time.Second, time.Millisecond)
}

func TestProvidersHandlerLastReleaseCannotRaceNewSameCIDLookup(t *testing.T) {
	c := testProviderCID()
	var calls atomic.Int32
	firstStarted := make(chan struct{})
	finder := providerFinderFunc(func(ctx context.Context, _ cid.Cid, _ int) <-chan peer.AddrInfo {
		call := calls.Add(1)
		out := make(chan peer.AddrInfo)
		if call == 1 {
			close(firstStarted)
			go func() {
				<-ctx.Done()
				close(out)
			}()
			return out
		}
		close(out)
		return out
	})
	api := newProviderHandler(finder).(*providerAPI)
	removalStarted := make(chan struct{})
	allowRemoval := make(chan struct{})
	var removalOnce sync.Once
	api.afterLastFlightRelease = func() {
		removalOnce.Do(func() {
			close(removalStarted)
			<-allowRemoval
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	firstRequest := httptest.NewRequest(http.MethodGet, providersAPIPath+"?cid="+c.String(), nil).WithContext(ctx)
	firstRequest.RemoteAddr = "198.51.100.8:1234"
	firstDone := make(chan struct{})
	go func() {
		api.ServeHTTP(httptest.NewRecorder(), firstRequest)
		close(firstDone)
	}()
	<-firstStarted
	cancel()
	<-removalStarted

	secondDone := make(chan *httptest.ResponseRecorder)
	go func() {
		secondDone <- providerRequestFrom(t, api, http.MethodGet, providersAPIPath+"?cid="+c.String(), "198.51.100.8:1234")
	}()
	close(allowRemoval)

	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first request did not finish")
	}
	var second *httptest.ResponseRecorder
	select {
	case second = <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("second request did not finish")
	}
	require.Equal(t, http.StatusOK, second.Code)
	require.Contains(t, second.Body.String(), `"count":0`)
	require.Equal(t, int32(2), calls.Load())
}

func TestProviderLimiterHasCapacityAndIdleEviction(t *testing.T) {
	api := newProviderHandlerWithOptions(nil, providerHandlerOptions{limiterIdleTimeout: 10 * time.Millisecond}).(*providerAPI)
	require.True(t, api.allowClient("198.51.100.1"))
	time.Sleep(20 * time.Millisecond)
	require.True(t, api.allowClient("198.51.100.2"))
	api.mu.Lock()
	defer api.mu.Unlock()
	_, oldPresent := api.clients["198.51.100.1"]
	require.False(t, oldPresent)
	require.LessOrEqual(t, len(api.clients), providerLimiterCapacity)
}

func TestProviderRateLimitIdentityDoesNotTrustForwardedHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, providersAPIPath, nil)
	req.RemoteAddr = "198.51.100.3:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.8")
	require.Equal(t, "198.51.100.3", providerRemoteHost(req))
}

func TestProviderRecordAddressAndEventLimits(t *testing.T) {
	addresses := make([]multiaddr.Multiaddr, 0, maxProviderAddresses+4)
	for i := 0; i < cap(addresses); i++ {
		addresses = append(addresses, testProviderAddr(t, "/ip4/8.8.8.8/tcp/"+strconv.Itoa(4001+i)))
	}
	record, ok := publicProviderRecord(peer.AddrInfo{ID: peer.ID("peer"), Addrs: addresses})
	require.True(t, ok)
	require.Len(t, record.Addresses, maxProviderAddresses)
	for _, address := range record.Addresses {
		require.LessOrEqual(t, len(address), maxProviderAddressLength)
	}
	data, err := json.Marshal(providerEvent{PeerID: record.PeerID, Addresses: record.Addresses})
	require.NoError(t, err)
	require.LessOrEqual(t, len(data), maxProviderEventSize)
}

func TestPublicProviderRecordBoundsInspectionAndOutput(t *testing.T) {
	addresses := make([]multiaddr.Multiaddr, maxProviderInspectAddresses+1)
	for i := range addresses {
		addresses[i] = testProviderAddr(t, "/ip4/192.168.1.2/tcp/4001")
	}
	addresses[maxProviderInspectAddresses] = testProviderAddr(t, "/ip4/8.8.8.8/tcp/4001")

	record, ok := publicProviderRecord(peer.AddrInfo{ID: peer.ID("peer"), Addrs: addresses})
	require.False(t, ok)
	require.Empty(t, record)

	large := make([]multiaddr.Multiaddr, maxProviderInspectAddresses+100_000)
	for i := range large {
		port := 5000
		if i < maxProviderAddresses {
			port = 4001 + i
		}
		large[i] = testProviderAddr(t, "/ip4/8.8.8.8/tcp/"+strconv.Itoa(port))
	}
	record, ok = publicProviderRecord(peer.AddrInfo{ID: peer.ID("peer"), Addrs: large})
	require.True(t, ok)
	require.Len(t, record.Addresses, maxProviderAddresses)
	require.LessOrEqual(t, cap(record.Addresses), maxProviderAddresses)

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, ok = publicProviderRecord(peer.AddrInfo{ID: peer.ID("peer"), Addrs: large})
	runtime.ReadMemStats(&after)
	require.True(t, ok)
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(1<<20))
}

func TestProviderCacheStoresFilteredBoundedDeepCopies(t *testing.T) {
	cache := newProviderCache()
	results := []providerRecord{{
		PeerID: "peer",
		Addresses: []string{
			"/ip4/8.8.8.8/tcp/4001",
			"/ip4/127.0.0.1/tcp/4001",
		},
	}}
	cache.put("cid", results, time.Now())
	results[0].Addresses[0] = "/ip4/1.1.1.1/tcp/4001"
	got, ok := cache.get("cid", time.Now())
	require.True(t, ok)
	require.Equal(t, []string{"/ip4/8.8.8.8/tcp/4001"}, got[0].Addresses)
	got[0].Addresses[0] = "/ip4/9.9.9.9/tcp/4001"
	gotAgain, ok := cache.get("cid", time.Now())
	require.True(t, ok)
	require.Equal(t, []string{"/ip4/8.8.8.8/tcp/4001"}, gotAgain[0].Addresses)
}

func TestProvidersHandlerMethodsAndUIPathMapping(t *testing.T) {
	c := testProviderCID()
	h := newProviderHandler(providerFinderFunc(func(_ context.Context, _ cid.Cid, _ int) <-chan peer.AddrInfo { return nil }))
	post := providerRequest(t, h, http.MethodPost, providersAPIPath+"?cid="+c.String())
	require.Equal(t, http.StatusMethodNotAllowed, post.Code)
	require.Equal(t, "GET, HEAD", post.Header().Get("Allow"))

	head := providerRequest(t, newProviderHandler(nil), http.MethodHead, providersAPIPath+"?cid="+c.String())
	require.Equal(t, http.StatusServiceUnavailable, head.Code)
	require.Empty(t, head.Body.String())
	availableHead := providerRequest(t, newProviderHandler(providerFinderFunc(func(_ context.Context, _ cid.Cid, _ int) <-chan peer.AddrInfo {
		return nil
	})), http.MethodHead, providersAPIPath+"?cid="+c.String())
	require.Equal(t, http.StatusOK, availableHead.Code)
	require.Empty(t, availableHead.Body.String())
	require.Equal(t, "text/event-stream", availableHead.Header().Get("Content-Type"))
	require.Equal(t, "no-store", availableHead.Header().Get("Cache-Control"))
	require.Equal(t, "no", availableHead.Header().Get("X-Accel-Buffering"))

	gate := withUIHostGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }), http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("provider route escaped UI gate") }))
	res := providerRequest(t, gate, http.MethodGet, providersAPIPath+"?cid="+c.String())
	require.Equal(t, http.StatusTeapot, res.Code)
}

func TestProviderEventJSONShape(t *testing.T) {
	var got map[string]any
	event := providerEvent{PeerID: "p", Addresses: []string{"/ip4/8.8.8.8/tcp/1"}}
	b, err := json.Marshal(event)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &got))
	require.Equal(t, "p", got["peerId"])
	require.Equal(t, []any{"/ip4/8.8.8.8/tcp/1"}, got["addresses"])
	require.NotContains(t, got, "multiaddrs")
}
