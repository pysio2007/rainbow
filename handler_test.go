package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	bsnet "github.com/ipfs/boxo/bitswap/network/bsnet"
	bsserver "github.com/ipfs/boxo/bitswap/server"
	boxopath "github.com/ipfs/boxo/path"
	boxoresolver "github.com/ipfs/boxo/path/resolver"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-metrics-interface"
	unixfsdata "github.com/ipfs/go-unixfsnode/data"
	unixfsbuilder "github.com/ipfs/go-unixfsnode/data/builder"
	"github.com/ipfs/go-unixfsnode/directory"
	dagpb "github.com/ipld/go-codec-dagpb"
	"github.com/ipld/go-ipld-prime"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrustless(t *testing.T) {
	t.Parallel()

	ts, gnd := mustTestServer(t, Config{
		Bitswap:                 true,
		TrustlessGatewayDomains: []string{"trustless.com"},
		disableMetrics:          true,
	})

	content := "hello world"
	cid := mustAddFile(t, gnd, []byte(content))
	url := ts.URL + "/ipfs/" + cid.String()

	t.Run("Non-trustless request returns 406", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		require.NoError(t, err)
		req.Host = "trustless.com"

		res, err := http.DefaultClient.Do(req)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusNotAcceptable, res.StatusCode)
	})

	t.Run("Trustless request with query parameter returns 200", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, url+"?format=raw", nil)
		require.NoError(t, err)
		req.Host = "trustless.com"

		res, err := http.DefaultClient.Do(req)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, res.StatusCode)
	})

	t.Run("Trustless request with accept header returns 200", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		require.NoError(t, err)
		req.Host = "trustless.com"
		req.Header.Set("Accept", "application/vnd.ipld.raw")

		res, err := http.DefaultClient.Do(req)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, res.StatusCode)
	})
}

func TestNoBlockcacheHeader(t *testing.T) {
	const authToken = "authorized"
	const authHeader = "Authorization"
	ts, gnd := mustTestServer(t, Config{
		Bitswap:          true,
		TracingAuthToken: authToken,
		disableMetrics:   true,
	})

	content := make([]byte, 1024)
	_, err := rand.Read(content)
	require.NoError(t, err)
	cid := mustAddFile(t, gnd, content)
	url := ts.URL + "/ipfs/" + cid.String()

	t.Run("Successful download of data with standard already cached in the node", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		require.NoError(t, err)

		res, err := http.DefaultClient.Do(req)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, res.StatusCode)
		responseBody, err := io.ReadAll(res.Body)
		assert.NoError(t, err)
		assert.Equal(t, content, responseBody)
	})

	t.Run("When caching is explicitly skipped the data is not found", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		require.NoError(t, err)

		// Both headers present, expect NoBlockcacheHeader to work
		req.Header.Set(NoBlockcacheHeader, "true")
		req.Header.Set(authHeader, authToken)

		_, err = http.DefaultClient.Do(req)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("When caching is explicitly skipped the data is found if a peer has it", func(t *testing.T) {
		newHost, err := libp2p.New()
		require.NoError(t, err)

		ctx := context.Background()
		// pacify metrics reporting code
		ctx = metrics.CtxScope(ctx, "test.bsserver.host")
		n := bsnet.NewFromIpfsHost(newHost)
		bs := bsserver.New(ctx, n, gnd.blockstore)
		n.Start(bs)
		defer bs.Close()

		require.NoError(t, newHost.Connect(context.Background(), peer.AddrInfo{
			ID:    gnd.host.ID(),
			Addrs: gnd.host.Addrs(),
		}))

		ctx, cancel := context.WithTimeout(ctx, time.Second*1)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		require.NoError(t, err)

		// Both headers present, expect NoBlockcacheHeader to work
		req.Header.Set(NoBlockcacheHeader, "true")
		req.Header.Set(authHeader, authToken)

		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, res.StatusCode)
		responseBody, err := io.ReadAll(res.Body)
		assert.NoError(t, err)
		assert.Equal(t, content, responseBody)
	})

	t.Run("Skipping the cache only works when 'true' is passed", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		require.NoError(t, err)

		// Both headers present, but NoBlockcacheHeader is not 'true'
		req.Header.Set(NoBlockcacheHeader, "1")
		req.Header.Set(authHeader, authToken)

		res, err := http.DefaultClient.Do(req)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, res.StatusCode)
		responseBody, err := io.ReadAll(res.Body)
		assert.NoError(t, err)
		assert.Equal(t, content, responseBody)
	})

	t.Run("Skipping the cache only works when the Authorization header matches", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		require.NoError(t, err)

		// Authorization missing, expect NoBlockcacheHeader to result in an error
		req.Header.Set(NoBlockcacheHeader, "true")

		res, err := http.DefaultClient.Do(req)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, res.StatusCode)
	})

	t.Run("Skipping the cache only works when RAINBOW_TRACING_AUTH is set", func(t *testing.T) {
		// Set up separate server without authToken set
		ts2, gnd := mustTestServer(t, Config{
			Bitswap:          true,
			TracingAuthToken: "", // simulate RAINBOW_TRACING_AUTH being not set
			disableMetrics:   true,
		})
		content := make([]byte, 1024)
		_, err := rand.Read(content)
		require.NoError(t, err)
		cid2 := mustAddFile(t, gnd, content)
		url := ts2.URL + "/ipfs/" + cid2.String()

		req, err := http.NewRequest(http.MethodGet, url, nil)
		require.NoError(t, err)

		// Authorization missing, expect NoBlockcacheHeader to result in an error
		req.Header.Set(NoBlockcacheHeader, "true")

		res, err := http.DefaultClient.Do(req)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, res.StatusCode)
	})
}

