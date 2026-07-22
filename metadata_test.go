package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
	unixfsdata "github.com/ipfs/go-unixfsnode/data"
	unixfsbuilder "github.com/ipfs/go-unixfsnode/data/builder"
	dagpb "github.com/ipld/go-codec-dagpb"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/multiformats/go-multicodec"
	"github.com/multiformats/go-multihash"
	"github.com/stretchr/testify/require"
)

type metadataBlockGetterFunc func(context.Context, cid.Cid) (blocks.Block, error)

type metadataRawBlock struct {
	raw []byte
	cid cid.Cid
}

func (b metadataRawBlock) RawData() []byte          { return b.raw }
func (b metadataRawBlock) Cid() cid.Cid             { return b.cid }
func (b metadataRawBlock) String() string           { return b.cid.String() }
func (b metadataRawBlock) Loggable() map[string]any { return map[string]any{} }

func (f metadataBlockGetterFunc) GetBlock(ctx context.Context, c cid.Cid) (blocks.Block, error) {
	return f(ctx, c)
}

func metadataTestCID(raw []byte, codec uint64) cid.Cid {
	mh, err := multihash.Sum(raw, multihash.SHA2_256, -1)
	if err != nil {
		panic(err)
	}
	return cid.NewCidV1(codec, mh)
}

func metadataBlock(t *testing.T, raw []byte, codec uint64) (cid.Cid, blocks.Block) {
	t.Helper()
	c := metadataTestCID(raw, codec)
	bl, err := blocks.NewBlockWithCid(raw, c)
	require.NoError(t, err)
	return c, bl
}

func metadataRequest(handler http.Handler, method, query, remote string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, metadataAPIPath+query, nil)
	req.RemoteAddr = remote
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func requireFrozenMetadataSchema(t *testing.T, data []byte) metadataResponse {
	t.Helper()
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))
	require.ElementsMatch(t, []string{"version", "parsedCid", "root", "canonicalLinks"}, mapKeys(raw))
	var body metadataResponse
	require.NoError(t, json.Unmarshal(data, &body))
	require.Equal(t, 1, body.Version)
	var parsed map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["parsedCid"], &parsed))
	require.ElementsMatch(t, []string{"canonical", "version", "codec", "multihash", "digestLength"}, mapKeys(parsed))
	var root map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["root"], &root))
	rootKeys := []string{"status", "blockBytes", "blockVerified", "directLinks"}
	if body.Root.UnixFS != nil {
		rootKeys = append(rootKeys, "unixfs")
	}
	require.ElementsMatch(t, rootKeys, mapKeys(root))
	if body.Root.UnixFS != nil {
		var unixfs map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(root["unixfs"], &unixfs))
		require.Contains(t, mapKeys(unixfs), "type")
	}
	var links map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(root["directLinks"], &links))
	require.ElementsMatch(t, []string{"total", "shown", "truncated", "items"}, mapKeys(links))
	var canonicalLinks map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["canonicalLinks"], &canonicalLinks))
	require.ElementsMatch(t, []string{"ipfs", "nativeGateway", "explorer", "inspector", "car"}, mapKeys(canonicalLinks))
	return body
}

func mapKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func TestMetadataHandlerRejectsInvalidQueriesAndMethods(t *testing.T) {
	getter := metadataBlockGetterFunc(func(context.Context, cid.Cid) (blocks.Block, error) {
		t.Fatal("invalid request reached block getter")
		return nil, nil
	})
	handler := newMetadataHandler(getter)
	identityHash, err := multihash.Sum([]byte("identity"), multihash.IDENTITY, -1)
	require.NoError(t, err)
	identity := cid.NewCidV1(cid.Raw, identityHash)
	for _, tc := range []struct {
		name  string
		query string
		code  int
	}{
		{"missing", "", http.StatusBadRequest},
		{"extra", "?cid=" + testProviderCID().String() + "&x=1", http.StatusBadRequest},
		{"duplicate", "?cid=" + testProviderCID().String() + "&cid=" + testProviderCID().String(), http.StatusBadRequest},
		{"invalid", "?cid=not-a-cid", http.StatusBadRequest},
		{"identity", "?cid=" + identity.String(), http.StatusBadRequest},
		{"too-long", "?cid=" + strings.Repeat("b", 5000), http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := metadataRequest(handler, http.MethodGet, tc.query, "198.51.100.10:1234")
			require.Equal(t, tc.code, res.Code)
			require.Equal(t, "no-store", res.Header().Get("Cache-Control"))
			require.Contains(t, res.Body.String(), `"code":"invalid_cid"`)
		})
	}
	res := metadataRequest(handler, http.MethodPost, "?cid="+testProviderCID().String(), "198.51.100.10:1234")
	require.Equal(t, http.StatusMethodNotAllowed, res.Code)
	require.Equal(t, "GET, HEAD", res.Header().Get("Allow"))
}

func TestMetadataHandlerRawFetchIsSingleRootGet(t *testing.T) {
	raw := []byte("root bytes")
	c, block := metadataBlock(t, raw, uint64(multicodec.Raw))
	var calls atomic.Int32
	handler := newMetadataHandler(metadataBlockGetterFunc(func(ctx context.Context, got cid.Cid) (blocks.Block, error) {
		calls.Add(1)
		require.Equal(t, c, got)
		return block, nil
	}))
	res := metadataRequest(handler, http.MethodGet, "?cid="+c.String(), "198.51.100.10:1234")
	require.Equal(t, http.StatusOK, res.Code)
	require.Equal(t, int32(1), calls.Load())
	body := requireFrozenMetadataSchema(t, res.Body.Bytes())
	require.Equal(t, c.String(), body.ParsedCID.Canonical)
	require.Equal(t, multicodec.Code(multicodec.Raw).String(), body.ParsedCID.Codec)
	require.Equal(t, "fetched", body.Root.Status)
	require.Equal(t, len(raw), body.Root.BlockBytes)
	require.True(t, body.Root.BlockVerified)
	require.Equal(t, "ipfs://"+c.String(), body.CanonicalLinks.IPFS)
	require.Empty(t, body.Root.UnixFS)
}

func TestMetadataHandlerUnknownMultihashHasStableName(t *testing.T) {
	const unknownCode = uint64(0x123456)
	hash, err := multihash.Encode([]byte("unknown digest"), unknownCode)
	require.NoError(t, err)
	c := cid.NewCidV1(uint64(multicodec.Raw), hash)
	handler := newMetadataHandler(metadataBlockGetterFunc(func(context.Context, cid.Cid) (blocks.Block, error) {
		return metadataRawBlock{raw: []byte("unknown digest"), cid: c}, nil
	}))
	res := metadataRequest(handler, http.MethodGet, "?cid="+c.String(), "198.51.100.26:1234")
	require.Equal(t, http.StatusOK, res.Code)
	body := requireFrozenMetadataSchema(t, res.Body.Bytes())
	require.Equal(t, "code-1193046", body.ParsedCID.Multihash)
	require.NotEmpty(t, body.ParsedCID.Multihash)
}

