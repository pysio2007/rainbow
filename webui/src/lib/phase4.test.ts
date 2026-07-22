import { describe, expect, it } from 'vitest'
import { normalizeCarCheck, normalizeDag, normalizeIpns, type CarCheckV1, type DagResponseV1, type IpnsResponseV1 } from './phase4'

const cid = 'bafybeigdyrzt5m7f3x5h2x2h5z7k3x2t5x2h5z7k3x2t5x2h5z7k3x2t5x2'
const fixture: DagResponseV1 = {
  version: 1, root: cid, requestedDepth: 2,
  observation: { status: 'fetched', truncated: true, limitsHit: ['node_limit'], nodesAttempted: 4, parsedBytes: 512 },
  nodes: [{ cid, depth: 0, codec: 'dag-pb', status: 'fetched', blockBytes: 123, blockVerified: true, links: { total: 2, shown: 1, truncated: true, items: [{ cid, name: 'child' }] } }],
}

describe('Phase 4 API normalizers', () => {
  it('preserves bounded DAG observations and optional link names', () => {
    expect(normalizeDag(fixture)).toEqual(fixture)
    expect(normalizeDag(fixture)?.nodes[0].links.items[0].name).toBe('child')
    expect(normalizeDag({ ...fixture, version: 2 })).toBeNull()
  })

  it('rejects malformed DAG data rather than filling invented fields', () => {
    expect(normalizeDag({ ...fixture, nodes: [{ ...fixture.nodes[0], links: { ...fixture.nodes[0].links, items: [{ name: 4, cid }] } }] })).toBeNull()
  })

  it('preserves the current DAG, IPNS, and CAR status strings', () => {
    for (const status of ['fetched', 'unsupported_mode', 'error', 'block_too_large', 'integrity_error', 'limit']) {
      expect(normalizeDag({ ...fixture, observation: { ...fixture.observation, status }, nodes: [{ ...fixture.nodes[0], status }] })?.observation.status).toBe(status)
    }
    expect(normalizeIpns({ version: 1, name: '12D3KooWExample', record: { status: 'validated', target: { status: 'valid', path: `/ipfs/${cid}` } } })).not.toBeNull()
    for (const status of ['invalid_record', 'unavailable']) expect(normalizeIpns({ version: 1, name: '12D3KooWExample', record: { status } })).not.toBeNull()
    for (const status of ['limit', 'unsupported_mode', 'unavailable', 'invalid_car', 'root_mismatch', 'root_missing', 'duplicate_root', 'unexpected_block_count', 'verified']) {
      expect(normalizeCarCheck({ version: 1, cid, verification: { status, scope: 'block' } })?.verification.status).toBe(status)
    }
  })

  it('accepts IPNS record states and optional target fields only', () => {
    const response: IpnsResponseV1 = { version: 1, name: '12D3KooWExample', record: { status: 'resolved', target: { status: 'fetched', path: `/ipfs/${cid}` }, sequence: '4', eol: '2026-07-23T00:00:00Z', ttlNanos: '60000000000' } }
    expect(normalizeIpns(response)).toEqual(response)
    expect(normalizeIpns({ ...response, record: { status: 'not_found' } })).toEqual({ ...response, record: { status: 'not_found' } })
    expect(normalizeIpns({ ...response, version: 2 })).toBeNull()
  })

  it('normalizes CAR verification statuses and rejects malformed bodies', () => {
    const response: CarCheckV1 = { version: 1, cid, verification: { status: 'verified', scope: 'block', carVersion: 1, declaredRootMatches: true, rootBlockPresent: true, rootBlockVerified: true, blocksRead: 1, bytesRead: 123 } }
    expect(normalizeCarCheck(response)).toEqual(response)
    expect(normalizeCarCheck({ ...response, verification: { status: 'incomplete', scope: 'block' } })).toEqual({ ...response, verification: { status: 'incomplete', scope: 'block' } })
    const unexpected = { ...response, verification: { ...response.verification, status: 'unexpected_block_count', rootBlockVerified: false, blocksRead: 2 } }
    expect(normalizeCarCheck(unexpected)?.verification).toMatchObject({ status: 'unexpected_block_count', scope: 'block', rootBlockVerified: false, blocksRead: 2 })
    expect(normalizeCarCheck({ ...response, verification: { ...response.verification, bytesRead: '512' } })).toBeNull()
    expect(normalizeCarCheck({ ...response, verification: { ...response.verification, carVersion: '1' } })).toBeNull()
  })
})
