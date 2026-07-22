package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ipfs/boxo/blockstore"
	boxopath "github.com/ipfs/boxo/path"
	leveldb "github.com/ipfs/go-ds-leveldb"
	"github.com/ipfs/go-log/v2"
	"github.com/ipfs/go-unixfsnode/directory"
	"github.com/ipfs/go-unixfsnode/hamt"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"

	_ "net/http/pprof"

	"github.com/felixge/httpsnoop"
	"github.com/ipfs/boxo/gateway"
	boxoresolver "github.com/ipfs/boxo/path/resolver"
	servertiming "github.com/mitchellh/go-server-timing"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

//go:embed all:webui/dist
var webuiFS embed.FS

const directoryAPIPath = "/_rainbow/api/v1/directory"

const statsAPIPath = "/_rainbow/api/v1/stats"

var directorySemaphore = make(chan struct{}, 32)

const maxDirectoryJSONSize = 2 << 20

var errDirectoryJSONTooLarge = errors.New("directory JSON response too large")

type directoryResponse struct {
	Version     int              `json:"version"`
	Path        string           `json:"path"`
	ResolvedCID string           `json:"resolvedCid"`
	Entries     []directoryEntry `json:"entries"`
}

type directoryEntry struct {
	Name string `json:"name"`
	CID  string `json:"cid"`
}

type directoryError struct {
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

func writeDirectoryError(w http.ResponseWriter, r *http.Request, status int, code string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	var body directoryError
	body.Error.Code = code
	data, _ := json.Marshal(body)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

func directoryHandler(cfg Config, nd *Node) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeDirectoryError(w, r, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		if cfg.RemoteBackendMode == RemoteBackendCAR {
			writeDirectoryError(w, r, http.StatusNotImplemented, "unsupported_mode")
			return
		}
		select {
		case directorySemaphore <- struct{}{}:
			defer func() { <-directorySemaphore }()
		default:
			w.Header().Set("Retry-After", "1")
			writeDirectoryError(w, r, http.StatusTooManyRequests, "busy")
			return
		}

		rctx, cancel := context.WithTimeout(r.Context(), directoryTimeout(cfg.RetrievalTimeout))
		defer cancel()
		rawPath, ok := canonicalDirectoryQuery(r.URL.RawQuery)
		if !ok {
			writeDirectoryError(w, r, http.StatusBadRequest, "invalid_path")
			return
		}
		body, status, code := enumerateDirectory(rctx, nd, rawPath)
		if code != "" {
			writeDirectoryError(w, r, status, code)
			return
		}
		data, err := marshalDirectoryJSON(body)
		if errors.Is(err, errDirectoryJSONTooLarge) {
			writeDirectoryError(w, r, http.StatusRequestEntityTooLarge, "directory_too_large")
			return
		}
		if err != nil {
			writeDirectoryError(w, r, http.StatusInternalServerError, "internal")
			return
		}
		etagInput := []byte(fmt.Sprintf("v1\x00%s\x00%s", body.Path, body.ResolvedCID))
		hash := sha256.Sum256(etagInput)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("ETag", fmt.Sprintf("\"v1-%x-%s-%s\"", hash[:8], strings.ReplaceAll(body.Path, "/", "_"), body.ResolvedCID))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(data)
		}
	})
}

type boundedJSONBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedJSONBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errDirectoryJSONTooLarge
	}
	return b.Buffer.Write(p)
}

func marshalDirectoryJSON(body directoryResponse) ([]byte, error) {
	var buf boundedJSONBuffer
	buf.limit = maxDirectoryJSONSize
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return nil, err
	}
	data := buf.Bytes()
	if len(data) > 0 && data[len(data)-1] == '\n' {
		data = data[:len(data)-1]
	}
	return data, nil
}

func canonicalDirectoryQuery(rawQuery string) (string, bool) {
	parts := strings.Split(rawQuery, "&")
	if len(parts) != 1 {
		return "", false
	}
	keyValue := strings.SplitN(parts[0], "=", 2)
	if len(keyValue) != 2 || keyValue[0] != "path" || keyValue[1] == "" {
		return "", false
	}
	decoded, err := url.QueryUnescape(keyValue[1])
	if err != nil || decoded == "" || "path="+url.QueryEscape(decoded) != rawQuery {
		return "", false
	}
	return decoded, true
}

