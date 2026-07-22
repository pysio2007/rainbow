package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/disintegration/imaging"
	"github.com/gen2brain/avif"
	"github.com/gen2brain/webp"
	"github.com/ipfs/boxo/files"
	"github.com/ipfs/boxo/gateway"
	boxopath "github.com/ipfs/boxo/path"

	// Register decoders with the standard image package. imaging pulls in
	// jpeg/png/gif/tiff/bmp; the gen2brain packages self-register webp and avif
	// via image.RegisterFormat in their init().
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// imagePathPrefix is the request path namespace for the on-the-fly image
// transformation endpoint (a Cloudflare Images-like feature). It is served
// independently of the IPFS/IPNS path gateway and the WebUI.
const imagePathPrefix = "/i/"

const (
	// maxImageSourceBytes caps how many bytes we read from a source object
	// before giving up. Decoding is memory-heavy, so this bounds abuse.
	maxImageSourceBytes = 32 << 20 // 32 MiB

	// maxImageDimension caps the width/height (after DPR) of a rendered image.
	maxImageDimension = 5000

	// defaultImageQuality is the lossy encoder quality used when the request
	// does not specify one.
	defaultImageQuality = 82

	// imageCacheControl is aggressive because a rendered image is a pure
	// function of an immutable CID plus its transform parameters.
	imageCacheControl = "public, max-age=29030400, immutable"
)

// imageRequest is the parsed, validated set of transform options.
type imageRequest struct {
	width   int
	height  int
	fit     string // contain (default) | cover | crop | scale-down | pad
	quality int    // 1..100
	format  string // "" (auto), jpeg, png, gif, webp, avif
	autoFmt bool   // true when format=auto (or unset) and negotiation applies
}

// withImageRoute dispatches requests under /i/ to the image handler and passes
// everything else through to next. It sits above boxo's HostnameHandler so the
// endpoint is reachable on every host, not just the configured gateways.
func withImageRoute(image, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, imagePathPrefix) {
			image.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeImageError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, msg+"\n")
	}
}

// imageHandler serves transformed images from immutable IPFS content.
//
// Request shape:
//
//	/i/<cid>[/path/to/file]?w=800&h=600&fit=cover&q=80&format=webp&dpr=2
//
// Supported query parameters:
//
//	w, width    target width in pixels (0 = derive from height / source)
//	h, height   target height in pixels (0 = derive from width / source)
//	dpr         device pixel ratio multiplier for w/h, 1..3 (default 1)
//	fit         contain (default) | cover | crop | scale-down | pad
//	q, quality  lossy encoder quality 1..100 (default 82)
//	format      auto (default) | jpeg | png | gif | webp | avif
func imageHandler(cfg Config, backend gateway.IPFSBackend, stats *Stats) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeImageError(w, r, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if backend == nil || cfg.RemoteBackendMode == RemoteBackendCAR {
			// CAR backends do not expose reconstructed UnixFS files via GetAll.
			writeImageError(w, r, http.StatusNotImplemented, "image transformation is not available in this backend mode")
			return
		}

		ip, ok := imageImmutablePath(r.URL.Path)
		if !ok {
			writeImageError(w, r, http.StatusBadRequest, "invalid image path")
			return
		}

		req, err := parseImageRequest(r)
		if err != nil {
			writeImageError(w, r, http.StatusBadRequest, err.Error())
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), imageTimeout(cfg.RetrievalTimeout))
		defer cancel()

		md, node, err := backend.GetAll(ctx, ip)
		if err != nil {
			writeImageError(w, r, imageBackendStatus(err), "unable to retrieve image content")
			return
		}
		defer node.Close()

		file := files.ToFile(node)
		if file == nil {
			writeImageError(w, r, http.StatusUnprocessableEntity, "path does not reference a file")
			return
		}

		data, err := readAllLimited(file, maxImageSourceBytes)
		if errors.Is(err, errImageTooLarge) {
			writeImageError(w, r, http.StatusRequestEntityTooLarge, "source image is too large to process")
			return
		}
		if err != nil {
			writeImageError(w, r, http.StatusBadGateway, "failed to read image content")
			return
		}

		// Resolve auto format against the client's Accept header before we build
		// the ETag so cached variants stay coherent.
		outFormat := req.format
		if req.autoFmt {
			outFormat = negotiateImageFormat(r.Header.Get("Accept"))
		}

		etag := imageETag(md.LastSegment, req, outFormat)
		if req.autoFmt {
			w.Header().Set("Vary", "Accept")
		}
		if match := r.Header.Get("If-None-Match"); match != "" && etagMatches(match, etag) {
			w.Header().Set("ETag", etag)
			w.Header().Set("Cache-Control", imageCacheControl)
			w.WriteHeader(http.StatusNotModified)
			return
		}

		src, srcFormat, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			writeImageError(w, r, http.StatusUnsupportedMediaType, "unsupported or corrupt image format")
			return
		}

		// For auto negotiation with no browser-preferred modern format, keep the
		// source codec instead of forcing a re-encode to a heavier one.
		if req.autoFmt && outFormat == "" {
			outFormat = normalizeSourceFormat(srcFormat)
		}

		dst := transformImage(src, req)

		var buf bytes.Buffer
		contentType, err := encodeImage(&buf, dst, outFormat, req.quality)
		if err != nil {
			writeImageError(w, r, http.StatusInternalServerError, "failed to encode image")
			return
		}

		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
		w.Header().Set("Cache-Control", imageCacheControl)
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(buf.Bytes())
		}
		if stats != nil {
			stats.AddFile()
		}
	})
}

