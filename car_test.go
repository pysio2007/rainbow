package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ipfs/boxo/gateway"
	boxopath "github.com/ipfs/boxo/path"
	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
	cbor "github.com/ipfs/go-ipld-cbor"
	"github.com/multiformats/go-multicodec"
	"github.com/stretchr/testify/require"
)

type carGetterFunc func(context.Context, boxopath.ImmutablePath, gateway.CarParams) (gateway.ContentPathMetadata, io.ReadCloser, error)

type carTestHeader struct {
	Roots   []cid.Cid
	Version uint64
}

func init() { cbor.RegisterCborType(carTestHeader{}) }

func (f carGetterFunc) GetCAR(ctx context.Context, path boxopath.ImmutablePath, params gateway.CarParams) (gateway.ContentPathMetadata, io.ReadCloser, error) {
	return f(ctx, path, params)
}

func carV1Bytes(root cid.Cid, entries ...blocks.Block) []byte {
	headerData, err := cbor.DumpObject(carTestHeader{Roots: []cid.Cid{root}, Version: 1})
	if err != nil {
		panic(err)
	}
	header := headerData
	var out []byte
	var length [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(length[:], uint64(len(header)))
	out = append(out, length[:n]...)
	out = append(out, header...)
	for _, entry := range entries {
		section := append(append([]byte{}, entry.Cid().Bytes()...), entry.RawData()...)
		n = binary.PutUvarint(length[:], uint64(len(section)))
		out = append(out, length[:n]...)
		out = append(out, section...)
	}
	return out
}

func carRequest(handler http.Handler, method, query, remote string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, carAPIPath+query, nil)
	req.RemoteAddr = remote
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func carStatus(t *testing.T, data []byte) (map[string]json.RawMessage, map[string]json.RawMessage) {
	t.Helper()
	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &body))
	require.ElementsMatch(t, []string{"version", "cid", "verification"}, mapKeys(body))
	var verification map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body["verification"], &verification))
	return body, verification
}

func TestCARHandlerVerifiesV1RootAndCleanEOF(t *testing.T) {
	root, rootBlock := metadataBlock(t, []byte("root"), uint64(multicodec.Raw))
	carBytes := carV1Bytes(root, rootBlock)
	var calls atomic.Int32
	getter := carGetterFunc(func(ctx context.Context, path boxopath.ImmutablePath, params gateway.CarParams) (gateway.ContentPathMetadata, io.ReadCloser, error) {
		calls.Add(1)
		require.Equal(t, root, path.RootCid())
		require.Equal(t, gateway.DagScopeBlock, params.Scope)
		return gateway.ContentPathMetadata{}, io.NopCloser(bytes.NewReader(carBytes)), nil
	})
	res := carRequest(newCARHandler(getter, false), http.MethodGet, "?cid="+root.String(), "198.51.100.60:1234")
	require.Equal(t, http.StatusOK, res.Code)
	body, verification := carStatus(t, res.Body.Bytes())
	var returnedCID string
	require.NoError(t, json.Unmarshal(body["cid"], &returnedCID))
	require.Equal(t, root.String(), returnedCID)
	require.Equal(t, int32(1), calls.Load())
	var status string
	require.NoError(t, json.Unmarshal(verification["status"], &status))
	require.Equal(t, "verified", status)
	var rootPresent, rootVerified bool
	require.NoError(t, json.Unmarshal(verification["rootBlockPresent"], &rootPresent))
	require.NoError(t, json.Unmarshal(verification["rootBlockVerified"], &rootVerified))
	require.True(t, rootPresent)
	require.True(t, rootVerified)
}