func TestWebUIHandlerRestrictsFilesAndFallback(t *testing.T) {
	handler := webUIHandler(nil)
	for _, tc := range []struct {
		name   string
		method string
		path   string
		status int
	}{
		{"root", http.MethodGet, "/", http.StatusOK},
		{"explore fallback", http.MethodGet, "/explore/a/b", http.StatusOK},
		{"index", http.MethodGet, "/index.html", http.StatusOK},
		{"asset directory", http.MethodGet, "/assets/", http.StatusNotFound},
		{"missing asset", http.MethodGet, "/assets/missing.js", http.StatusNotFound},
		{"encoded traversal", http.MethodGet, "/explore/../assets/missing.js", http.StatusNotFound},
		{"post", http.MethodPost, "/", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "http://127.0.0.1:8090"+tc.path, nil)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tc.status {
				t.Fatalf("status = %d, want %d", res.Code, tc.status)
			}
		})
	}
	getReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/index.html", nil)
	getRes := httptest.NewRecorder()
	handler.ServeHTTP(getRes, getReq)
	headReq := httptest.NewRequest(http.MethodHead, "http://127.0.0.1:8090/index.html", nil)
	headRes := httptest.NewRecorder()
	handler.ServeHTTP(headRes, headReq)
	if getRes.Code != headRes.Code || getRes.Header().Get("Content-Type") != headRes.Header().Get("Content-Type") || getRes.Header().Get("Content-Length") != headRes.Header().Get("Content-Length") || getRes.Header().Get("Last-Modified") != headRes.Header().Get("Last-Modified") || headRes.Body.Len() != 0 {
		t.Fatalf("GET/HEAD mismatch: GET %d %#v, HEAD %d %#v body=%d", getRes.Code, getRes.Header(), headRes.Code, headRes.Header(), headRes.Body.Len())
	}
}

func TestUIHostRoutingAndDirectoryMethod(t *testing.T) {
	gate := withUIHostGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	for _, host := range []string{"localhost:8090", "example.com", "127.0.0.1:8090"} {
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/explore", nil)
		res := httptest.NewRecorder()
		gate.ServeHTTP(res, req)
		if res.Code != http.StatusNoContent {
			t.Fatalf("host %q status = %d, want %d", host, res.Code, http.StatusNoContent)
		}
	}

	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, directoryAPIPath, strings.NewReader(""))
	directoryHandler(Config{}, &Node{}).ServeHTTP(res, req)
	if res.Code != http.StatusMethodNotAllowed || res.Header().Get("Allow") != "GET, HEAD" || res.Body.String() != `{"error":{"code":"method_not_allowed"}}` {
		t.Fatalf("method response = status %d allow %q body %q", res.Code, res.Header().Get("Allow"), res.Body.String())
	}
}

