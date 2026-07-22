# Image Transformations (`/i/`)

Rainbow exposes a Cloudflare Images-like endpoint for transforming images
served from IPFS on the fly. A rendered image is a pure function of an
immutable CID plus its transform parameters, so responses are content-addressed
and safe to cache aggressively.

## Request shape

```
/i/<cid>[/path/to/file]?w=800&h=600&fit=cover&q=80&format=webp&dpr=2
```

The path after `/i/` is a content-addressed (CID-rooted) IPFS path, exactly as
it would appear after `/ipfs/`. Only immutable `/ipfs` paths are accepted;
IPNS and DNSLink are not resolved here. The endpoint is available on every host
the gateway serves.

> [!NOTE]
> This endpoint requires a backend that can reconstruct UnixFS files
> (the default blocks backend). It is not available when Rainbow runs in the
> remote `CAR` backend mode and returns `501 Not Implemented` in that case.

## Query parameters

| Parameter       | Values                                          | Default   | Description |
| --------------- | ----------------------------------------------- | --------- | ----------- |
| `w`, `width`    | integer ≥ 0                                     | `0`       | Target width in pixels. `0` derives width from height / source. |
| `h`, `height`   | integer ≥ 0                                     | `0`       | Target height in pixels. `0` derives height from width / source. |
| `dpr`           | float `1`..`3`                                   | `1`       | Device pixel ratio; multiplies `w`/`h`. |
| `fit`           | `contain`, `cover`, `crop`, `scale-down`, `pad` | `contain` | How the image is fitted into the target box (see below). |
| `q`, `quality`  | integer `1`..`100`                              | `82`      | Lossy encoder quality (JPEG/WebP/AVIF). |
| `format`        | `auto`, `jpeg`, `png`, `gif`, `webp`, `avif`    | `auto`    | Output format. |

Width and height are clamped to a maximum of `5000` pixels (after `dpr`). Source
objects larger than 32 MiB are rejected with `413 Request Entity Too Large`.

### `fit` modes

- `contain` — scale to fit within the box, preserving aspect ratio (default).
- `cover` / `crop` — fill the box exactly, cropping the overflow from the center.
- `scale-down` — like `contain`, but never upscales beyond the source size.
- `pad` — like `contain`, centered on a transparent canvas of the exact box size.

### `format=auto`

With `format=auto` (the default) the output codec is negotiated from the
request's `Accept` header, preferring AVIF, then WebP. When the client accepts
neither, the source format is preserved. Auto responses include `Vary: Accept`.

Input formats decoded: JPEG, PNG, GIF, WebP, AVIF, TIFF, BMP. All codecs are
pure Go, so `CGO_ENABLED=0` builds (including the release Docker image) are
unaffected.

## Caching

Successful responses set `Cache-Control: public, max-age=29030400, immutable`
and a strong `ETag` derived from the resolved CID and the normalized transform
parameters. Conditional requests with `If-None-Match` return `304 Not Modified`.

## Examples

```
# Resize to 320px wide, preserving aspect ratio
/i/bafy.../photo.jpg?w=320

# Square thumbnail, cropped, as WebP
/i/bafy.../photo.jpg?w=200&h=200&fit=cover&format=webp

# Retina-density AVIF, negotiated automatically
/i/bafy.../photo.jpg?w=400&dpr=2   (with Accept: image/avif)
```