func metadataDAGPBBlock(t *testing.T) (cid.Cid, blocks.Block, cid.Cid) {
	t.Helper()
	child, _ := metadataBlock(t, []byte("child"), uint64(multicodec.Raw))
	dataNode, err := unixfsbuilder.BuildUnixFS(func(b *unixfsbuilder.Builder) {
		unixfsbuilder.DataType(b, unixfsdata.Data_File)
		unixfsbuilder.FileSize(b, 12)
		unixfsbuilder.Data(b, []byte("root payload"))
		unixfsbuilder.Permissions(b, 0o640)
		unixfsbuilder.Mtime(b, func(tb unixfsbuilder.TimeBuilder) {
			unixfsbuilder.Seconds(tb, 1700000000)
		})
	})
	require.NoError(t, err)
	link, err := unixfsbuilder.BuildUnixFSDirectoryEntry("child.txt", 5, cidlink.Link{Cid: child})
	require.NoError(t, err)
	pbb := dagpb.Type.PBNode.NewBuilder()
	pbm, err := pbb.BeginMap(2)
	require.NoError(t, err)
	require.NoError(t, pbm.AssembleKey().AssignString("Data"))
	require.NoError(t, pbm.AssembleValue().AssignBytes(unixfsdata.EncodeUnixFSData(dataNode)))
	require.NoError(t, pbm.AssembleKey().AssignString("Links"))
	links, err := pbm.AssembleValue().BeginList(1)
	require.NoError(t, err)
	require.NoError(t, links.AssembleValue().AssignNode(link))
	require.NoError(t, links.Finish())
	require.NoError(t, pbm.Finish())
	node := pbb.Build().(dagpb.PBNode)
	var raw bytes.Buffer
	require.NoError(t, dagpb.Encode(node, &raw))
	root, block := metadataBlock(t, raw.Bytes(), uint64(multicodec.DagPb))
	return root, block, child
}

func TestMetadataHandlerDAGPBUnixFSAndDirectLinks(t *testing.T) {
	c, block, child := metadataDAGPBBlock(t)
	handler := newMetadataHandler(metadataBlockGetterFunc(func(context.Context, cid.Cid) (blocks.Block, error) {
		return block, nil
	}))
	res := metadataRequest(handler, http.MethodGet, "?cid="+c.String(), "198.51.100.10:1234")
	require.Equal(t, http.StatusOK, res.Code)
	body := requireFrozenMetadataSchema(t, res.Body.Bytes())
	require.Equal(t, "file", body.Root.UnixFS.Type)
	require.Equal(t, int64(12), *body.Root.UnixFS.DeclaredFileSize)
	require.Equal(t, int64(0o640), *body.Root.UnixFS.Mode)
	require.Equal(t, int64(1700000000), *body.Root.UnixFS.Mtime)
	require.Equal(t, 1, body.Root.DirectLinks.Total)
	require.Equal(t, child.String(), body.Root.DirectLinks.Items[0].CID)
	require.Equal(t, "child.txt", body.Root.DirectLinks.Items[0].Name)
	require.Equal(t, "/inspect/"+c.String(), body.CanonicalLinks.Inspector)
	require.Equal(t, "/ipfs/"+c.String()+"?format=car", body.CanonicalLinks.CAR)
}

func TestMetadataHandlerPreservesUnnamedDirectLinks(t *testing.T) {
	child, _ := metadataBlock(t, []byte("child"), uint64(multicodec.Raw))
	link, err := unixfsbuilder.BuildUnixFSDirectoryEntry("", 0, cidlink.Link{Cid: child})
	require.NoError(t, err)
	pbb := dagpb.Type.PBNode.NewBuilder()
	pbm, err := pbb.BeginMap(1)
	require.NoError(t, err)
	require.NoError(t, pbm.AssembleKey().AssignString("Links"))
	links, err := pbm.AssembleValue().BeginList(1)
	require.NoError(t, err)
	require.NoError(t, links.AssembleValue().AssignNode(link))
	require.NoError(t, links.Finish())
	require.NoError(t, pbm.Finish())
	var raw bytes.Buffer
	require.NoError(t, dagpb.Encode(pbb.Build(), &raw))
	c, block := metadataBlock(t, raw.Bytes(), uint64(multicodec.DagPb))
	res := metadataRequest(newMetadataHandler(metadataBlockGetterFunc(func(context.Context, cid.Cid) (blocks.Block, error) {
		return block, nil
	})), http.MethodGet, "?cid="+c.String(), "198.51.100.25:1234")
	body := requireFrozenMetadataSchema(t, res.Body.Bytes())
	require.Len(t, body.Root.DirectLinks.Items, 1)
	require.Empty(t, body.Root.DirectLinks.Items[0].Name)
	var wire map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &wire))
	var rootWire map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(wire["root"], &rootWire))
	var directWire map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rootWire["directLinks"], &directWire))
	var items []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(directWire["items"], &items))
	require.ElementsMatch(t, []string{"cid"}, mapKeys(items[0]))
}

