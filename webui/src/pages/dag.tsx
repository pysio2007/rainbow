import { useEffect, useState } from 'react'
import { Check, ChevronRight, CircleAlert, Clipboard, ExternalLink, GitBranch, RefreshCw } from 'lucide-react'
import { useParams } from 'react-router-dom'
import { Header } from '@/components/layout'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { formatBytes } from '@/lib/format'
import { normalizeProviderTarget } from '@/lib/providers'
import { normalizeDag, phase4Status, type DagResponseV1 } from '@/lib/phase4'

function CopyButton({ value }: { value: string }) {
  const [copied, setCopied] = useState(false)
  return <Button type="button" variant="ghost" size="icon-sm" title="Copy CID" aria-label="Copy CID" onClick={() => { void navigator.clipboard?.writeText(value).then(() => { setCopied(true); window.setTimeout(() => setCopied(false), 1000) }) }}>{copied ? <Check className="size-4" /> : <Clipboard className="size-4" />}</Button>
}

export default function Dag() {
  const { cid: rawCid = '' } = useParams<{ cid: string }>()
  const [cid, setCid] = useState(''); const [depth, setDepth] = useState<1 | 2>(1); const [data, setData] = useState<DagResponseV1 | null>(null); const [error, setError] = useState(''); const [loading, setLoading] = useState(true)
  async function load(target: string, requestedDepth: number) {
    setLoading(true); setError('')
    try {
      const response = await fetch(`/_rainbow/api/v1/dag?cid=${encodeURIComponent(target)}&depth=${requestedDepth}`, { headers: { Accept: 'application/json' } })
      const body = await response.json().catch(() => null)
      if (!response.ok) throw new Error(body?.error?.code || `HTTP ${response.status}`)
      const normalized = normalizeDag(body); if (!normalized) throw new Error('invalid_response')
      setData(normalized)
    } catch (caught) { setError(caught instanceof Error ? caught.message : 'unavailable')
    } finally { setLoading(false) }
  }
  useEffect(() => { try { const target = normalizeProviderTarget(rawCid); setCid(target); void load(target, depth) } catch { setError('invalid_cid'); setLoading(false) } }, [rawCid, depth])
  return <div className="flex min-h-svh flex-col"><Header /><main className="mx-auto w-full max-w-5xl flex-1 px-6 py-10"><div className="flex flex-wrap items-start justify-between gap-4"><div><div className="flex items-center gap-2 text-sm text-muted-foreground"><GitBranch className="size-4" /> Bounded DAG view</div><h1 className="mt-2 text-2xl font-semibold tracking-tight">DAG for <code className="break-all font-mono text-lg">{cid || rawCid}</code></h1></div><div className="flex flex-wrap gap-2"><Button asChild variant="outline" size="sm"><a href={`/inspect/${encodeURIComponent(cid)}`}>Root Inspector <ExternalLink className="size-4" /></a></Button><CopyButton value={cid} /></div></div>
    <div className="mt-6 flex flex-wrap items-center gap-2"><span className="text-sm text-muted-foreground">Requested depth</span>{([1, 2] as const).map((value) => <Button key={value} type="button" variant={depth === value ? 'default' : 'outline'} size="sm" onClick={() => setDepth(value)}>{value}</Button>)}<span className="text-xs text-muted-foreground">Only bounded nodes are requested.</span></div>
    {error && <Alert className="mt-6" variant="destructive"><CircleAlert className="size-4" /><AlertTitle>Could not load DAG</AlertTitle><AlertDescription>{error.replaceAll('_', ' ')}. {cid && <Button variant="link" className="h-auto p-0" onClick={() => void load(cid, depth)}><RefreshCw className="size-3" /> Retry</Button>}</AlertDescription></Alert>}
    {loading && <div className="mt-6 space-y-3"><Skeleton className="h-24 w-full" /><Skeleton className="h-16 w-full" /><Skeleton className="h-16 w-full" /></div>}
    {!loading && !error && data && <div className="mt-6 space-y-6"><Card><CardHeader><div className="flex flex-wrap items-center justify-between gap-3"><CardTitle className="text-base">Observation</CardTitle><Badge variant={data.observation.truncated ? 'outline' : 'secondary'}>{phase4Status(data.observation.status)}</Badge></div></CardHeader><CardContent><dl className="grid gap-5 sm:grid-cols-2 lg:grid-cols-4"><div><dt className="text-xs text-muted-foreground">Requested depth</dt><dd>{data.requestedDepth}</dd></div><div><dt className="text-xs text-muted-foreground">Nodes attempted</dt><dd>{data.observation.nodesAttempted}</dd></div><div><dt className="text-xs text-muted-foreground">Parsed bytes</dt><dd>{formatBytes(data.observation.parsedBytes)}</dd></div><div><dt className="text-xs text-muted-foreground">Limits hit</dt><dd>{data.observation.limitsHit.length ? data.observation.limitsHit.join(', ') : 'None reported'}</dd></div></dl>{data.observation.truncated && <p className="mt-4 text-xs text-muted-foreground">The DAG view is truncated by the reported limits; unshown nodes were not inspected.</p>}</CardContent></Card><Card><CardHeader><CardTitle className="text-base">Nodes</CardTitle></CardHeader><CardContent><div className="space-y-2">{data.nodes.map((node) => <div className="min-w-0 border-b py-3 last:border-b-0" key={`${node.cid}-${node.depth}`}><div className="flex min-w-0 flex-wrap items-center gap-2"><Badge variant="outline">depth {node.depth}</Badge><code className="min-w-0 break-all font-mono text-xs">{node.cid}</code><CopyButton value={node.cid} /></div><dl className="mt-3 grid gap-3 text-sm sm:grid-cols-4"><div><dt className="text-xs text-muted-foreground">Codec</dt><dd>{node.codec}</dd></div><div><dt className="text-xs text-muted-foreground">Status</dt><dd>{phase4Status(node.status)}</dd></div><div><dt className="text-xs text-muted-foreground">Block</dt><dd>{formatBytes(node.blockBytes)} · {node.blockVerified ? 'verified' : 'not verified'}</dd></div><div><dt className="text-xs text-muted-foreground">Links</dt><dd>{node.links.shown} of {node.links.total}{node.links.truncated ? ' · truncated' : ''}</dd></div></dl>{node.links.items.length > 0 && <div className="mt-3 space-y-1 border-l pl-3 text-sm">{node.links.items.map((link, index) => <a className="flex min-w-0 items-center gap-2 hover:underline" href={`/inspect/${encodeURIComponent(link.cid)}`} key={`${link.cid}-${index}`}><ChevronRight className="size-4 shrink-0" /><span className="truncate">{link.name || 'Unnamed link'}</span><code className="min-w-0 truncate font-mono text-xs text-muted-foreground">{link.cid}</code></a>)}</div>}</div>)}</div></CardContent></Card></div>}
  </main></div>
}