func directoryTimeout(configured time.Duration) time.Duration {
	if configured > 0 && configured < 30*time.Second {
		return configured
	}
	return 30 * time.Second
}

func enumerateDirectory(ctx context.Context, nd *Node, rawPath string) (directoryResponse, int, string) {
	if len(rawPath) > 4096 || rawPath == "" || strings.HasSuffix(rawPath, "/") {
		return directoryResponse{}, http.StatusBadRequest, "invalid_path"
	}
	p, err := boxopath.NewPath(rawPath)
	if err != nil || p.Namespace() != boxopath.IPFSNamespace || p.String() != rawPath || len(p.Segments()) > 128 {
		return directoryResponse{}, http.StatusBadRequest, "invalid_path"
	}
	ip, err := boxopath.NewImmutablePath(p)
	if err != nil {
		return directoryResponse{}, http.StatusBadRequest, "invalid_path"
	}
	if nd.resolver == nil {
		return directoryResponse{}, http.StatusNotImplemented, "unsupported_mode"
	}
	node, link, err := nd.resolver.ResolvePath(ctx, ip)
	if err != nil {
		return directoryResponse{}, directoryStatus(ctx, err), directoryCode(err, ctx)
	}
	clink, ok := link.(cidlink.Link)
	if !ok {
		return directoryResponse{}, http.StatusInternalServerError, "internal"
	}
	if _, ok := node.(hamt.UnixFSHAMTShard); ok {
		return directoryResponse{}, http.StatusNotImplemented, "unsupported_directory_kind"
	}
	dir, ok := node.(directory.UnixFSBasicDir)
	if !ok {
		return directoryResponse{}, http.StatusUnprocessableEntity, "not_directory"
	}
	entries := make([]directoryEntry, 0)
	it := dir.Iterator()
	for !it.Done() {
		if err := ctx.Err(); err != nil {
			return directoryResponse{}, http.StatusGatewayTimeout, "timeout"
		}
		name, link := it.Next()
		if name == nil || link == nil {
			return directoryResponse{}, http.StatusInternalServerError, "internal"
		}
		nameString := name.String()
		if !utf8.ValidString(nameString) || len([]byte(nameString)) > 1024 {
			return directoryResponse{}, http.StatusInternalServerError, "internal"
		}
		child, ok := link.Link().(cidlink.Link)
		if !ok {
			return directoryResponse{}, http.StatusInternalServerError, "internal"
		}
		entries = append(entries, directoryEntry{Name: nameString, CID: child.Cid.String()})
		if len(entries) > 1000 {
			return directoryResponse{}, http.StatusRequestEntityTooLarge, "directory_too_large"
		}
	}
	if err := ctx.Err(); err != nil {
		return directoryResponse{}, http.StatusGatewayTimeout, "timeout"
	}
	slices.SortFunc(entries, func(a, b directoryEntry) int { return strings.Compare(a.Name, b.Name) })
	return directoryResponse{Version: 1, Path: rawPath, ResolvedCID: clink.Cid.String(), Entries: entries}, 0, ""
}

func directoryStatus(ctx context.Context, err error) int {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled) {
		return http.StatusGatewayTimeout
	}
	var noLink *boxoresolver.ErrNoLink
	if errors.As(err, &noLink) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func directoryCode(err error, ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled) {
		return "timeout"
	}
	var noLink *boxoresolver.ErrNoLink
	if errors.As(err, &noLink) {
		return "not_found"
	}
	return "internal"
}

type statsResponse struct {
	Version        int   `json:"version"`
	FilesProcessed int64 `json:"filesProcessed"`
	OriginBytes    int64 `json:"originBytes"`
}

// statsHandler serves cumulative gateway usage counters as JSON for the web UI.
func statsHandler(stats *Stats) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		files, bytes := stats.Snapshot()
		data, err := json.Marshal(statsResponse{Version: 1, FilesProcessed: files, OriginBytes: bytes})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=5")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(data)
		}
	})
}