func metadataDAGPBLinksBlock(t *testing.T, count int) (cid.Cid, blocks.Block) {
	t.Helper()
	child, _ := metadataBlock(t, []byte("child"), uint64(multicodec.Raw))
	links := make([]dagpb.PBLink, 0, count)
	for i := 0; i < count; i++ {
		link, err := unixfsbuilder.BuildUnixFSDirectoryEntry("link-"+strconv.Itoa(i), 0, cidlink.Link{Cid: child})
		require.NoError(t, err)
		links = append(links, link)
	}
	pbb := dagpb.Type.PBNode.NewBuilder()
	pbm, err := pbb.BeginMap(1)
	require.NoError(t, err)
	require.NoError(t, pbm.AssembleKey().AssignString("Links"))
	list, err := pbm.AssembleValue().BeginList(int64(len(links)))
	require.NoError(t, err)
	for _, link := range links {
		require.NoError(t, list.AssembleValue().AssignNode(link))
	}
	require.NoError(t, list.Finish())
	require.NoError(t, pbm.Finish())
	var raw bytes.Buffer
	require.NoError(t, dagpb.Encode(pbb.Build(), &raw))
	return metadataBlock(t, raw.Bytes(), uint64(multicodec.DagPb))
}

func metadataUnixFSBlock(t *testing.T, dataType int64) (cid.Cid, blocks.Block) {
	t.Helper()
	dataNode, err := unixfsbuilder.BuildUnixFS(func(b *unixfsbuilder.Builder) {
		unixfsbuilder.DataType(b, dataType)
		if dataType == unixfsdata.Data_Symlink {
			unixfsbuilder.Data(b, []byte("target"))
		}
	})
	require.NoError(t, err)
	pbb := dagpb.Type.PBNode.NewBuilder()
	pbm, err := pbb.BeginMap(2)
	require.NoError(t, err)
	require.NoError(t, pbm.AssembleKey().AssignString("Data"))
	require.NoError(t, pbm.AssembleValue().AssignBytes(unixfsdata.EncodeUnixFSData(dataNode)))
	require.NoError(t, pbm.AssembleKey().AssignString("Links"))
	links, err := pbm.AssembleValue().BeginList(0)
	require.NoError(t, err)
	require.NoError(t, links.Finish())
	require.NoError(t, pbm.Finish())
	var raw bytes.Buffer
	require.NoError(t, dagpb.Encode(pbb.Build(), &raw))
	return metadataBlock(t, raw.Bytes(), uint64(multicodec.DagPb))
}

