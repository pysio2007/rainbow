import { CID } from 'multiformats/cid'

export type Provider = { peerId: string; addresses: string[] }
export type ProviderEvent = { event: string; data: Record<string, unknown> }
export type ProviderSummary = { count: number; durationMs: number; cached: boolean; timedOut: boolean }

export class ProviderStreamError extends Error {
  constructor(public code: 'incomplete' | 'invalid', message: string) {
    super(message)
    this.name = 'ProviderStreamError'
  }
}

export function normalizeProviderTarget(input: string): string {
  const value = input.trim()
  let candidate = value
  if (/^https?:\/\//i.test(value)) candidate = new URL(value).pathname
  if (candidate.startsWith('/')) {
    const match = candidate.match(/^\/ipfs\/([^/?#]+)/i)
    if (!match) throw new Error('Expected an IPFS CID or path')
    candidate = match[1]
  } else if (/^ipfs\//i.test(candidate)) candidate = candidate.slice(5).split('/')[0]
  else candidate = candidate.split('/')[0]
  try { return CID.parse(decodeURIComponent(candidate)).toString() } catch { throw new Error('Invalid CID') }
}

export function parseSseEvents(text: string): ProviderEvent[] {
  return text.split(/\r?\n\r?\n/).flatMap((frame) => {
    let event = 'message';
    const data: string[] = []
    for (const line of frame.split(/\r?\n/)) {
      if (line.startsWith(':')) continue
      if (line.startsWith('event:')) event = line.slice(6).trim()
      if (line.startsWith('data:')) data.push(line.slice(5).trim())
    }
    if (!data.length) return []
    try { return [{ event, data: JSON.parse(data.join('\n')) as Record<string, unknown> }] } catch { return [] }
  })
}

export function normalizeProviderEvent(data: Record<string, unknown>): Provider {
  if (typeof data.peerId !== 'string' || !data.peerId.trim() || !Array.isArray(data.addresses)) {
    throw new ProviderStreamError('invalid', 'Provider events must contain peerId and addresses')
  }
  const addresses = [...new Set(data.addresses.filter((address): address is string => typeof address === 'string' && address.trim().length > 0).map((address) => address.trim()))]
  if (!addresses.length) throw new ProviderStreamError('invalid', 'Provider events must contain at least one address')
  return { peerId: data.peerId.trim(), addresses }
}

function normalizeSummary(data: Record<string, unknown>): ProviderSummary {
  if (typeof data.count !== 'number' || typeof data.durationMs !== 'number' || typeof data.cached !== 'boolean' || typeof data.timedOut !== 'boolean') {
    throw new ProviderStreamError('invalid', 'Complete events have an invalid shape')
  }
  return { count: data.count, durationMs: data.durationMs, cached: data.cached, timedOut: data.timedOut }
}

function abortError(signal: AbortSignal): DOMException {
  return new DOMException(signal.reason === 'timeout' ? 'The provider query timed out' : 'The provider query was cancelled', 'AbortError')
}

export async function parseProviderStream(response: Response, onProvider: (provider: Provider) => void, signal?: AbortSignal): Promise<ProviderSummary> {
  if (!response.body) throw new ProviderStreamError('incomplete', 'Provider response ended without a body')
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let summary: ProviderSummary | null = null
  const providers = new Map<string, Provider>()
  const consume = (text: string) => {
    for (const event of parseSseEvents(text)) {
      if (event.event === 'provider') {
        const provider = normalizeProviderEvent(event.data)
        const previous = providers.get(provider.peerId)
        const merged = previous
          ? { peerId: provider.peerId, addresses: [...new Set([...previous.addresses, ...provider.addresses])] }
          : provider
        providers.set(merged.peerId, merged)
        onProvider(merged)
      } else if (event.event === 'complete') {
        summary = normalizeSummary(event.data)
      }
    }
  }
  try {
    while (!summary) {
      if (signal?.aborted) throw abortError(signal)
      const chunk = await reader.read()
      buffer += decoder.decode(chunk.value || new Uint8Array(), { stream: !chunk.done })
      const frames = buffer.split(/\r?\n\r?\n/)
      buffer = frames.pop() || ''
      consume(frames.map((frame) => `${frame}\n\n`).join(''))
      if (chunk.done) {
        consume(buffer)
        break
      }
    }
  } catch (error) {
    if (signal?.aborted) throw abortError(signal)
    throw error
  } finally {
    reader.releaseLock()
  }
  if (!summary) throw new ProviderStreamError('incomplete', 'Provider response ended before complete')
  return summary
}

export function providerUrl(input: string): string {
  return `/network/providers/?cid=${encodeURIComponent(normalizeProviderTarget(input))}`
}

export function filterProviders(providers: Provider[], query: string): Provider[] {
  const needle = query.trim().toLowerCase()
  if (!needle) return providers
  return providers.filter((provider) => provider.peerId.toLowerCase().includes(needle) || provider.addresses.some((address) => address.toLowerCase().includes(needle)))
}

export function retryAfterMs(value: string | null, now = Date.now()): number | null {
  if (!value) return null
  if (/^\d+$/.test(value.trim())) return Number(value) * 1000
  const date = Date.parse(value)
  return Number.isNaN(date) ? null : Math.max(0, date - now)
}
