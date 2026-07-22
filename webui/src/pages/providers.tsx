import { useEffect, useMemo, useState } from 'react'
import { Check, CircleAlert, Clipboard, Network, RefreshCw, Search, Square, X } from 'lucide-react'
import { normalizeProviderTarget, filterProviders, parseProviderStream, retryAfterMs, type Provider } from '@/lib/providers'
import { Header, SiteFooter } from '@/components/layout'
import { initialVersion } from '@/lib/initial'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'

type State = 'idle' | 'loading' | 'complete' | 'error' | 'cancelled'
type Notice = { title: string; detail: string; retryMs?: number }

function CopyButton({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false)
  return <Button variant="ghost" size="icon" title={`Copy ${label}`} aria-label={`Copy ${label}`} onClick={() => { void navigator.clipboard?.writeText(value)?.then(() => { setCopied(true); setTimeout(() => setCopied(false), 1200) }) }}>{copied ? <Check className="size-4" /> : <Clipboard className="size-4" />}</Button>
}

function errorNotice(response: Response): Notice {
  const retryMs = response.status === 429 ? retryAfterMs(response.headers.get('Retry-After')) ?? undefined : undefined
  if (response.status === 429) return { title: 'Discovery is rate limited', detail: retryMs ? `Try again in ${Math.ceil(retryMs / 1000)} seconds.` : 'Please wait before trying again.', retryMs }
  return { title: response.status >= 500 ? 'Provider service unavailable' : 'Provider query failed', detail: `The server returned HTTP ${response.status}.` }
}