func TestMetadataHandlerRootStatusesAndDirectLinkCap(t *testing.T) {
	raw := []byte("root")
	c, _ := metadataBlock(t, raw, uint64(multicodec.Raw))
	unsupportedCID, unsupportedBlock := metadataBlock(t, raw, uint64(multicodec.DagCbor))
	malformedCID, malformedBlock := metadataBlock(t, []byte{0xff, 0x00}, uint64(multicodec.DagPb))
	oversizeCID, oversizeBlock := metadataBlock(t, bytes.Repeat([]byte{'x'}, metadataBlockLimit+1), uint64(multicodec.Raw))
	byteMismatch := metadataRawBlock{raw: []byte("different"), cid: c}
	tests := []struct {
		name     string
		cid      cid.Cid
		block    blocks.Block
		err      error
		status   string
		verified bool
	}{
		{"not-found", c, nil, errors.New("backend: block not found"), "not_found", false},
		{"cancelled", c, nil, context.Canceled, "cancelled", false},
		{"unsupported-codec", unsupportedCID, unsupportedBlock, nil, "unsupported_codec", true},
		{"malformed-dagpb", malformedCID, malformedBlock, nil, "malformed", true},
		{"oversize", oversizeCID, oversizeBlock, nil, "block_too_large", false},
		{"byte-mismatch", c, byteMismatch, nil, "integrity_error", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := newMetadataHandler(metadataBlockGetterFunc(func(context.Context, cid.Cid) (blocks.Block, error) {
				return tc.block, tc.err
			}))
			res := metadataRequest(handler, http.MethodGet, "?cid="+tc.cid.String(), "198.51.100.20:1234")
			require.Equal(t, http.StatusOK, res.Code)
			var body metadataResponse
			require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
			require.Equal(t, tc.status, body.Root.Status)
			require.Equal(t, tc.verified, body.Root.BlockVerified)
			require.NotContains(t, res.Body.String(), "backend: block not found")
			if tc.name == "oversize" {
				require.Equal(t, len(oversizeBlock.RawData()), body.Root.BlockBytes)
			}
		})
	}
	truncatedCID, truncatedBlock := metadataDAGPBLinksBlock(t, metadataLinkLimit+1)
	res := metadataRequest(newMetadataHandler(metadataBlockGetterFunc(func(context.Context, cid.Cid) (blocks.Block, error) {
		return truncatedBlock, nil
	})), http.MethodGet, "?cid="+truncatedCID.String(), "198.51.100.21:1234")
	body := requireFrozenMetadataSchema(t, res.Body.Bytes())
	require.Equal(t, metadataLinkLimit+1, body.Root.DirectLinks.Total)
	require.Equal(t, metadataLinkLimit, body.Root.DirectLinks.Shown)
	require.True(t, body.Root.DirectLinks.Truncated)
}

func TestMetadataHandlerUnixFSTypesAndRemoteAddrRateLimit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		typeID int64
		want   string
	}{
		{"directory", unixfsdata.Data_Directory, "directory"},
		{"symlink", unixfsdata.Data_Symlink, "symlink"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, block := metadataUnixFSBlock(t, tc.typeID)
			res := metadataRequest(newMetadataHandler(metadataBlockGetterFunc(func(context.Context, cid.Cid) (blocks.Block, error) {
				return block, nil
			})), http.MethodGet, "?cid="+c.String(), "198.51.100.22:1234")
			body := requireFrozenMetadataSchema(t, res.Body.Bytes())
			require.Equal(t, tc.want, body.Root.UnixFS.Type)
		})
	}
	c := testProviderCID()
	handler := newMetadataHandler(nil)
	for i := 0; i < 2; i++ {
		require.Equal(t, http.StatusOK, metadataRequest(handler, http.MethodGet, "?cid="+c.String(), "198.51.100.23:1234").Code)
	}
	res := metadataRequest(handler, http.MethodGet, "?cid="+c.String(), "198.51.100.23:1234")
	require.Equal(t, http.StatusTooManyRequests, res.Code)
	require.Equal(t, "10", res.Header().Get("Retry-After"))
}

