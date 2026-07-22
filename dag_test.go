package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
	unixfsbuilder "github.com/ipfs/go-unixfsnode/data/builder"
	dagpb "github.com/ipld/go-codec-dagpb"
	"github.com/ipld/go-ipld-prime/codec/dagcbor"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/ipld/go-ipld-prime/node/basicnode"
	"github.com/multiformats/go-multicodec"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"
)

type dagFixtureGetter struct {
	blocks map[string]blocks.Block
	errors map[string]error
	calls  atomic.Int32
}

func (g *dagFixtureGetter) GetBlock(ctx context.Context, c cid.Cid) (blocks.Block, error) {
	g.calls.Add(1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := g.errors[c.KeyString()]; err != nil {
		return nil, err
	}
	block := g.blocks[c.KeyString()]
	if block == nil {
		return nil, errors.New("not found")
	}
	return block, nil
}

func dagPBLinksBlock(t *testing.T, links []cid.Cid) (cid.Cid, blocks.Block) {
	t.Helper()
	pbb := dagpb.Type.PBNode.NewBuilder()
	pbm, err := pbb.BeginMap(1)
	require.NoError(t, err)
	require.NoError(t, pbm.AssembleKey().AssignString("Links"))
	list, err := pbm.AssembleValue().BeginList(int64(len(links)))
	require.NoError(t, err)
	for i, linkCID := range links {
		name := "link-" + string(rune('a'+i))
		link, err := unixfsbuilder.BuildUnixFSDirectoryEntry(name, 0, cidlink.Link{Cid: linkCID})
		require.NoError(t, err)
		require.NoError(t, list.AssembleValue().AssignNode(link))
	}
	require.NoError(t, list.Finish())
	require.NoError(t, pbm.Finish())
	var raw bytes.Buffer
	require.NoError(t, dagpb.Encode(pbb.Build(), &raw))
	return metadataBlock(t, raw.Bytes(), uint64(multicodec.DagPb))
}

func dagRequest(handler http.Handler, method, query, remote string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, dagAPIPath+query, nil)
	req.RemoteAddr = remote
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func TestDAGHandlerBFSDepthDedupAndCycle(t *testing.T) {
	leaf, leafBlock := metadataBlock(t, []byte("leaf"), uint64(multicodec.Raw))
	b, bBlock := dagPBLinksBlock(t, []cid.Cid{leaf})
	a, aBlock := dagPBLinksBlock(t, []cid.Cid{b, leaf})
	root, rootBlock := dagPBLinksBlock(t, []cid.Cid{a, b, leaf})
	getter := &dagFixtureGetter{blocks: map[string]blocks.Block{
		root.KeyString(): rootBlock, a.KeyString(): aBlock, b.KeyString(): bBlock, leaf.KeyString(): leafBlock,
	}}
	res := dagRequest(newDAGHandler(getter, false), http.MethodGet, "?cid="+root.String()+"&depth=2", "198.51.100.40:1234")
	require.Equal(t, http.StatusOK, res.Code)
	var body dagResponse
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
	require.Equal(t, 1, body.Version)
	require.Equal(t, root.String(), body.Root)
	require.Equal(t, 2, body.RequestedDepth)
	require.Equal(t, "fetched", body.Observation.Status)
	require.False(t, body.Observation.Truncated)
	require.Empty(t, body.Observation.LimitsHit)
	var wire struct {
		Observation struct {
			LimitsHit []string `json:"limitsHit"`
		} `json:"observation"`
	}
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &wire))
	require.NotNil(t, wire.Observation.LimitsHit)
	require.Empty(t, wire.Observation.LimitsHit)
	require.Len(t, body.Nodes, 4)
	require.Equal(t, int32(4), getter.calls.Load())
	seen := make(map[string]struct{})
	for _, node := range body.Nodes {
		require.NotContains(t, seen, node.CID)
		seen[node.CID] = struct{}{}
	}
}

