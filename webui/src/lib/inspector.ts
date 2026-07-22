export type ParsedCid = { canonical: string; version: number; codec: string; multihash: string; digestLength: number }
export type UnixFsMetadata = { type: string; declaredFileSize?: number; mode?: number; mtime?: number }
export type MetadataLink = { name?: string; cid: string }
export type DirectLinks = { total: number; shown: number; truncated: boolean; items: MetadataLink[] }
export type RootMetadata = { status: string; blockBytes: number; blockVerified: boolean; unixfs?: UnixFsMetadata; directLinks: DirectLinks }
export type CanonicalLinks = { ipfs: string; nativeGateway: string; explorer: string; inspector: string; car: string }
export type MetadataV1 = { version: 1; parsedCid: ParsedCid; root: RootMetadata; canonicalLinks: CanonicalLinks }

const statuses: Record<string, string> = {
  fetched: 'Fetched', not_found: 'Not found', timeout: 'Timed out',
  unsupported_mode: 'Unsupported by this gateway', block_too_large: 'Block too large', integrity_error: 'Integrity error',
}

function record(value: unknown): Record<string, unknown> | null {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null
}
function stringValue(value: unknown): string | null { return typeof value === 'string' && value.length > 0 ? value : null }
function numberValue(value: unknown): number | null { return typeof value === 'number' && Number.isFinite(value) ? value : null }

function parseUnixFs(value: unknown): UnixFsMetadata | undefined {
  if (value === undefined) return undefined
  const source = record(value)
  const type = source && stringValue(source.type)
  if (!type) return undefined
  const result: UnixFsMetadata = { type }
  for (const key of ['declaredFileSize', 'mode'] as const) {
    if (source?.[key] !== undefined) {
      const parsed = numberValue(source[key])
      if (parsed === null) return undefined
      result[key] = parsed
    }
  }
  if (source?.mtime !== undefined) {
    const mtime = numberValue(source.mtime)
    if (mtime === null) return undefined
    result.mtime = mtime
  }
  return result
}

export function normalizeMetadata(value: unknown): MetadataV1 | null {
  const source = record(value)
  if (!source || source.version !== 1) return null
  const parsedSource = record(source.parsedCid)
  const rootSource = record(source.root)
  const linksSource = record(rootSource?.directLinks)
  const canonicalSource = record(source.canonicalLinks)
  if (!parsedSource || !rootSource || !linksSource || !canonicalSource) return null
  const parsed: ParsedCid = {
    canonical: stringValue(parsedSource.canonical) || '', version: numberValue(parsedSource.version) ?? -1,
    codec: stringValue(parsedSource.codec) || '', multihash: stringValue(parsedSource.multihash) || '', digestLength: numberValue(parsedSource.digestLength) ?? -1,
  }
  const root: RootMetadata = {
    status: stringValue(rootSource.status) || '', blockBytes: numberValue(rootSource.blockBytes) ?? -1,
    blockVerified: rootSource.blockVerified as boolean, directLinks: { total: numberValue(linksSource.total) ?? -1, shown: numberValue(linksSource.shown) ?? -1, truncated: linksSource.truncated as boolean, items: [] },
  }
  const unixfs = parseUnixFs(rootSource.unixfs)
  if (rootSource.unixfs !== undefined && !unixfs) return null
  if (unixfs) root.unixfs = unixfs
  if (!parsed.canonical || parsed.version < 0 || !parsed.codec || !parsed.multihash || parsed.digestLength < 0 || !root.status || root.blockBytes < 0 || typeof root.blockVerified !== 'boolean' || root.directLinks.total < 0 || root.directLinks.shown < 0 || typeof root.directLinks.truncated !== 'boolean') return null
  if (!Array.isArray(linksSource.items)) return null
  root.directLinks.items = linksSource.items.flatMap((item) => {
    const link = record(item)
    const cid = link && stringValue(link.cid)
    if (!cid) return []
    const name = link.name === undefined ? undefined : stringValue(link.name)
    return link.name !== undefined && !name ? [] : [{ ...(name ? { name } : {}), cid }]
  })
  if (root.directLinks.items.length !== root.directLinks.shown) return null
  const canonicalLinks = {} as CanonicalLinks
  for (const key of ['ipfs', 'nativeGateway', 'explorer', 'inspector', 'car'] as const) {
    const link = stringValue(canonicalSource[key])
    if (!link) return null
    canonicalLinks[key] = link
  }
  return { version: 1, parsedCid: parsed, root, canonicalLinks }
}

export function formatInspectorStatus(status: string | undefined): string {
  return status ? statuses[status] || status.replaceAll('_', ' ') : 'Not reported'
}

export function formatUnixSeconds(value: number | undefined): string {
  if (value === undefined || !Number.isFinite(value)) return 'Not reported'
  const date = new Date(value * 1000)
  if (Number.isNaN(date.getTime())) return 'Not reported'
  const iso = date.toISOString()
  return `${iso.replace('T', ' ').replace(/\.000Z$/, '')} UTC`
}

export function metadataErrorCode(response: Response, body: unknown): string {
  if (response.status === 429) return '429'
  const error = record(record(body)?.error)?.code
  return typeof error === 'string' ? error : `HTTP ${response.status}`
}