func TestInspectAndMetadataRoutesUseUIHostGate(t *testing.T) {
	var uiCalls, nativeCalls atomic.Int32
	ui := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uiCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	native := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nativeCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	handler := withUIHostGate(ui, native)
	for _, path := range []string{"/inspect", "/inspect/abc", "/retrieval", "/retrieval/abc", metadataAPIPath, dagAPIPath, ipnsAPIPath, carAPIPath} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		require.Equal(t, http.StatusOK, res.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/ipfs/"+testProviderCID().String(), nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	require.Equal(t, http.StatusOK, res.Code)
	require.Equal(t, int32(8), uiCalls.Load())
	require.Equal(t, int32(1), nativeCalls.Load())
}

func TestMetadataHandlerNilGetterAndCIDMismatchAreSafe(t *testing.T) {
	raw := []byte("root")
	c, block := metadataBlock(t, raw, uint64(multicodec.Raw))
	other := cid.NewCidV1(uint64(multicodec.DagPb), c.Hash())
	for _, tc := range []struct {
		name   string
		getter rootBlockGetter
		status string
		verify bool
	}{
		{"unsupported", nil, "unsupported_mode", false},
		{"mismatch", metadataBlockGetterFunc(func(context.Context, cid.Cid) (blocks.Block, error) {
			return blockWithCID(block, other), nil
		}), "integrity_error", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := newMetadataHandler(tc.getter)
			res := metadataRequest(handler, http.MethodGet, "?cid="+c.String(), "198.51.100.10:1234")
			require.Equal(t, http.StatusOK, res.Code)
			body := requireFrozenMetadataSchema(t, res.Body.Bytes())
			require.Equal(t, tc.status, body.Root.Status)
			require.Equal(t, tc.verify, body.Root.BlockVerified)
		})
	}
}

func TestMetadataHandlerRemoteCARSkipsGetter(t *testing.T) {
	var calls atomic.Int32
	getter := metadataBlockGetterFunc(func(context.Context, cid.Cid) (blocks.Block, error) {
		calls.Add(1)
		return nil, errors.New("getter must not run")
	})
	handler := newMetadataHandlerWithOptions(getter, metadataHandlerOptions{remoteCAR: true})
	c := testProviderCID()
	res := metadataRequest(handler, http.MethodGet, "?cid="+c.String(), "198.51.100.24:1234")
	require.Equal(t, http.StatusOK, res.Code)
	body := requireFrozenMetadataSchema(t, res.Body.Bytes())
	require.Equal(t, "unsupported_mode", body.Root.Status)
	require.Equal(t, int32(0), calls.Load())
	require.Equal(t, c.String(), body.ParsedCID.Canonical)
	require.Equal(t, "/inspect/"+c.String(), body.CanonicalLinks.Inspector)
}

func blockWithCID(block blocks.Block, c cid.Cid) blocks.Block {
	result, err := blocks.NewBlockWithCid(block.RawData(), c)
	if err != nil {
		return nil
	}
	return result
}

func TestMetadataHandlerMapsErrorsAndLimits(t *testing.T) {
	c := testProviderCID()
	getter := metadataBlockGetterFunc(func(ctx context.Context, _ cid.Cid) (blocks.Block, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	handler := newMetadataHandlerWithOptions(getter, metadataHandlerOptions{timeout: time.Nanosecond})
	res := metadataRequest(handler, http.MethodGet, "?cid="+c.String(), "198.51.100.10:1234")
	require.Equal(t, http.StatusOK, res.Code)
	body := requireFrozenMetadataSchema(t, res.Body.Bytes())
	require.Equal(t, "timeout", body.Root.Status)
	require.NotContains(t, res.Body.String(), "context deadline exceeded")

	busy := newMetadataHandler(getter)
	busy.slots <- struct{}{}
	busy.slots <- struct{}{}
	res = metadataRequest(busy, http.MethodGet, "?cid="+c.String(), "198.51.100.11:1234")
	require.Equal(t, http.StatusTooManyRequests, res.Code)
	require.Equal(t, "1", res.Header().Get("Retry-After"))
	require.Contains(t, res.Body.String(), `"code":"busy"`)
}

func TestMetadataHandlerRequestCancellation(t *testing.T) {
	c := testProviderCID()
	handler := newMetadataHandler(metadataBlockGetterFunc(func(ctx context.Context, _ cid.Cid) (blocks.Block, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, metadataAPIPath+"?cid="+c.String(), nil).WithContext(ctx)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	body := requireFrozenMetadataSchema(t, res.Body.Bytes())
	require.Equal(t, "cancelled", body.Root.Status)
}