// withStatsCounter records one processed file per successfully served gateway
// request (HTTP status < 400).
func withStatsCounter(next http.Handler, stats *Stats) http.Handler {
	if stats == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := httpsnoop.CaptureMetrics(next, w, r)
		if m.Code < http.StatusBadRequest {
			stats.AddFile()
		}
	})
}

func webUIHandler() http.Handler {
	ui, _ := fs.Sub(webuiFS, "webui/dist")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name != "" && name != "explore/" && !fs.ValidPath(name) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if name == "" || name == "explore" || name == "explore/" || strings.HasPrefix(name, "explore/") {
			name = "index.html"
		}
		if !fs.ValidPath(name) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		file, err := ui.Open(name)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		content, err := io.ReadAll(file)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		http.ServeContent(w, r, path.Base(name), info.ModTime(), bytes.NewReader(content))
	})
}

func withUIHostGate(ui, native http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uiPath := r.URL.Path == "/" || r.URL.Path == "/explore" || r.URL.Path == "/explore/" || strings.HasPrefix(r.URL.Path, "/explore/") || r.URL.Path == directoryAPIPath || r.URL.Path == statsAPIPath || r.URL.Path == "/index.html" || strings.HasPrefix(r.URL.Path, "/assets/")
		if uiPath {
			ui.ServeHTTP(w, r)
			return
		}
		native.ServeHTTP(w, r)
	})
}

func makeMetricsAndDebuggingHandler() *http.ServeMux {
	mux := http.NewServeMux()

	gatherers := prometheus.Gatherers{
		prometheus.DefaultGatherer,
	}
	options := promhttp.HandlerOpts{}
	mux.Handle("/debug/metrics/prometheus", promhttp.HandlerFor(gatherers, options))
	mux.Handle("/debug/vars", http.DefaultServeMux)
	mux.Handle("/debug/pprof/", http.DefaultServeMux)
	mux.HandleFunc("/debug/stack", func(w http.ResponseWriter, r *http.Request) {
		if err := writeAllGoroutineStacks(w); err != nil {
			goLog.Error(err)
		}
	})
	MutexFractionOption("/debug/pprof-mutex/", mux)
	BlockProfileRateOption("/debug/pprof-block/", mux)

	return mux
}

func addLogHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/mgr/log/level", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		q := r.URL.Query()
		subsystem := q.Get("subsystem")
		level := q.Get("level")

		if subsystem == "" || level == "" {
			http.Error(w, "both subsystem and level must be passed", http.StatusBadRequest)
			return
		}

		if err := log.SetLogLevel(subsystem, level); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	})
	mux.HandleFunc("/mgr/log/ls", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Join(log.GetSubsystems(), ",")))
	})
}

func gcHandler(gnd *Node) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		var body struct {
			BytesToFree int64
		}

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if err := gnd.GC(r.Context(), body.BytesToFree); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
}

func purgePeerHandler(p2pHost host.Host) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		q := r.URL.Query()
		peerIDStr := q.Get("peer")
		if peerIDStr == "" {
			http.Error(w, "missing peer id", http.StatusBadRequest)
			return
		}

		if peerIDStr == "all" {
			purgeCount, err := purgeAllConnections(p2pHost)
			if err != nil {
				goLog.Errorw("Error closing all libp2p connections", "err", err)
				http.Error(w, "error closing connections", http.StatusInternalServerError)
				return
			}
			goLog.Infow("Purged connections", "count", purgeCount)

			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintln(w, "Peer connections purged:", purgeCount)
			return
		}

		peerID, err := peer.Decode(peerIDStr)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		err = purgeConnection(p2pHost, peerID)
		if err != nil {
			goLog.Errorw("Error closing libp2p connection", "err", err, "peer", peerID)
			http.Error(w, "error closing connection", http.StatusInternalServerError)
			return
		}
		goLog.Infow("Purged connection", "peer", peerID)

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "Purged connection to peer", peerID)
	}
}

func purgeConnection(p2pHost host.Host, peerID peer.ID) error {
	peerStore := p2pHost.Peerstore()
	if peerStore != nil {
		peerStore.RemovePeer(peerID)
		peerStore.ClearAddrs(peerID)
	}
	return p2pHost.Network().ClosePeer(peerID)
}

