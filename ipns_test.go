package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ipfs/boxo/ipns"
	boxopath "github.com/ipfs/boxo/path"
	ic "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
	"github.com/stretchr/testify/require"
)

type ipnsStoreFixture struct {
	value       []byte
	err         error
	calls       atomic.Int32
	searchCalls atomic.Int32
	key         string
}

func (s *ipnsStoreFixture) GetValue(ctx context.Context, key string, _ ...routing.Option) ([]byte, error) {
	s.calls.Add(1)
	s.key = key
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.value, nil
}

func (s *ipnsStoreFixture) SearchValue(context.Context, string, ...any) (<-chan []byte, error) {
	s.searchCalls.Add(1)
	return nil, errors.New("SearchValue must not be called")
}

func ipnsSignedRecord(t *testing.T, sk ic.PrivKey, sequence uint64, eol time.Time) ([]byte, ipns.Name) {
	t.Helper()
	pid, err := peer.IDFromPrivateKey(sk)
	require.NoError(t, err)
	name := ipns.NameFromPeer(pid)
	target, err := boxopath.NewPath("/ipfs/" + testProviderCID().String())
	require.NoError(t, err)
	record, err := ipns.NewRecord(sk, target, sequence, eol, time.Minute)
	require.NoError(t, err)
	raw, err := ipns.MarshalRecord(record)
	require.NoError(t, err)
	return raw, name
}

func ipnsRequest(handler http.Handler, method, query, remote string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, ipnsAPIPath+query, nil)
	req.RemoteAddr = remote
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func requireIPNSWire(t *testing.T, data []byte) ipnsResponse {
	t.Helper()
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))
	require.ElementsMatch(t, []string{"version", "name", "record"}, mapKeys(raw))
	var body ipnsResponse
	require.NoError(t, json.Unmarshal(data, &body))
	require.Equal(t, 1, body.Version)
	return body
}

func TestIPNSHandlerValidatesAndUsesOneGetValue(t *testing.T) {
	sk, _, err := ic.GenerateKeyPair(ic.Ed25519, -1)
	require.NoError(t, err)
	eol := time.Date(2030, time.January, 2, 3, 4, 5, 123456789, time.UTC)
	raw, name := ipnsSignedRecord(t, sk, 7, eol)
	store := &ipnsStoreFixture{value: raw}
	res := ipnsRequest(newIPNSHandler(store), http.MethodGet, "?name="+name.Peer().String(), "198.51.100.50:1234")
	require.Equal(t, http.StatusOK, res.Code)
	body := requireIPNSWire(t, res.Body.Bytes())
	require.Equal(t, "validated", body.Record.Status)
	require.Equal(t, name.Peer().String(), body.Name)
	require.Equal(t, "reported", body.Record.Target.Status)
	require.Equal(t, string(name.RoutingKey()), store.key)
	require.Equal(t, int32(1), store.calls.Load())
	require.Zero(t, store.searchCalls.Load())
	require.NotEmpty(t, body.Record.Sequence)
	require.Equal(t, eol.Format(time.RFC3339Nano), body.Record.EOL)
	require.NotEmpty(t, body.Record.TTLNanos)
}

func TestIPNSHandlerRecordErrorsAndBounds(t *testing.T) {
	sk, _, err := ic.GenerateKeyPair(ic.Ed25519, -1)
	require.NoError(t, err)
	valid, name := ipnsSignedRecord(t, sk, 1, time.Now().Add(time.Hour))
	otherKey, _, err := ic.GenerateKeyPair(ic.Ed25519, -1)
	require.NoError(t, err)
	wrongSignature, _ := ipnsSignedRecord(t, otherKey, 1, time.Now().Add(time.Hour))
	expired, _ := ipnsSignedRecord(t, sk, 2, time.Now().Add(-time.Hour))
	for _, tc := range []struct {
		name   string
		value  []byte
		status string
	}{
		{"malformed", []byte("not a record"), "invalid_record"},
		{"wrong-signature", wrongSignature, "invalid_record"},
		{"expired", expired, "expired_record"},
		{"oversize", append(valid, make([]byte, 16<<10)...), "invalid_record"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &ipnsStoreFixture{value: tc.value}
			res := ipnsRequest(newIPNSHandler(store), http.MethodGet, "?name="+name.Peer().String(), "198.51.100.51:1234")
			require.Equal(t, http.StatusOK, res.Code)
			body := requireIPNSWire(t, res.Body.Bytes())
			require.Equal(t, tc.status, body.Record.Status)
		})
	}
	for _, query := range []string{"", "?name=not-a-peer", "?name=" + name.Peer().String() + "&extra=1", "?name=" + strings.Repeat("a", 257)} {
		res := ipnsRequest(newIPNSHandler(nil), http.MethodGet, query, "198.51.100.52:1234")
		require.Equal(t, http.StatusBadRequest, res.Code)
	}
}

func TestIPNSHandlerUnavailableTimeoutCancelAndRateLimit(t *testing.T) {
	_, pid := mustTestPeer(t)
	name := ipns.NameFromPeer(pid)
	res := ipnsRequest(newIPNSHandler(nil), http.MethodGet, "?name="+name.Peer().String(), "198.51.100.53:1234")
	require.Equal(t, http.StatusOK, res.Code)
	require.Equal(t, "unavailable", requireIPNSWire(t, res.Body.Bytes()).Record.Status)
	store := &ipnsStoreFixture{err: context.DeadlineExceeded}
	handler := newIPNSHandlerWithOptions(store, ipnsHandlerOptions{timeout: time.Nanosecond})
	res = ipnsRequest(handler, http.MethodGet, "?name="+name.Peer().String(), "198.51.100.54:1234")
	require.Equal(t, "timeout", requireIPNSWire(t, res.Body.Bytes()).Record.Status)
	for i := 0; i < 2; i++ {
		res = ipnsRequest(newIPNSHandler(nil), http.MethodGet, "?name="+name.Peer().String(), "198.51.100.55:1234")
		require.Equal(t, http.StatusOK, res.Code)
	}
	handler = newIPNSHandler(nil)
	for i := 0; i < 2; i++ {
		_ = ipnsRequest(handler, http.MethodGet, "?name="+name.Peer().String(), "198.51.100.56:1234")
	}
	res = ipnsRequest(handler, http.MethodGet, "?name="+name.Peer().String(), "198.51.100.56:1234")
	require.Equal(t, http.StatusTooManyRequests, res.Code)
}
