import { describe, expect, it } from 'vitest'
import { formatInspectorStatus, formatUnixSeconds, normalizeMetadata, type MetadataV1 } from './inspector'

const rootCid = 'bafybeigdyrzt5m7f3x5h2x2h5z7k3x2t5x2h5z7k3x2t5x2h5z7k3x2t5x2'
const childCid = 'bafybeihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku'

const fixture: MetadataV1 = {
  version: 1,
  parsedCid: { canonical: rootCid, version: 1, codec: 'dag-pb', multihash: 'sha2-256', digestLength: 32 },
  root: {
    status: 'fetched', blockBytes: 123, blockVerified: true,
    unixfs: { type: 'directory', mode: 493, mtime: 1784764800 },
    directLinks: { total: 3, shown: 2, truncated: true, items: [{ name: 'README.md', cid: childCid }, { cid: rootCid }] },
  },
  canonicalLinks: { ipfs: `/ipfs/${rootCid}`, nativeGateway: `/ipfs/${rootCid}`, explorer: `/explore/${rootCid}`, inspector: `/inspect/${rootCid}`, car: `/ipfs/${rootCid}?format=car` },
}

describe('metadata v1 normalizer', () => {
  it('preserves the exact structured schema, optional link names, and canonical actions', () => {
    expect(normalizeMetadata(fixture)).toEqual(fixture)
    expect(normalizeMetadata(fixture)?.root.directLinks.items[1]).toEqual({ cid: rootCid })
    expect(normalizeMetadata(fixture)?.root.unixfs).toEqual({ type: 'directory', mode: 493, mtime: 1784764800 })
  })

  it('accepts every root status without inventing a status shape', () => {
    for (const status of ['fetched', 'not_found', 'timeout', 'unsupported_mode', 'block_too_large', 'integrity_error']) {
      const result = normalizeMetadata({ ...fixture, root: { ...fixture.root, status } })
      expect(result?.root.status).toBe(status)
      expect(formatInspectorStatus(status)).toBeDefined()
    }
  })

  it('returns null for unknown or malformed bodies and ignores extra fields', () => {
    expect(normalizeMetadata(null)).toBeNull()
    expect(normalizeMetadata({ ...fixture, version: 2 })).toBeNull()
    expect(normalizeMetadata({ version: 1, parsedCid: {}, root: {}, canonicalLinks: {} })).toBeNull()
    expect(normalizeMetadata({ ...fixture, extra: { ignored: true } } as unknown)).toEqual(fixture)
    expect(normalizeMetadata({ ...fixture, root: { ...fixture.root, unixfs: { ...fixture.root.unixfs, mtime: '2026-07-23T00:00:00Z' } } } as unknown)).toBeNull()
  })

  it('formats finite Unix seconds as explicit UTC and preserves unknown time', () => {
    expect(formatUnixSeconds(1784764800)).toBe('2026-07-23 00:00:00 UTC')
    expect(formatUnixSeconds(undefined)).toBe('Not reported')
    expect(formatUnixSeconds(Number.NaN)).toBe('Not reported')
    expect(formatInspectorStatus(undefined)).toBe('Not reported')
  })
})