func purgeAllConnections(p2pHost host.Host) (int, error) {
	net := p2pHost.Network()
	peers := net.Peers()

	peerStore := p2pHost.Peerstore()
	if peerStore != nil {
		for _, peerID := range peers {
			peerStore.RemovePeer(peerID)
			peerStore.ClearAddrs(peerID)
		}
	}

	var errCount, purgeCount int
	for _, peerID := range peers {
		err := net.ClosePeer(peerID)
		if err != nil {
			goLog.Errorw("Closing libp2p connection", "err", err, "peer", peerID)
			errCount++
		} else {
			purgeCount++
		}
	}

	if errCount != 0 {
		return 0, fmt.Errorf("error closing connections to %d peers", errCount)
	}

	return purgeCount, nil
}

func showPeersHandler(p2pHost host.Host) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		w.Header().Set("Content-Type", "application/json; charset=utf-8")

		peers := p2pHost.Network().Peers()
		body := struct {
			Count int64
			Peers []string
		}{
			Count: int64(len(peers)),
		}

		if len(peers) != 0 {
			peerStrs := make([]string, len(peers))
			for i, peerID := range peers {
				peerStrs[i] = peerID.String()
			}
			body.Peers = peerStrs
		}

		enc := json.NewEncoder(w)
		if err := enc.Encode(body); err != nil {
			goLog.Errorw("cannot write response", "err", err)
			http.Error(w, "", http.StatusInternalServerError)
		}
	}
}

func withConnect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ServeMux does not support requests with CONNECT method,
		// so we need to handle them separately
		// https://golang.org/src/net/http/request.go#L111
		if r.Method == http.MethodConnect {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func withRequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := httpsnoop.CaptureMetrics(next, w, r)
		goLog.Infow(r.Method, "url", r.URL, "host", r.Host, "code", m.Code, "duration", m.Duration, "written", m.Written, "ua", r.UserAgent(), "referer", r.Referer())
	})
}