func TestSetupGatewayHandlerPortalHostAndNativeRoutes(t *testing.T) {
	ts, gnd := mustTestServer(t, Config{Bitswap: true, TrustlessGatewayDomains: []string{"native.example"}, disableMetrics: true})

	request := func(method, target, host string) *http.Response {
		req := httptest.NewRequest(method, ts.URL+target, nil)
		req.Host = host
		res := httptest.NewRecorder()
		ts.Config.Handler.ServeHTTP(res, req)
		return res.Result()
	}

	portal := request(http.MethodGet, "/", "127.0.0.1:8090")
	if portal.StatusCode != http.StatusOK || !strings.Contains(portal.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("literal portal response = %d content-type %q", portal.StatusCode, portal.Header.Get("Content-Type"))
	}
	for _, host := range []string{"localhost:8090", "example.com"} {
		for _, target := range []string{"/", "/index.html"} {
			res := request(http.MethodGet, target, host)
			body, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatalf("read hosted UI path %s response: %v", target, err)
			}
			if res.StatusCode != http.StatusOK {
				t.Fatalf("hosted UI path %s on %s status = %d, want %d", target, host, res.StatusCode, http.StatusOK)
			}
			if !strings.Contains(res.Header.Get("Content-Type"), "text/html") || !strings.Contains(string(body), "<!doctype html>") {
				t.Fatalf("hosted UI path %s on %s is not identifiable WebUI content: content-type %q body %q", target, host, res.Header.Get("Content-Type"), string(body))
			}
		}
	}
	version := request(http.MethodGet, "/version", "native.example")
	if version.StatusCode != http.StatusOK {
		t.Fatalf("native /version status = %d", version.StatusCode)
	}

	contentCID := mustAddFile(t, gnd, []byte("native route"))
	native := request(http.MethodGet, "/ipfs/"+contentCID.String()+"?format=raw", "native.example")
	if native.StatusCode != http.StatusOK {
		t.Fatalf("native /ipfs route status = %d", native.StatusCode)
	}
}

func TestDirectoryQueryAndHEADSemantics(t *testing.T) {
	handler := directoryHandler(Config{}, &Node{})
	valid := "/ipfs/bafybeihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku"
	encoded := url.QueryEscape(valid)
	for _, tc := range []struct {
		name      string
		method    string
		query     string
		status    int
		body      string
		contentLT string
	}{
		{"missing path", http.MethodGet, "", http.StatusBadRequest, `{"error":{"code":"invalid_path"}}`, ""},
		{"unencoded path", http.MethodGet, "path=" + valid, http.StatusBadRequest, `{"error":{"code":"invalid_path"}}`, ""},
		{"double encoded", http.MethodGet, "path=" + url.QueryEscape(encoded), http.StatusBadRequest, `{"error":{"code":"invalid_path"}}`, ""},
		{"duplicate path", http.MethodGet, "path=" + encoded + "&path=" + encoded, http.StatusBadRequest, `{"error":{"code":"invalid_path"}}`, ""},
		{"nil resolver", http.MethodGet, "path=" + encoded, http.StatusNotImplemented, `{"error":{"code":"unsupported_mode"}}`, ""},
		{"nil resolver HEAD", http.MethodHead, "path=" + encoded, http.StatusNotImplemented, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, directoryAPIPath+querySuffix(tc.query), nil)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tc.status || res.Body.String() != tc.body {
				t.Fatalf("response = status %d body %q, want status %d body %q", res.Code, res.Body.String(), tc.status, tc.body)
			}
			if res.Header().Get("Content-Type") != "application/json; charset=utf-8" || res.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("headers = %#v", res.Header())
			}
			if tc.method == http.MethodHead && res.Body.Len() != 0 {
				t.Fatalf("HEAD body length = %d", res.Body.Len())
			}
		})
	}
}

func querySuffix(query string) string {
	if query == "" {
		return ""
	}
	return "?" + query
}

func TestDirectoryCARModeIsUnsupported(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		req := httptest.NewRequest(method, directoryAPIPath, nil)
		res := httptest.NewRecorder()
		directoryHandler(Config{RemoteBackendMode: RemoteBackendCAR}, &Node{}).ServeHTTP(res, req)
		if res.Code != http.StatusNotImplemented || res.Header().Get("Cache-Control") != "no-store" || res.Body.Len() != 0 && method == http.MethodHead {
			t.Fatalf("%s response = status %d headers %#v body %q", method, res.Code, res.Header(), res.Body.String())
		}
	}
}

