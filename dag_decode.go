package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/ipfs/go-cid"
	dagpb "github.com/ipld/go-codec-dagpb"
	"github.com/ipld/go-ipld-prime/codec/dagcbor"
	"github.com/ipld/go-ipld-prime/datamodel"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/ipld/go-ipld-prime/node/basicnode"
	"github.com/multiformats/go-multicodec"
	"google.golang.org/protobuf/encoding/protowire"
)

var (
	errDAGDecodeLimit = errors.New("dag decode limit")
	errDAGLinkLimit   = errors.New("dag link limit")
)

func preflightDAGPB(raw []byte) (string, bool) {
	linkCount := 0
	for len(raw) > 0 {
		num, typ, n := protowire.ConsumeTag(raw)
		if n < 0 {
			return "dagpb_malformed", false
		}
		raw = raw[n:]
		if num == 2 {
			if typ != protowire.BytesType {
				return "dagpb_malformed", false
			}
			link, consumed := protowire.ConsumeBytes(raw)
			if consumed < 0 {
				return "dagpb_malformed", false
			}
			raw = raw[consumed:]
			linkCount++
			if linkCount > 64 {
				return "dagpb_link_limit", true
			}
			if len(link) > 4096 {
				return "dagpb_link_bytes", true
			}
			if reason, limited := preflightDAGPBLink(link); reason != "" {
				return reason, limited
			}
			continue
		}
		consumed := protowire.ConsumeFieldValue(num, typ, raw)
		if consumed < 0 {
			return "dagpb_malformed", false
		}
		raw = raw[consumed:]
	}
	return "", false
}

func preflightDAGPBLink(raw []byte) (string, bool) {
	hashPresent := false
	for len(raw) > 0 {
		num, typ, n := protowire.ConsumeTag(raw)
		if n < 0 {
			return "dagpb_malformed", false
		}
		raw = raw[n:]
		switch num {
		case 1:
			if typ != protowire.BytesType {
				return "dagpb_invalid_link", false
			}
			value, consumed := protowire.ConsumeBytes(raw)
			if consumed < 0 {
				return "dagpb_malformed", false
			}
			raw = raw[consumed:]
			used, parsed, err := cid.CidFromBytes(value)
			if err != nil || !parsed.Defined() || used != len(value) {
				return "dagpb_invalid_link", false
			}
			hashPresent = true
		case 2:
			if typ != protowire.BytesType {
				return "dagpb_malformed", false
			}
			value, consumed := protowire.ConsumeBytes(raw)
			if consumed < 0 {
				return "dagpb_malformed", false
			}
			raw = raw[consumed:]
			if !utf8.Valid(value) {
				return "dagpb_malformed", false
			}
			if len(value) > 256 {
				return "dagpb_name_bytes", true
			}
		default:
			consumed := protowire.ConsumeFieldValue(num, typ, raw)
			if consumed < 0 {
				return "dagpb_malformed", false
			}
			raw = raw[consumed:]
		}
	}
	if !hashPresent {
		return "dagpb_invalid_link", false
	}
	return "", false
}

func addDAGLimitReason(observation *dagObservation, reason string) {
	observation.Truncated = true
	for _, existing := range observation.LimitsHit {
		if existing == reason {
			return
		}
	}
	observation.LimitsHit = append(observation.LimitsHit, reason)
}