func setupGatewayHandler(cfg Config, nd *Node) (http.Handler, error) {
	var (
		backend gateway.IPFSBackend
		err     error
	)

	options := []gateway.BackendOption{
		gateway.WithValueStore(nd.vs),
		gateway.WithNameSystem(nd.ns),
		gateway.WithResolver(nd.resolver), // May be nil, but that is fine.
	}

	if len(cfg.RemoteBackends) > 0 && cfg.RemoteBackendMode == RemoteBackendCAR {
		var fetcher gateway.CarFetcher
		fetcher, err = gateway.NewRemoteCarFetcher(cfg.RemoteBackends, nil)
		if err != nil {
			return nil, err
		}
		backend, err = gateway.NewCarBackend(fetcher, options...)
	} else {
		backend, err = gateway.NewBlocksBackend(nd.bsrv, options...)
	}
	if err != nil {
		return nil, err
	}

	headers := map[string][]string{}

	// Note: in the future we may want to make this more configurable.
	//noDNSLink := false

	// Helper function to check if a domain should have DNSLink enabled
	isDNSLinkAllowedForDomain := func(domain string) bool {
		// If no domains specified, allow all (backward compatibility)
		if len(cfg.DNSLinkGatewayDomains) == 0 {
			return true
		}

		// Check if the domain matches any allowed domain
		for _, allowed := range cfg.DNSLinkGatewayDomains {
			// Exact match
			if domain == allowed {
				return true
			}
			// Subdomain match (e.g., "sub.example.com" matches "example.com")
			if strings.HasSuffix(domain, "."+allowed) {
				return true
			}
		}
		goLog.Debugf("DNSLink blocked for domain %s (not in allowed list)", domain)
		return false
	}

	// TODO: allow appending hostnames to this list via ENV variable (separate PATH_GATEWAY_HOSTS & SUBDOMAIN_GATEWAY_HOSTS)
	publicGateways := map[string]*gateway.PublicGateway{
		"localhost": {
			Paths:                 []string{"/ipfs", "/ipns", "/version"},
			NoDNSLink:             len(cfg.DNSLinkGatewayDomains) > 0,
			InlineDNSLink:         false,
			DeserializedResponses: true,
			UseSubdomains:         true,
		},
	}
	for _, domain := range cfg.GatewayDomains {
		publicGateways[domain] = &gateway.PublicGateway{
			Paths:                 []string{"/ipfs", "/ipns", "/version"},
			NoDNSLink:             !isDNSLinkAllowedForDomain(domain),
			InlineDNSLink:         true,
			DeserializedResponses: true,
			UseSubdomains:         false,
		}
	}

	for _, domain := range cfg.SubdomainGatewayDomains {
		publicGateways[domain] = &gateway.PublicGateway{
			Paths:                 []string{"/ipfs", "/ipns", "/version"},
			NoDNSLink:             !isDNSLinkAllowedForDomain(domain),
			InlineDNSLink:         true,
			DeserializedResponses: true,
			UseSubdomains:         true,
		}
	}

	for _, domain := range cfg.TrustlessGatewayDomains {
		publicGateways[domain] = &gateway.PublicGateway{
			Paths:                 []string{"/ipfs", "/ipns", "/version"},
			NoDNSLink:             true,
			InlineDNSLink:         true,
			DeserializedResponses: false,
			UseSubdomains:         slices.Contains(cfg.SubdomainGatewayDomains, domain),
		}
	}

	// If we're doing tests, ensure the right public gateways are enabled.
	if os.Getenv("GATEWAY_CONFORMANCE_TEST") == "true" {
		publicGateways["example.com"] = &gateway.PublicGateway{
			Paths:                 []string{"/ipfs", "/ipns"},
			NoDNSLink:             !isDNSLinkAllowedForDomain("example.com"),
			InlineDNSLink:         true,
			DeserializedResponses: true,
			UseSubdomains:         true,
		}

		// TODO: revisit the below once we clarify desired behavior in https://specs.ipfs.tech/http-gateways/subdomain-gateway/
		publicGateways["localhost"].InlineDNSLink = true
	}

	// After configuring all the standard domains, add DNSLink-only domains
	for _, domain := range cfg.DNSLinkGatewayDomains {
		// Only add if not already configured
		if _, exists := publicGateways[domain]; !exists {
			publicGateways[domain] = &gateway.PublicGateway{
				Paths:                 []string{"/ipfs", "/ipns", "/version"},
				NoDNSLink:             false,
				InlineDNSLink:         true,
				DeserializedResponses: true,
				UseSubdomains:         false,
			}
		}
	}

	gwConf := gateway.Config{
		DeserializedResponses:       true,
		PublicGateways:              publicGateways,
		NoDNSLink:                   len(cfg.DNSLinkGatewayDomains) > 0,
		MaxConcurrentRequests:       cfg.MaxConcurrentRequests, // When exceeded, returns 429 with Retry-After: 60 (hardcoded in boxo)
		RetrievalTimeout:            cfg.RetrievalTimeout,
		MaxRequestDuration:          cfg.MaxRequestDuration,
		MaxRangeRequestFileSize:     cfg.MaxRangeRequestFileSize,
		MaxDeserializedResponseSize: cfg.MaxDeserializedResponseSize,
		MaxUnixFSDAGResponseSize:    cfg.MaxUnixFSDAGResponseSize,
		DiagnosticServiceURL:        cfg.DiagnosticServiceURL,
	}
	gwHandler := gateway.NewHandler(gwConf, backend)

	ipfsHandler := withStatsCounter(withHTTPMetrics(gwHandler, "ipfs", cfg.disableMetrics), nd.stats)
	ipnsHandler := withStatsCounter(withHTTPMetrics(gwHandler, "ipns", cfg.disableMetrics), nd.stats)

	nativeMux := http.NewServeMux()
	nativeMux.Handle("/ipfs/", ipfsHandler)
	nativeMux.Handle("/ipns/", ipnsHandler)
	nativeMux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Client: %s\n", name)
		fmt.Fprintf(w, "Version: %s\n", version)
	})
	nativeMux.HandleFunc("/api/v0/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
		w.Write([]byte("The /api/v0 Kubo RPC is not part of IPFS Gateway Specs (https://specs.ipfs.tech/http-gateways/). Consider refactoring your app. If you still need this Kubo endpoint, please self-host a Kubo instance yourself: https://docs.ipfs.tech/install/command-line/ with proper auth https://github.com/ipfs/kubo/blob/master/docs/config.md#apiauthorizations"))
	})
	uiMux := http.NewServeMux()
	uiMux.Handle(directoryAPIPath, directoryHandler(cfg, nd))
	uiMux.Handle(statsAPIPath, statsHandler(nd.stats))
	uiMux.Handle("/", webUIHandler())

	// Construct the HTTP handler for the gateway.
	nativeHandler := withConnect(nativeMux)
	nativeHandler = http.Handler(gateway.NewHostnameHandler(gwConf, backend, nativeHandler))
	handler := withUIHostGate(uiMux, nativeHandler)

	// The Cloudflare Images-like /i/ endpoint is served on every host, ahead of
	// the hostname/UI gating, and reads content directly from the backend.
	imgHandler := withHTTPMetrics(imageHandler(cfg, backend, nd.stats), "image", cfg.disableMetrics)
	handler = withImageRoute(imgHandler, handler)

	// Add custom headers and liberal CORS.
	handler = gateway.NewHeaders(headers).ApplyCors().Wrap(handler)

	handler = servertiming.Middleware(handler, nil)

	// Add logging.
	handler = withRequestLogger(handler)

	// Add tracing.
	handler = withTracingAndDebug(handler, cfg.TracingAuthToken)

	return handler, nil
}