type staticDirectoryResolver struct {
	node ipld.Node
	link ipld.Link
}

func (r staticDirectoryResolver) ResolvePath(context.Context, boxopath.ImmutablePath) (ipld.Node, ipld.Link, error) {
	return r.node, r.link, nil
}

func (r staticDirectoryResolver) ResolveToLastNode(context.Context, boxopath.ImmutablePath) (cid.Cid, []string, error) {
	return r.link.(cidlink.Link).Cid, nil, nil
}

func (r staticDirectoryResolver) ResolvePathComponents(context.Context, boxopath.ImmutablePath) ([]ipld.Node, error) {
	return []ipld.Node{r.node}, nil
}

var _ boxoresolver.Resolver = staticDirectoryResolver{}

func testBasicDirectory(t *testing.T) (ipld.Node, ipld.Link) {
	t.Helper()
	root, err := cid.Decode("bafybeihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku")
	require.NoError(t, err)
	child := root
	dataNode, err := unixfsbuilder.BuildUnixFS(func(b *unixfsbuilder.Builder) {
		unixfsbuilder.DataType(b, unixfsdata.Data_Directory)
	})
	require.NoError(t, err)
	links := make([]dagpb.PBLink, 0, 2)
	for _, name := range []string{"zeta", "alpha"} {
		link, err := unixfsbuilder.BuildUnixFSDirectoryEntry(name, 0, cidlink.Link{Cid: child})
		require.NoError(t, err)
		links = append(links, link)
	}
	pbb := dagpb.Type.PBNode.NewBuilder()
	pbm, err := pbb.BeginMap(2)
	require.NoError(t, err)
	require.NoError(t, pbm.AssembleKey().AssignString("Data"))
	require.NoError(t, pbm.AssembleValue().AssignBytes(unixfsdata.EncodeUnixFSData(dataNode)))
	require.NoError(t, pbm.AssembleKey().AssignString("Links"))
	list, err := pbm.AssembleValue().BeginList(int64(len(links)))
	require.NoError(t, err)
	for _, link := range links {
		require.NoError(t, list.AssembleValue().AssignNode(link))
	}
	require.NoError(t, list.Finish())
	require.NoError(t, pbm.Finish())
	node := pbb.Build().(dagpb.PBNode)
	directoryNode, err := directory.NewUnixFSBasicDir(context.Background(), node, dataNode, nil)
	require.NoError(t, err)
	return directoryNode, cidlink.Link{Cid: root}
}

func TestDirectoryBasicDirGetHeadSortAndETag(t *testing.T) {
	node, link := testBasicDirectory(t)
	handler := directoryHandler(Config{}, &Node{resolver: staticDirectoryResolver{node: node, link: link}})
	query := "?path=" + url.QueryEscape("/ipfs/"+link.(cidlink.Link).Cid.String())
	getReq := httptest.NewRequest(http.MethodGet, directoryAPIPath+query, nil)
	getRes := httptest.NewRecorder()
	handler.ServeHTTP(getRes, getReq)
	require.Equal(t, http.StatusOK, getRes.Code)
	var body directoryResponse
	require.NoError(t, json.Unmarshal(getRes.Body.Bytes(), &body))
	require.Equal(t, []string{"alpha", "zeta"}, []string{body.Entries[0].Name, body.Entries[1].Name})
	require.Equal(t, link.(cidlink.Link).Cid.String(), body.ResolvedCID)

	headReq := httptest.NewRequest(http.MethodHead, directoryAPIPath+query, nil)
	headRes := httptest.NewRecorder()
	handler.ServeHTTP(headRes, headReq)
	require.Equal(t, getRes.Code, headRes.Code)
	require.Equal(t, getRes.Header().Get("Content-Type"), headRes.Header().Get("Content-Type"))
	require.Equal(t, getRes.Header().Get("Content-Length"), headRes.Header().Get("Content-Length"))
	require.Equal(t, getRes.Header().Get("Cache-Control"), headRes.Header().Get("Cache-Control"))
	require.Equal(t, getRes.Header().Get("ETag"), headRes.Header().Get("ETag"))
	require.Empty(t, headRes.Body.Bytes())
}