func parseDAGLinks(c cid.Cid, raw []byte) (dagLinks, string, string) {
	links := dagLinks{Items: make([]dagLink, 0)}
	switch c.Type() {
	case uint64(multicodec.Raw):
		return links, "fetched", ""
	case uint64(multicodec.DagPb):
		if reason, limited := preflightDAGPB(raw); reason != "" {
			links.Truncated = limited
			if limited && reason == "dagpb_link_limit" {
				links.Total = 65
			} else if limited {
				links.Total = 1
			}
			if limited {
				return links, "decode_limit", reason
			}
			return links, "malformed", reason
		}
		assembler := dagpb.Type.PBNode.NewBuilder()
		if err := dagpb.DecodeBytes(assembler, raw); err != nil {
			return links, "malformed", "dagpb_malformed"
		}
		node := assembler.Build()
		listNode, err := node.LookupByString("Links")
		if err != nil {
			return links, "malformed", "dagpb_malformed"
		}
		it := listNode.ListIterator()
		for !it.Done() {
			_, value, err := it.Next()
			if err != nil {
				return links, "malformed", "dagpb_malformed"
			}
			links.Total++
			if links.Shown >= dagLinkLimit {
				links.Truncated = true
				return links, "fetched", "link_limit"
			}
			hash, err := value.LookupByString("Hash")
			if err != nil {
				return links, "malformed", "dagpb_malformed"
			}
			link, err := hash.AsLink()
			if err != nil {
				return links, "malformed", "dagpb_malformed"
			}
			clink, ok := link.(cidlink.Link)
			if !ok {
				return links, "malformed", "dagpb_invalid_link"
			}
			item := dagLink{CID: clink.Cid.String()}
			if name, err := value.LookupByString("Name"); err == nil && !name.IsAbsent() {
				if text, err := name.AsString(); err == nil && len([]byte(text)) <= 256 {
					item.Name = text
				}
			}
			links.Items = append(links.Items, item)
			links.Shown++
		}
	case uint64(multicodec.DagCbor):
		assembler := basicnode.Prototype.Any.NewBuilder()
		options := dagcbor.DecodeOptions{AllowLinks: true, AllocationBudget: 2 * dagBlockLimit, MaxCollectionPrealloc: dagLinkLimit, MaxDepth: dagNestingLimit}
		if err := options.Decode(assembler, bytes.NewReader(raw)); err != nil {
			if errors.Is(err, dagcbor.ErrAllocationBudgetExceeded) || errors.Is(err, dagcbor.ErrDecodeDepthExceeded) {
				return links, "decode_limit", "decode_limit"
			}
			return links, "malformed", "dagcbor_malformed"
		}
		reason := ""
		if err := collectDAGCBORLinks(assembler.Build(), 0, &links, new(int), &reason); err != nil {
			if errors.Is(err, errDAGDecodeLimit) {
				return links, "decode_limit", reason
			}
			if errors.Is(err, errDAGLinkLimit) {
				return links, "fetched", reason
			}
			return links, "malformed", "dagcbor_malformed"
		}
	default:
		return links, "unsupported_codec", ""
	}
	links.Truncated = links.Total > links.Shown
	return links, "fetched", ""
}

func collectDAGCBORLinks(node datamodel.Node, depth int, links *dagLinks, visits *int, reason *string) error {
	if depth > dagNestingLimit || *visits >= dagVisitLimit {
		*reason = "decode_limit"
		return errDAGDecodeLimit
	}
	(*visits)++
	if node.Kind() == datamodel.Kind_Link {
		link, err := node.AsLink()
		if err != nil {
			return err
		}
		clink, ok := link.(cidlink.Link)
		if !ok {
			return errors.New("non-cid link")
		}
		links.Total++
		if links.Shown >= dagLinkLimit {
			links.Truncated = true
			*reason = "link_limit"
			return errDAGLinkLimit
		}
		links.Items = append(links.Items, dagLink{CID: clink.Cid.String()})
		links.Shown++
		return nil
	}
	switch node.Kind() {
	case datamodel.Kind_Map:
		it := node.MapIterator()
		for !it.Done() {
			_, value, err := it.Next()
			if err != nil {
				return err
			}
			if err := collectDAGCBORLinks(value, depth+1, links, visits, reason); err != nil {
				return err
			}
		}
	case datamodel.Kind_List:
		it := node.ListIterator()
		for !it.Done() {
			_, value, err := it.Next()
			if err != nil {
				return err
			}
			if err := collectDAGCBORLinks(value, depth+1, links, visits, reason); err != nil {
				return err
			}
		}
	}
	links.Truncated = links.Total > links.Shown
	return nil
}

func marshalDAG(body *dagResponse) ([]byte, error) {
	for {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		if len(data) <= dagJSONLimit {
			return data, nil
		}
		if len(body.Nodes) <= 1 {
			return nil, errors.New("dag JSON limit")
		}
		body.Nodes = body.Nodes[:len(body.Nodes)-1]
		body.Observation.Truncated = true
		addDAGLimitReason(&body.Observation, "json_limit")
	}
}