export default function Providers() {
  const [input, setInput] = useState(() => new URLSearchParams(window.location.search).get('cid') || '')
  const [cid, setCid] = useState('')
  const [providers, setProviders] = useState<Provider[]>([])
  const [query, setQuery] = useState('')
  const [state, setState] = useState<State>('idle')
  const [notice, setNotice] = useState<Notice | null>(null)
  const [summary, setSummary] = useState<{ count: number; durationMs: number; cached: boolean; timedOut: boolean } | null>(null)
  const [controller, setController] = useState<AbortController | null>(null)

  const visible = useMemo(() => filterProviders(providers, query), [providers, query])
  async function discover(value = input) {
    let normalized: string
    try { normalized = normalizeProviderTarget(value) } catch (error) { setNotice({ title: 'Invalid CID', detail: error instanceof Error ? error.message : 'Enter a CID or IPFS path.' }); setState('error'); return }
    controller?.abort()
    const next = new AbortController(); setController(next); setCid(normalized); setInput(normalized); setProviders([]); setSummary(null); setNotice(null); setState('loading')
    window.history.replaceState({}, '', `/network/providers/?cid=${encodeURIComponent(normalized)}`)
    const timeout = window.setTimeout(() => next.abort('timeout'), 30000)
    try {
      const response = await fetch(`/_rainbow/api/v1/providers?cid=${encodeURIComponent(normalized)}`, { headers: { Accept: 'text/event-stream' }, signal: next.signal })
      if (!response.ok) throw Object.assign(new Error('HTTP error'), { response })
      if (!response.body) throw new Error('unavailable')
      const nextSummary = await parseProviderStream(response, (provider) => setProviders((current) => {
        const index = current.findIndex((item) => item.peerId === provider.peerId)
        if (index < 0) return [...current, provider]
        const updated = [...current]; updated[index] = provider; return updated
      }), next.signal)
      setSummary(nextSummary); setState('complete')
    } catch (error) {
      if (next.signal.aborted) {
        const timedOut = next.signal.reason === 'timeout'
        setState(timedOut ? 'error' : 'cancelled')
        setNotice({ title: timedOut ? 'Provider query timed out' : 'Provider query cancelled', detail: 'Start another query when ready.' })
      }
      else if ((error as { response?: Response }).response) setNotice(errorNotice((error as { response: Response }).response)), setState('error')
      else if ((error as { code?: string }).code === 'incomplete') { setNotice({ title: 'Provider stream ended early', detail: 'The server ended the response before discovery completed.' }); setState('error') }
      else if ((error as { code?: string }).code === 'invalid') { setNotice({ title: 'Invalid provider response', detail: 'The server returned data that does not match the provider contract.' }); setState('error') }
      else { setNotice({ title: 'Provider service unavailable', detail: 'The provider endpoint could not be reached.' }); setState('error') }
    } finally { window.clearTimeout(timeout); setController(null) }
  }
  useEffect(() => { const initial = new URLSearchParams(window.location.search).get('cid'); if (initial) void discover(initial) }, [])

  return <div className="flex min-h-svh flex-col"><Header /><main className="mx-auto w-full max-w-5xl flex-1 px-6 py-10">
    <div className="flex items-center gap-2 text-sm text-muted-foreground"><Network className="size-4" /> Provider discovery</div>
    <h1 className="mt-3 text-3xl font-semibold tracking-tight">Find providers for one CID</h1>
    <p className="mt-3 max-w-2xl text-muted-foreground">Query the gateway network for peers that can provide the content addressed by a single CID.</p>
    <form className="mt-8 flex flex-col gap-2 sm:flex-row" onSubmit={(event) => { event.preventDefault(); void discover() }}><Input value={input} onChange={(event) => setInput(event.target.value)} placeholder="CID, /ipfs path, or gateway URL" aria-label="CID, IPFS path, or gateway URL" /><Button type="submit" disabled={state === 'loading'}>{state === 'loading' ? <RefreshCw className="size-4 animate-spin" /> : <Search className="size-4" />} Query</Button>{state === 'loading' && <Button type="button" variant="outline" onClick={() => controller?.abort()}><Square className="size-4" /> Cancel</Button>}</form>
    {notice && <Alert className="mt-6" variant={state === 'error' ? 'destructive' : 'default'}><CircleAlert className="size-4" /><AlertTitle>{notice.title}</AlertTitle><AlertDescription>{notice.detail}</AlertDescription></Alert>}
    {cid && <div className="mt-8 flex flex-wrap items-center gap-2 text-sm"><span className="text-muted-foreground">CID</span><code className="max-w-full break-all rounded bg-muted px-2 py-1 font-mono text-xs">{cid}</code><CopyButton value={cid} label="CID" />{summary && <Badge variant={summary.timedOut ? 'outline' : 'secondary'}>{summary.count} provider{summary.count === 1 ? '' : 's'} · {summary.durationMs} ms{summary.cached ? ' · cached' : ''}{summary.timedOut ? ' · lookup timed out; results may be partial' : ''}</Badge>}</div>}
    {cid && <Card className="mt-6"><CardHeader className="flex-row items-center justify-between"><CardTitle className="text-base">Providers</CardTitle>{providers.length > 0 && <Input className="w-48" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Filter peer or address" aria-label="Filter providers" />}</CardHeader><CardContent>{visible.length === 0 ? <p className="py-8 text-center text-sm text-muted-foreground">{state === 'loading' ? 'Waiting for provider events…' : 'No providers matched this CID.'}</p> : <div className="divide-y rounded-md border">{visible.map((provider) => <div className="grid gap-3 px-3 py-3 text-sm md:grid-cols-[1fr_1.5fr]" key={provider.peerId}><div className="flex min-w-0 items-start gap-2"><span className="font-medium">Peer ID</span><code className="min-w-0 break-all font-mono text-xs text-muted-foreground">{provider.peerId}</code><CopyButton value={provider.peerId} label="peer ID" /></div><div className="space-y-1">{provider.addresses.map((address) => <div className="flex items-start gap-2" key={address}><code className="min-w-0 break-all font-mono text-xs text-muted-foreground">{address}</code><CopyButton value={address} label="address" /></div>)}</div></div>)}</div>}</CardContent></Card>}
  </main><SiteFooter version={initialVersion()} /></div>
}