func TestDAGHandlerCapsMalformedMismatchCancelAndRemoteCAR(t *testing.T) {
	c, block := metadataBlock(t, []byte("raw"), uint64(multicodec.Raw))
	getter := &dagFixtureGetter{blocks: map[string]blocks.Block{c.KeyString(): block}}
	handler := newDAGHandlerWithOptions(metadataBlockGetterFunc(func(ctx context.Context, _ cid.Cid) (blocks.Block, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}), dagHandlerOptions{timeout: time.Nanosecond})
	res := dagRequest(handler, http.MethodGet, "?cid="+c.String()+"&depth=1", "198.51.100.41:1234")
	var body dagResponse
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
	require.Equal(t, "timeout", body.Observation.Status)

	bad := metadataRawBlock{raw: []byte("bad"), cid: c}
	getter = &dagFixtureGetter{blocks: map[string]blocks.Block{c.KeyString(): bad}}
	res = dagRequest(newDAGHandler(getter, false), http.MethodGet, "?cid="+c.String()+"&depth=1", "198.51.100.42:1234")
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
	require.Equal(t, "integrity_error", body.Nodes[0].Status)

	remoteCalls := atomic.Int32{}
	remote := metadataBlockGetterFunc(func(context.Context, cid.Cid) (blocks.Block, error) {
		remoteCalls.Add(1)
		return block, nil
	})
	res = dagRequest(newDAGHandler(remote, true), http.MethodGet, "?cid="+c.String()+"&depth=1", "198.51.100.43:1234")
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
	require.Equal(t, "unsupported_mode", body.Observation.Status)
	require.Zero(t, remoteCalls.Load())
}

func TestDAGHandlerLinkAndNodeLimits(t *testing.T) {
	childBlocks := make([]cid.Cid, 5)
	fixture := &dagFixtureGetter{blocks: make(map[string]blocks.Block)}
	for i := range childBlocks {
		child, block := metadataBlock(t, []byte{byte(i)}, uint64(multicodec.Raw))
		childBlocks[i] = child
		fixture.blocks[child.KeyString()] = block
	}
	root, rootBlock := dagPBLinksBlock(t, childBlocks)
	fixture.blocks[root.KeyString()] = rootBlock
	res := dagRequest(newDAGHandler(fixture, false), http.MethodGet, "?cid="+root.String()+"&depth=1", "198.51.100.44:1234")
	var body dagResponse
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
	require.Equal(t, 4, body.Nodes[0].Links.Shown)
	require.True(t, body.Nodes[0].Links.Truncated)
	require.Equal(t, []string{"link_limit"}, body.Observation.LimitsHit)
	var wire struct {
		Observation struct {
			LimitsHit []string `json:"limitsHit"`
		} `json:"observation"`
	}
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &wire))
	require.NotNil(t, wire.Observation.LimitsHit)
	require.Equal(t, []string{"link_limit"}, wire.Observation.LimitsHit)
}

func TestDAGHandlerLimitsHitIsArrayForNonSuccessStatuses(t *testing.T) {
	c := testProviderCID()
	tests := []struct {
		name    string
		handler http.Handler
		status  string
	}{
		{
			name:    "unsupported",
			handler: newDAGHandler(nil, true),
			status:  "unsupported_mode",
		},
		{
			name: "not-found",
			handler: newDAGHandler(metadataBlockGetterFunc(func(context.Context, cid.Cid) (blocks.Block, error) {
				return nil, errors.New("not found")
			}), false),
			status: "not_found",
		},
		{
			name: "timeout",
			handler: newDAGHandlerWithOptions(metadataBlockGetterFunc(func(ctx context.Context, _ cid.Cid) (blocks.Block, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			}), dagHandlerOptions{timeout: time.Nanosecond}),
			status: "timeout",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := dagRequest(tc.handler, http.MethodGet, "?cid="+c.String()+"&depth=1", "198.51.100.75:1234")
			var wire struct {
				Observation struct {
					Status    string   `json:"status"`
					LimitsHit []string `json:"limitsHit"`
				} `json:"observation"`
			}
			require.NoError(t, json.Unmarshal(res.Body.Bytes(), &wire))
			require.Equal(t, tc.status, wire.Observation.Status)
			require.NotNil(t, wire.Observation.LimitsHit)
			require.Empty(t, wire.Observation.LimitsHit)
		})
	}
}

