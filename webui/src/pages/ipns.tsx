import { FormEvent, useEffect, useState } from 'react'
import { CircleAlert, ExternalLink, Globe2, RefreshCw } from 'lucide-react'
import { useParams } from 'react-router-dom'
import { Header } from '@/components/layout'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { normalizeIpns, phase4Status, type IpnsResponseV1 } from '@/lib/phase4'

function validPeerId(value: string) { return value.trim().length > 0 && !/[/?#\s]/.test(value.trim()) }

export default function Ipns() {
  const { name = '' } = useParams<{ name: string }>(); const [input, setInput] = useState(() => decodeURIComponent(name)); const [data, setData] = useState<IpnsResponseV1 | null>(null); const [error, setError] = useState(''); const [loading, setLoading] = useState(true)
  async function load(target: string) {
    setLoading(true); setError('')
    try { const response = await fetch(`/_rainbow/api/v1/ipns?name=${encodeURIComponent(target)}`, { headers: { Accept: 'application/json' } }); const body = await response.json().catch(() => null); if (!response.ok) throw new Error(body?.error?.code || `HTTP ${response.status}`); const normalized = normalizeIpns(body); if (!normalized) throw new Error('invalid_response'); setData(normalized) }
    catch (caught) { setError(caught instanceof Error ? caught.message : 'unavailable') } finally { setLoading(false) }
  }
  useEffect(() => { if (validPeerId(name)) void load(name); else { setLoading(false); setError(name ? 'invalid_name' : '') } }, [name])
  function submit(event: FormEvent) { event.preventDefault(); if (validPeerId(input)) window.location.assign(`/inspect/ipns/${encodeURIComponent(input.trim())}`); else setError('invalid_name') }
  return <div className="flex min-h-svh flex-col"><Header /><main className="mx-auto w-full max-w-5xl flex-1 px-6 py-10"><div className="flex items-center gap-2 text-sm text-muted-foreground"><Globe2 className="size-4" /> IPNS Inspector</div><h1 className="mt-3 break-all text-2xl font-semibold tracking-tight">{name || 'Inspect an IPNS peer ID'}</h1><form className="mt-6 flex flex-col gap-2 sm:flex-row" onSubmit={submit}><Input value={input} onChange={(event) => setInput(event.target.value)} placeholder="IPNS peer ID" aria-label="IPNS peer ID" /><Button type="submit">Inspect</Button></form>{error && <Alert className="mt-6" variant="destructive"><CircleAlert className="size-4" /><AlertTitle>{error === 'invalid_name' ? 'Enter a peer ID' : 'IPNS record unavailable'}</AlertTitle><AlertDescription>{error.replaceAll('_', ' ')}. {name && <Button variant="link" className="h-auto p-0" onClick={() => void load(name)}><RefreshCw className="size-3" /> Retry</Button>}</AlertDescription></Alert>}{loading && <div className="mt-6 h-40 animate-pulse rounded-md bg-muted" />}{!loading && !error && data && <div className="mt-6 space-y-6"><Card><CardHeader><CardTitle className="text-base">Record</CardTitle></CardHeader><CardContent><dl className="grid gap-5 sm:grid-cols-2 lg:grid-cols-4"><div><dt className="text-xs text-muted-foreground">Status</dt><dd>{phase4Status(data.record.status)}</dd></div><div><dt className="text-xs text-muted-foreground">Sequence</dt><dd>{data.record.sequence || 'Not reported'}</dd></div><div><dt className="text-xs text-muted-foreground">EOL</dt><dd>{data.record.eol || 'Not reported'}</dd></div><div><dt className="text-xs text-muted-foreground">TTL (nanoseconds)</dt><dd>{data.record.ttlNanos || 'Not reported'}</dd></div></dl></CardContent></Card><Card><CardHeader><CardTitle className="text-base">Reported record target</CardTitle></CardHeader><CardContent>{data.record.target ? <div className="flex flex-wrap items-center justify-between gap-3"><div><p className="text-sm">{phase4Status(data.record.target.status)}</p><code className="mt-1 block break-all font-mono text-xs">{data.record.target.path}</code></div><Button asChild variant="outline" size="sm"><a href={data.record.target.path}><ExternalLink className="size-4" />Open target</a></Button></div> : <p className="text-sm text-muted-foreground">No target was reported.</p>}</CardContent></Card></div>}</main></div>
}