// imageImmutablePath converts a /i/<cid>[/path] request path into an immutable
// IPFS path. Only content-addressed (CID-rooted) paths are accepted.
func imageImmutablePath(urlPath string) (boxopath.ImmutablePath, bool) {
	rest := strings.TrimPrefix(urlPath, imagePathPrefix)
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" {
		return boxopath.ImmutablePath{}, false
	}
	p, err := boxopath.NewPath("/ipfs/" + rest)
	if err != nil || p.Namespace() != boxopath.IPFSNamespace {
		return boxopath.ImmutablePath{}, false
	}
	ip, err := boxopath.NewImmutablePath(p)
	if err != nil {
		return boxopath.ImmutablePath{}, false
	}
	return ip, true
}

func parseImageRequest(r *http.Request) (imageRequest, error) {
	q := r.URL.Query()
	req := imageRequest{fit: "contain", quality: defaultImageQuality, autoFmt: true}

	width, err := parseDimension(firstNonEmpty(q.Get("w"), q.Get("width")))
	if err != nil {
		return req, fmt.Errorf("invalid width")
	}
	height, err := parseDimension(firstNonEmpty(q.Get("h"), q.Get("height")))
	if err != nil {
		return req, fmt.Errorf("invalid height")
	}

	dpr := 1.0
	if v := q.Get("dpr"); v != "" {
		dpr, err = strconv.ParseFloat(v, 64)
		if err != nil || dpr < 1 || dpr > 3 {
			return req, fmt.Errorf("invalid dpr (allowed 1..3)")
		}
	}
	req.width = clampDimension(int(float64(width)*dpr + 0.5))
	req.height = clampDimension(int(float64(height)*dpr + 0.5))

	if v := q.Get("fit"); v != "" {
		switch v {
		case "contain", "cover", "crop", "scale-down", "pad":
			req.fit = v
		default:
			return req, fmt.Errorf("invalid fit (allowed: contain, cover, crop, scale-down, pad)")
		}
	}

	if v := firstNonEmpty(q.Get("q"), q.Get("quality")); v != "" {
		req.quality, err = strconv.Atoi(v)
		if err != nil || req.quality < 1 || req.quality > 100 {
			return req, fmt.Errorf("invalid quality (allowed 1..100)")
		}
	}

	if v := q.Get("format"); v != "" && v != "auto" {
		switch v {
		case "jpeg", "jpg":
			req.format = "jpeg"
		case "png":
			req.format = "png"
		case "gif":
			req.format = "gif"
		case "webp":
			req.format = "webp"
		case "avif":
			req.format = "avif"
		default:
			return req, fmt.Errorf("invalid format (allowed: auto, jpeg, png, gif, webp, avif)")
		}
		req.autoFmt = false
	}

	return req, nil
}

