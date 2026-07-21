// Schemes allowed to pass through untouched. Providing a custom urlTransform to
// react-markdown replaces its built-in sanitizer, so we must reject dangerous
// schemes (javascript:, data:, vbscript:, ...) ourselves to avoid XSS from
// untrusted markdown served by the gateway.
const allowedSchemes = ['http', 'https', 'mailto', 'tel', 'ipfs', 'ipns']

// resolveMarkdownUrl rewrites a link/image URL found inside a rendered markdown
// file so it points at the gateway. Relative URLs are resolved against the
// directory of the markdown file (filePath, a decoded /ipfs/... path) and
// returned as a root-relative, percent-encoded gateway path. Absolute paths,
// protocol-relative URLs, allow-listed scheme URLs, and pure fragments are
// returned unchanged; disallowed schemes are dropped (empty string).
export function resolveMarkdownUrl(filePath: string, url: string): string {
  if (!url) return url
  const trimmed = url.trim()

  if (trimmed.startsWith('#') || trimmed.startsWith('//') || trimmed.startsWith('/')) {
    return trimmed
  }

  const schemeMatch = trimmed.match(/^([a-z][a-z0-9+.-]*):/i)
  if (schemeMatch) {
    return allowedSchemes.includes(schemeMatch[1].toLowerCase()) ? trimmed : ''
  }

  const cut = trimmed.search(/[?#]/)
  const rel = cut === -1 ? trimmed : trimmed.slice(0, cut)
  const suffix = cut === -1 ? '' : trimmed.slice(cut)

  const segments = filePath.split('/').filter(Boolean)
  segments.pop() // drop the file name → its directory
  for (const raw of rel.split('/')) {
    if (raw === '' || raw === '.') continue
    if (raw === '..') {
      if (segments.length > 0) segments.pop()
      continue
    }
    segments.push(safeDecode(raw))
  }

  const encoded = segments.map((segment) => encodeURIComponent(segment)).join('/')
  return `/${encoded}${suffix}`
}

function safeDecode(value: string): string {
  try {
    return decodeURIComponent(value)
  } catch {
    return value
  }
}