func TestCARHandlerRejectsBadRootsHashesTruncationAndLimits(t *testing.T) {
	root, rootBlock := metadataBlock(t, []byte("root"), uint64(multicodec.Raw))
	other, otherBlock := metadataBlock(t, []byte("other"), uint64(multicodec.Raw))
	tests := []struct {
		name   string
		data   []byte
		status string
	}{
		{"wrong-root", carV1Bytes(other, rootBlock), "root_mismatch"},
		{"extra-root-block", carV1Bytes(root, rootBlock, rootBlock), "unexpected_block_count"},
		{"extra-valid-block", carV1Bytes(root, rootBlock, otherBlock), "unexpected_block_count"},
		{"truncated", carV1Bytes(root, rootBlock)[:len(carV1Bytes(root, rootBlock))-1], "invalid_car"},
		{"hash-mismatch", carV1Bytes(root, metadataRawBlock{raw: []byte("bad"), cid: root}), "integrity_error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := carRequest(newCARHandler(carGetterFunc(func(context.Context, boxopath.ImmutablePath, gateway.CarParams) (gateway.ContentPathMetadata, io.ReadCloser, error) {
				return gateway.ContentPathMetadata{}, io.NopCloser(bytes.NewReader(tc.data)), nil
			}), false), http.MethodGet, "?cid="+root.String(), "198.51.100.61:1234")
			_, verification := carStatus(t, res.Body.Bytes())
			var status string
			require.NoError(t, json.Unmarshal(verification["status"], &status))
			require.Equal(t, tc.status, status)
		})
	}
	_, large := metadataBlock(t, bytes.Repeat([]byte{'x'}, carSectionLimit+1), uint64(multicodec.Raw))
	res := carRequest(newCARHandler(carGetterFunc(func(context.Context, boxopath.ImmutablePath, gateway.CarParams) (gateway.ContentPathMetadata, io.ReadCloser, error) {
		return gateway.ContentPathMetadata{}, io.NopCloser(bytes.NewReader(carV1Bytes(root, large))), nil
	}), false), http.MethodGet, "?cid="+root.String(), "198.51.100.62:1234")
	_, verification := carStatus(t, res.Body.Bytes())
	var status string
	require.NoError(t, json.Unmarshal(verification["status"], &status))
	require.Equal(t, "limit", status)
}

func TestCARHandlerRemoteCARCancelAdmissionAndInput(t *testing.T) {
	root, block := metadataBlock(t, []byte("root"), uint64(multicodec.Raw))
	var calls atomic.Int32
	getter := carGetterFunc(func(context.Context, boxopath.ImmutablePath, gateway.CarParams) (gateway.ContentPathMetadata, io.ReadCloser, error) {
		calls.Add(1)
		return gateway.ContentPathMetadata{}, io.NopCloser(bytes.NewReader(carV1Bytes(root, block))), nil
	})
	res := carRequest(newCARHandler(getter, true), http.MethodGet, "?cid="+root.String(), "198.51.100.63:1234")
	_, verification := carStatus(t, res.Body.Bytes())
	var status string
	require.NoError(t, json.Unmarshal(verification["status"], &status))
	require.Equal(t, "unsupported_mode", status)
	require.Zero(t, calls.Load())
	res = carRequest(newCARHandler(getter, false), http.MethodPost, "?cid="+root.String(), "198.51.100.63:1234")
	require.Equal(t, http.StatusMethodNotAllowed, res.Code)
	for _, query := range []string{"", "?cid=not-cid", "?cid=" + root.String() + "&extra=1"} {
		res = carRequest(newCARHandler(getter, false), http.MethodGet, query, "198.51.100.64:1234")
		require.Equal(t, http.StatusBadRequest, res.Code)
	}
	getter = carGetterFunc(func(ctx context.Context, _ boxopath.ImmutablePath, _ gateway.CarParams) (gateway.ContentPathMetadata, io.ReadCloser, error) {
		<-ctx.Done()
		return gateway.ContentPathMetadata{}, nil, ctx.Err()
	})
	res = carRequest(newCARHandlerWithOptions(getter, carHandlerOptions{timeout: time.Nanosecond}), http.MethodGet, "?cid="+root.String(), "198.51.100.65:1234")
	_, verification = carStatus(t, res.Body.Bytes())
	require.NoError(t, json.Unmarshal(verification["status"], &status))
	require.Equal(t, "timeout", status)
}
