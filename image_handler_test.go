package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"testing"

	"github.com/gen2brain/avif"
	"github.com/gen2brain/webp"
	_ "image/jpeg"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeTestPNG builds a simple w×h PNG and returns its encoded bytes.
func makeTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func decodedBounds(t *testing.T, body []byte) image.Rectangle {
	t.Helper()
	cfg, _, err := image.DecodeConfig(bytes.NewReader(body))
	require.NoError(t, err)
	return image.Rect(0, 0, cfg.Width, cfg.Height)
}

func TestImageHandler(t *testing.T) {
	t.Parallel()

	ts, gnd := mustTestServer(t, Config{
		Bitswap:        true,
		disableMetrics: true,
	})

	srcW, srcH := 200, 100
	imgCID := mustAddFile(t, gnd, makeTestPNG(t, srcW, srcH))
	base := ts.URL + imagePathPrefix + imgCID.String()

	get := func(t *testing.T, url string, headers map[string]string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, url, nil)
		require.NoError(t, err)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		return res
	}

	t.Run("no params preserves source format and dimensions", func(t *testing.T) {
		res := get(t, base, nil)
		defer res.Body.Close()
		assert.Equal(t, http.StatusOK, res.StatusCode)
		assert.Equal(t, "image/png", res.Header.Get("Content-Type"))
		assert.Contains(t, res.Header.Get("Cache-Control"), "immutable")
		assert.NotEmpty(t, res.Header.Get("ETag"))

		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		assert.Equal(t, image.Rect(0, 0, srcW, srcH), decodedBounds(t, body))
	})

	t.Run("contain resize by width preserves aspect ratio", func(t *testing.T) {
		res := get(t, base+"?w=100", nil)
		defer res.Body.Close()
		assert.Equal(t, http.StatusOK, res.StatusCode)
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		assert.Equal(t, image.Rect(0, 0, 100, 50), decodedBounds(t, body))
	})

	t.Run("cover fit crops to exact box", func(t *testing.T) {
		res := get(t, base+"?w=80&h=80&fit=cover", nil)
		defer res.Body.Close()
		assert.Equal(t, http.StatusOK, res.StatusCode)
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		assert.Equal(t, image.Rect(0, 0, 80, 80), decodedBounds(t, body))
	})

	t.Run("scale-down never upscales", func(t *testing.T) {
		res := get(t, base+"?w=400&h=400&fit=scale-down", nil)
		defer res.Body.Close()
		assert.Equal(t, http.StatusOK, res.StatusCode)
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		// Source is 200x100; scale-down clamps the box to the source bounds.
		assert.Equal(t, image.Rect(0, 0, srcW, srcH), decodedBounds(t, body))
	})

	t.Run("explicit jpeg format", func(t *testing.T) {
		res := get(t, base+"?w=50&format=jpeg", nil)
		defer res.Body.Close()
		assert.Equal(t, http.StatusOK, res.StatusCode)
		assert.Equal(t, "image/jpeg", res.Header.Get("Content-Type"))
	})

	t.Run("explicit webp format decodes", func(t *testing.T) {
		res := get(t, base+"?w=60&format=webp", nil)
		defer res.Body.Close()
		assert.Equal(t, http.StatusOK, res.StatusCode)
		assert.Equal(t, "image/webp", res.Header.Get("Content-Type"))
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		_, err = webp.Decode(bytes.NewReader(body))
		assert.NoError(t, err)
	})

	t.Run("auto format negotiates webp via Accept and sets Vary", func(t *testing.T) {
		res := get(t, base+"?w=60&format=auto", map[string]string{"Accept": "image/webp,*/*"})
		defer res.Body.Close()
		assert.Equal(t, http.StatusOK, res.StatusCode)
		assert.Equal(t, "image/webp", res.Header.Get("Content-Type"))
		assert.Equal(t, "Accept", res.Header.Get("Vary"))
	})

	t.Run("auto format negotiates avif via Accept", func(t *testing.T) {
		res := get(t, base+"?w=60", map[string]string{"Accept": "image/avif,image/webp,*/*"})
		defer res.Body.Close()
		assert.Equal(t, http.StatusOK, res.StatusCode)
		assert.Equal(t, "image/avif", res.Header.Get("Content-Type"))
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		_, err = avif.Decode(bytes.NewReader(body))
		assert.NoError(t, err)
	})

	t.Run("conditional request returns 304", func(t *testing.T) {
		first := get(t, base+"?w=100", nil)
		etag := first.Header.Get("ETag")
		first.Body.Close()
		require.NotEmpty(t, etag)

		res := get(t, base+"?w=100", map[string]string{"If-None-Match": etag})
		defer res.Body.Close()
		assert.Equal(t, http.StatusNotModified, res.StatusCode)
	})

	t.Run("invalid parameters return 400", func(t *testing.T) {
		for _, q := range []string{"?w=-1", "?fit=bogus", "?q=0", "?q=101", "?dpr=9", "?format=tiff"} {
			res := get(t, base+q, nil)
			res.Body.Close()
			assert.Equal(t, http.StatusBadRequest, res.StatusCode, "query %q", q)
		}
	})

	t.Run("non-image content returns 415", func(t *testing.T) {
		txtCID := mustAddFile(t, gnd, []byte("this is not an image"))
		res := get(t, ts.URL+imagePathPrefix+txtCID.String(), nil)
		defer res.Body.Close()
		assert.Equal(t, http.StatusUnsupportedMediaType, res.StatusCode)
	})

	t.Run("missing cid returns 400", func(t *testing.T) {
		res := get(t, ts.URL+imagePathPrefix, nil)
		defer res.Body.Close()
		assert.Equal(t, http.StatusBadRequest, res.StatusCode)
	})
}
