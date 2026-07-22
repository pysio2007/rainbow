import { describe, expect, it } from 'vitest'
import { filterProviders, normalizeProviderTarget, parseProviderStream, parseSseEvents, providerUrl, retryAfterMs, type Provider } from './providers'

describe('provider discovery helpers', () => {
  it('normalizes a CID, IPFS path, and gateway URL to the same CID', () => {
    const cid = 'QmYwAPJzv5CZsnAzt8auVZRnZQ5J7cV7Wc6YzS4hGJ5a6H'
    expect(normalizeProviderTarget(cid)).toBe(cid)
    expect(normalizeProviderTarget(`/ipfs/${cid}/notes.txt`)).toBe(cid)
    expect(normalizeProviderTarget(`https://gateway.example/ipfs/${cid}/notes.txt`)).toBe(cid)
  })

  it('parses split SSE frames and ignores keep-alive comments', () => {
    const events = parseSseEvents('event: provider\ndata: {"peerId":"p1","addresses":["/ip4/127.0.0.1"]}\n\n: ping\n\nevent: complete\ndata: {"count":1,"durationMs":12,"cached":false,"timedOut":false}\n\n')
    expect(events).toEqual([
      { event: 'provider', data: { peerId: 'p1', addresses: ['/ip4/127.0.0.1'] } },
      { event: 'complete', data: { count: 1, durationMs: 12, cached: false, timedOut: false } },
    ])
  })

  it('validates the provider contract and merges duplicate peers', async () => {
    const response = new Response(
      new ReadableStream({
        start(controller) {
          controller.enqueue(new TextEncoder().encode('event: provider\ndata: {"peerId":"p1","addresses":["/ip4/1","/ip4/1"]}\n\n'))
          controller.enqueue(new TextEncoder().encode('event: provider\ndata: {"peerId":"p1","addresses":["/dns4/relay"]}\n\nevent: complete\ndata: {"count":1,"durationMs":12,"cached":false,"timedOut":false}\n\n'))
          controller.close()
        },
      }),
      { headers: { 'Content-Type': 'text/event-stream' } },
    )
    const providers = new Map<string, Provider>()
    const summary = await parseProviderStream(response, (provider) => providers.set(provider.peerId, provider))
    expect([...providers.values()]).toEqual([{ peerId: 'p1', addresses: ['/ip4/1', '/dns4/relay'] }])
    expect(summary.timedOut).toBe(false)
  })

  it('reports an incomplete stream when EOF has no complete event', async () => {
    const response = new Response('event: provider\ndata: {"peerId":"p1","addresses":["/ip4/1"]}\n\n')
    const providers = new Map<string, Provider>()
    await expect(parseProviderStream(response, (provider) => providers.set(provider.peerId, provider))).rejects.toMatchObject({ code: 'incomplete' })
    expect([...providers.values()]).toEqual([{ peerId: 'p1', addresses: ['/ip4/1'] }])
  })

  it('preserves aborts as cancellation and keeps timed-out completion visible', async () => {
    const controller = new AbortController()
    controller.abort('cancelled')
    await expect(parseProviderStream(new Response(''), () => {}, controller.signal)).rejects.toMatchObject({ name: 'AbortError' })

    const response = new Response('event: complete\ndata: {"count":1,"durationMs":30,"cached":false,"timedOut":true}\n\n')
    await expect(parseProviderStream(response, () => {})).resolves.toEqual({ count: 1, durationMs: 30, cached: false, timedOut: true })
  })

  it('filters peer IDs and addresses case-insensitively', () => {
    const rows = [{ peerId: 'PeerOne', addresses: ['/dns4/relay.example/tcp/4001'] }, { peerId: 'PeerTwo', addresses: ['/ip4/10.0.0.2'] }]
    expect(filterProviders(rows, 'relay')).toEqual([rows[0]])
    expect(filterProviders(rows, 'peerTWO')).toEqual([rows[1]])
  })

  it('reads Retry-After as seconds or an HTTP date', () => {
    expect(retryAfterMs('3', 0)).toBe(3000)
    expect(retryAfterMs(new Date(5000).toUTCString(), 0)).toBe(5000)
  })

  it('creates a direct MPA provider URL from a CID or IPFS path', () => {
    const cid = 'QmYwAPJzv5CZsnAzt8auVZRnZQ5J7cV7Wc6YzS4hGJ5a6H'
    expect(providerUrl(`/ipfs/${cid}/notes.txt`)).toBe(`/network/providers/?cid=${encodeURIComponent(cid)}`)
  })
})