func withTracingAndDebug(next http.Handler, authToken string) http.Handler {
	next = otelhttp.NewHandler(next, "Gateway")

	// Remove tracing and cache skipping headers if not authorized
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// Disable tracing/debug headers if auth token missing or invalid
		if authToken == "" || request.Header.Get("Authorization") != authToken {
			if request.Header.Get("Traceparent") != "" || request.Header.Get("Tracestate") != "" || request.Header.Get(NoBlockcacheHeader) != "" {
				http.Error(writer, "The request is not accompanied by a valid authorization header", http.StatusUnauthorized)
				return
			}
		}

		// Process cache skipping header
		if noBlockCache := request.Header.Get(NoBlockcacheHeader); noBlockCache == "true" {
			ds, err := leveldb.NewDatastore("", nil)
			if err != nil {
				writer.WriteHeader(http.StatusInternalServerError)
				_, _ = writer.Write([]byte(err.Error()))
				return
			}
			newCtx := context.WithValue(request.Context(), NoBlockcache{}, blockstore.NewBlockstore(ds))
			request = request.WithContext(newCtx)
		}

		next.ServeHTTP(writer, request)
	})
}

const NoBlockcacheHeader = "Rainbow-No-Blockcache"

type NoBlockcache struct{}

// MutexFractionOption allows to set runtime.SetMutexProfileFraction via HTTP
// using POST request with parameter 'fraction'.
func MutexFractionOption(path string, mux *http.ServeMux) *http.ServeMux {
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "only POST allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		asfr := r.Form.Get("fraction")
		if len(asfr) == 0 {
			http.Error(w, "parameter 'fraction' must be set", http.StatusBadRequest)
			return
		}

		fr, err := strconv.Atoi(asfr)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		runtime.SetMutexProfileFraction(fr)
	})

	return mux
}

// BlockProfileRateOption allows to set runtime.SetBlockProfileRate via HTTP
// using POST request with parameter 'rate'.
// The profiler tries to sample 1 event every <rate> nanoseconds.
// If rate == 1, then the profiler samples every blocking event.
// To disable, set rate = 0.
func BlockProfileRateOption(path string, mux *http.ServeMux) *http.ServeMux {
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "only POST allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		rateStr := r.Form.Get("rate")
		if len(rateStr) == 0 {
			http.Error(w, "parameter 'rate' must be set", http.StatusBadRequest)
			return
		}

		rate, err := strconv.Atoi(rateStr)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		runtime.SetBlockProfileRate(rate)
	})
	return mux
}