// transformImage applies the requested resize/crop. A zero width and height
// means "no geometric change".
func transformImage(src image.Image, req imageRequest) image.Image {
	w, h := req.width, req.height
	if w == 0 && h == 0 {
		return src
	}

	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()

	switch req.fit {
	case "cover", "crop":
		if w == 0 {
			w = sw
		}
		if h == 0 {
			h = sh
		}
		return imaging.Fill(src, w, h, imaging.Center, imaging.Lanczos)
	case "pad":
		fitted := containResize(src, w, h)
		if w == 0 || h == 0 {
			return fitted
		}
		bg := imaging.New(w, h, color.NRGBA{})
		return imaging.PasteCenter(bg, fitted)
	case "scale-down":
		// Never upscale: clamp targets to the source dimensions, then contain.
		if w == 0 || w > sw {
			if w > sw {
				w = sw
			}
		}
		if h == 0 || h > sh {
			if h > sh {
				h = sh
			}
		}
		return containResize(src, w, h)
	default: // contain
		return containResize(src, w, h)
	}
}

// containResize scales the image to fit within the box while preserving aspect
// ratio. When only one dimension is provided the other is derived.
func containResize(src image.Image, w, h int) image.Image {
	switch {
	case w == 0 && h == 0:
		return src
	case w == 0 || h == 0:
		return imaging.Resize(src, w, h, imaging.Lanczos)
	default:
		return imaging.Fit(src, w, h, imaging.Lanczos)
	}
}

// encodeImage writes img to buf in the requested format and returns the MIME
// content type. An empty format defaults to jpeg.
func encodeImage(buf *bytes.Buffer, img image.Image, format string, quality int) (string, error) {
	switch format {
	case "png":
		return "image/png", imaging.Encode(buf, img, imaging.PNG)
	case "gif":
		return "image/gif", imaging.Encode(buf, img, imaging.GIF)
	case "webp":
		return "image/webp", webp.Encode(buf, img, webp.Options{Quality: quality})
	case "avif":
		return "image/avif", avif.Encode(buf, img, avif.Options{Quality: quality, Speed: avif.DefaultSpeed})
	default: // jpeg
		return "image/jpeg", imaging.Encode(buf, img, imaging.JPEG, imaging.JPEGQuality(quality))
	}
}

// negotiateImageFormat picks a modern format from the Accept header, preferring
// AVIF over WebP. It returns "" when neither is acceptable, signalling that the
// source format should be preserved.
func negotiateImageFormat(accept string) string {
	if accept == "" {
		return ""
	}
	if strings.Contains(accept, "image/avif") {
		return "avif"
	}
	if strings.Contains(accept, "image/webp") {
		return "webp"
	}
	return ""
}

// normalizeSourceFormat maps a decoder format name to an encoder we support,
// falling back to jpeg for anything without a lossless-friendly encoder here.
func normalizeSourceFormat(srcFormat string) string {
	switch srcFormat {
	case "png":
		return "png"
	case "gif":
		return "gif"
	case "webp":
		return "webp"
	case "avif":
		return "avif"
	default:
		return "jpeg"
	}
}

// imageETag derives a strong ETag from the resolved (content-addressed) path
// plus the transform parameters and chosen output format.
func imageETag(resolved boxopath.ImmutablePath, req imageRequest, outFormat string) string {
	key := fmt.Sprintf("%s|w=%d|h=%d|fit=%s|q=%d|fmt=%s", resolved.String(), req.width, req.height, req.fit, req.quality, outFormat)
	sum := sha256.Sum256([]byte(key))
	return fmt.Sprintf("\"i1-%x\"", sum[:16])
}

func etagMatches(ifNoneMatch, etag string) bool {
	for _, candidate := range strings.Split(ifNoneMatch, ",") {
		candidate = strings.TrimSpace(candidate)
		candidate = strings.TrimPrefix(candidate, "W/")
		if candidate == "*" || candidate == etag {
			return true
		}
	}
	return false
}

var errImageTooLarge = errors.New("image source exceeds limit")

// readAllLimited reads up to limit bytes, returning errImageTooLarge if the
// source has more data than the limit allows.
func readAllLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errImageTooLarge
	}
	return data, nil
}

func imageBackendStatus(err error) int {
	var epErr *gateway.ErrorStatusCode
	if errors.As(err, &epErr) {
		return epErr.StatusCode
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return http.StatusGatewayTimeout
	}
	return http.StatusNotFound
}

func imageTimeout(configured time.Duration) time.Duration {
	if configured > 0 && configured < 60*time.Second {
		return configured
	}
	return 60 * time.Second
}

func parseDimension(v string) (int, error) {
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid dimension")
	}
	return n, nil
}

func clampDimension(n int) int {
	if n > maxImageDimension {
		return maxImageDimension
	}
	if n < 0 {
		return 0
	}
	return n
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