func TestDAGHandlerDAGPBPreflightStopsMaliciousLinks(t *testing.T) {
	child, _ := metadataBlock(t, []byte("child"), uint64(multicodec.Raw))
	many := make([]cid.Cid, 65)
	for i := range many {
		many[i] = child
	}
	tooManyCID, tooManyBlock := dagPBLinksBlock(t, many)
	largeNameLink := protowire.AppendTag(nil, 1, protowire.BytesType)
	largeNameLink = protowire.AppendBytes(largeNameLink, child.Bytes())
	largeNameLink = protowire.AppendTag(largeNameLink, 2, protowire.BytesType)
	largeNameLink = protowire.AppendBytes(largeNameLink, bytes.Repeat([]byte{'n'}, 257))
	largeNameNode := protowire.AppendTag(nil, 2, protowire.BytesType)
	largeNameNode = protowire.AppendBytes(largeNameNode, largeNameLink)
	largeNameCID, largeNameBlock := metadataBlock(t, largeNameNode, uint64(multicodec.DagPb))
	largeLink := protowire.AppendTag(nil, 1, protowire.BytesType)
	largeLink = protowire.AppendBytes(largeLink, child.Bytes())
	largeLink = protowire.AppendTag(largeLink, 4, protowire.BytesType)
	largeLink = protowire.AppendBytes(largeLink, bytes.Repeat([]byte{'x'}, 4090))
	largeLinkNode := protowire.AppendTag(nil, 2, protowire.BytesType)
	largeLinkNode = protowire.AppendBytes(largeLinkNode, largeLink)
	largeLinkCID, largeLinkBlock := metadataBlock(t, largeLinkNode, uint64(multicodec.DagPb))
	for _, tc := range []struct {
		name   string
		cid    cid.Cid
		block  blocks.Block
		reason string
	}{
		{"link-count", tooManyCID, tooManyBlock, "dagpb_link_limit"},
		{"name-bytes", largeNameCID, largeNameBlock, "dagpb_name_bytes"},
		{"link-bytes", largeLinkCID, largeLinkBlock, "dagpb_link_bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := dagRequest(newDAGHandler(metadataBlockGetterFunc(func(context.Context, cid.Cid) (blocks.Block, error) {
				return tc.block, nil
			}), false), http.MethodGet, "?cid="+tc.cid.String()+"&depth=1", "198.51.100.47:1234")
			var body dagResponse
			require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
			require.Equal(t, "decode_limit", body.Nodes[0].Status)
			require.Contains(t, body.Observation.LimitsHit, tc.reason)
		})
	}
}

func TestDAGHandlerDAGCBORDecoderAndLinkLimits(t *testing.T) {
	child, _ := metadataBlock(t, []byte("child"), uint64(multicodec.Raw))
	builder := basicnode.Prototype.Any.NewBuilder()
	mapAssembler, err := builder.BeginMap(1)
	require.NoError(t, err)
	require.NoError(t, mapAssembler.AssembleKey().AssignString("links"))
	list, err := mapAssembler.AssembleValue().BeginList(5)
	require.NoError(t, err)
	for range 5 {
		require.NoError(t, list.AssembleValue().AssignLink(cidlink.Link{Cid: child}))
	}
	require.NoError(t, list.Finish())
	require.NoError(t, mapAssembler.Finish())
	var raw bytes.Buffer
	require.NoError(t, dagcbor.Encode(builder.Build(), &raw))
	c, block := metadataBlock(t, raw.Bytes(), uint64(multicodec.DagCbor))
	res := dagRequest(newDAGHandler(metadataBlockGetterFunc(func(context.Context, cid.Cid) (blocks.Block, error) {
		return block, nil
	}), false), http.MethodGet, "?cid="+c.String()+"&depth=1", "198.51.100.48:1234")
	var body dagResponse
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
	require.Equal(t, "fetched", body.Nodes[0].Status)
	require.True(t, body.Nodes[0].Links.Truncated)
	require.Contains(t, body.Observation.LimitsHit, "link_limit")

	deep := append([]byte{}, bytes.Repeat([]byte{0x81}, dagNestingLimit+1)...)
	deep = append(deep, 0)
	deepCID, deepBlock := metadataBlock(t, deep, uint64(multicodec.DagCbor))
	res = dagRequest(newDAGHandler(metadataBlockGetterFunc(func(context.Context, cid.Cid) (blocks.Block, error) {
		return deepBlock, nil
	}), false), http.MethodGet, "?cid="+deepCID.String()+"&depth=1", "198.51.100.49:1234")
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
	require.Equal(t, "decode_limit", body.Nodes[0].Status)
	require.Contains(t, body.Observation.LimitsHit, "decode_limit")
}

func TestDAGLimitReasonsAlwaysMarkTruncated(t *testing.T) {
	for _, reason := range []string{"link_limit", "node_limit", "block_limit", "parsed_bytes_limit", "decode_limit", "json_limit"} {
		observation := dagObservation{}
		addDAGLimitReason(&observation, reason)
		require.True(t, observation.Truncated, reason)
		require.Equal(t, []string{reason}, observation.LimitsHit)
	}
}

func TestDAGHandlerAdmissionAndCancellation(t *testing.T) {
	c := testProviderCID()
	started := make(chan struct{})
	getter := metadataBlockGetterFunc(func(ctx context.Context, _ cid.Cid) (blocks.Block, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	handler := newDAGHandler(getter, false)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = dagRequest(handler, http.MethodGet, "?cid="+c.String()+"&depth=1", "198.51.100.45:1234")
		}()
	}
	<-started
	res := dagRequest(handler, http.MethodGet, "?cid="+c.String()+"&depth=1", "198.51.100.46:1234")
	require.Equal(t, http.StatusTooManyRequests, res.Code)
	wg.Wait()
}
