import { useEffect, useState } from 'react'
import { Activity, ArrowUpRight, Check, CircleAlert, Clipboard, Compass, ExternalLink, FileCheck2, RefreshCw } from 'lucide-react'
import { useParams } from 'react-router-dom'
import { Header } from '@/components/layout'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { formatBytes } from '@/lib/format'
import { formatInspectorStatus, formatUnixSeconds, metadataErrorCode, normalizeMetadata, type MetadataV1 } from '@/lib/inspector'
import { normalizeProviderTarget } from '@/lib/providers'

function CopyButton({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false)
  async function copy() {
    await navigator.clipboard?.writeText(value)
    setCopied(true); window.setTimeout(() => setCopied(false), 1200)
  }
  return <Button type="button" variant="ghost" size="icon-sm" title={`Copy ${label}`} aria-label={`Copy ${label}`} onClick={() => void copy()}>{copied ? <Check className="size-4" /> : <Clipboard className="size-4" />}</Button>
}

function Value({ value, mono = false }: { value: unknown; mono?: boolean }) {
  return <span className={mono ? 'break-all font-mono text-xs' : ''}>{value === undefined ? 'Not reported' : String(value)}</span>
}

function Field({ label, value, mono = false }: { label: string; value: unknown; mono?: boolean }) {
  return <div className="min-w-0 space-y-1"><dt className="text-xs text-muted-foreground">{label}</dt><dd><Value value={value} mono={mono} /></dd></div>
}

function errorTitle(code: string) {
  if (code === 'invalid_cid') return 'Invalid CID'
  if (code === 'invalid_response') return 'Metadata response was not valid v1 data'
  if (code === 'not_found') return 'CID was not found'
  if (code === 'timeout') return 'Metadata request timed out'
  if (code === 'unsupported_mode') return 'Metadata is not supported by this gateway'
  if (code === 'block_too_large') return 'Root block is too large'
  if (code === 'integrity_error') return 'Root integrity check failed'
  if (code === '429') return 'Metadata request is rate limited'
  return 'Could not inspect this CID'
}

export default function Inspector() {
  const { cid: rawCid = '' } = useParams<{ cid: string }>()
  const [cid, setCid] = useState('')
  const [metadata, setMetadata] = useState<MetadataV1 | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  async function load(target: string) {
    setLoading(true); setError(''); setMetadata(null)
    try {
      const response = await fetch(`/_rainbow/api/v1/metadata?cid=${encodeURIComponent(target)}`, { headers: { Accept: 'application/json' } })
      const body = await response.json().catch(() => null)
      if (!response.ok) throw new Error(metadataErrorCode(response, body))
      const normalized = normalizeMetadata(body)
      if (!normalized) throw new Error('invalid_response')
      setMetadata(normalized)
    } catch (caught) { setError(caught instanceof Error ? caught.message : 'unavailable')
    } finally { setLoading(false) }
  }

  useEffect(() => {
    try { const target = normalizeProviderTarget(rawCid); setCid(target); void load(target) }
    catch { setCid(''); setError('invalid_cid'); setLoading(false) }
  }, [rawCid])

  const root = metadata?.root
  const parsed = metadata?.parsedCid
  const actions = metadata?.canonicalLinks
  const links = root?.directLinks.items || []
  return <div className="flex min-h-svh flex-col"><Header /><main className="mx-auto w-full max-w-5xl flex-1 px-6 py-10">
    <div className="flex flex-wrap items-start justify-between gap-4"><div className="min-w-0"><div className="text-sm text-muted-foreground">CID Inspector</div><h1 className="mt-2 break-all font-mono text-2xl font-semibold tracking-tight">{parsed?.canonical || cid || rawCid}</h1><div className="mt-2 flex items-center gap-1 text-sm text-muted-foreground">Copy CID {cid && <CopyButton value={cid} label="CID" />}</div></div>{actions && <div className="flex flex-wrap gap-2"><Button asChild variant="outline" size="sm"><a href={actions.nativeGateway}>Open content <ArrowUpRight className="size-4" /></a></Button><Button asChild variant="outline" size="sm"><a href={actions.explorer}><Compass className="size-4" />Explore</a></Button><Button asChild variant="outline" size="sm"><a href={`/inspect/${encodeURIComponent(cid)}/dag`}>View DAG</a></Button><Button asChild variant="outline" size="sm"><a href={`/inspect/${encodeURIComponent(cid)}/car`}>Check CAR</a></Button><Button asChild variant="outline" size="sm"><a href={`/retrieval/${encodeURIComponent(cid)}`}><Activity className="size-4" />Observe retrieval</a></Button></div>}</div>
    {error && <Alert className="mt-6" variant="destructive"><CircleAlert className="size-4" /><AlertTitle>{errorTitle(error)}</AlertTitle><AlertDescription>{error === '429' ? 'Please wait before retrying.' : error.replaceAll('_', ' ')}. {cid && <Button variant="link" className="h-auto p-0" onClick={() => void load(cid)}><RefreshCw className="size-3" /> Retry</Button>}</AlertDescription></Alert>}
    {loading && <div className="mt-6 space-y-4"><Skeleton className="h-28 w-full" /><Skeleton className="h-48 w-full" /><Skeleton className="h-40 w-full" /></div>}
    {!loading && !error && metadata && root && parsed && <div className="mt-6 space-y-6">
      <Card><CardHeader><CardTitle className="flex items-center gap-2 text-base"><FileCheck2 className="size-4" />Root metadata</CardTitle></CardHeader><CardContent><dl className="grid gap-5 sm:grid-cols-2 lg:grid-cols-3"><Field label="Status" value={formatInspectorStatus(root.status)} /><Field label="Block bytes" value={formatBytes(root.blockBytes)} /><Field label="Block verified" value={root.blockVerified ? 'Yes' : 'No'} /><Field label="Direct links" value={`${root.directLinks.shown} of ${root.directLinks.total}${root.directLinks.truncated ? ' · truncated' : ''}`} /><Field label="CID codec" value={parsed.codec} mono /><Field label="CID version" value={parsed.version} /></dl><p className="mt-4 text-xs text-muted-foreground">Block verification covers the fetched root block only; it does not prove DAG completeness.</p></CardContent></Card>
      <Card><CardHeader><CardTitle className="text-base">Parsed CID</CardTitle></CardHeader><CardContent><dl className="grid gap-5 sm:grid-cols-2 lg:grid-cols-4"><Field label="Canonical" value={parsed.canonical} mono /><Field label="Multihash" value={parsed.multihash} mono /><Field label="Digest length" value={`${parsed.digestLength} bytes`} /><Field label="Codec" value={parsed.codec} mono /></dl></CardContent></Card>
      <Card><CardHeader><div className="flex flex-wrap items-center justify-between gap-3"><CardTitle className="text-base">Direct links</CardTitle><Badge variant="outline">{root.directLinks.shown} of {root.directLinks.total}</Badge></div></CardHeader><CardContent>{links.length === 0 ? <p className="text-sm text-muted-foreground">No direct links were reported.</p> : <div className="divide-y rounded-md border">{links.map((link, index) => <div className="flex min-w-0 items-center justify-between gap-3 px-3 py-2.5 text-sm" key={`${link.cid}-${index}`}><div className="min-w-0"><div className="truncate">{link.name || 'Unnamed link'}</div><code className="block truncate font-mono text-xs text-muted-foreground">{link.cid}</code></div><div className="flex shrink-0 items-center gap-1"><CopyButton value={link.cid} label="link CID" /><Button asChild variant="ghost" size="icon-sm" title="Inspect link CID" aria-label="Inspect link CID"><a href={`/inspect/${encodeURIComponent(link.cid)}`}><ExternalLink className="size-4" /></a></Button></div></div>)}</div>}{root.directLinks.truncated && <p className="mt-3 text-xs text-muted-foreground">Only the reported direct links are shown.</p>}</CardContent></Card>
      <Card><CardHeader><CardTitle className="text-base">UnixFS metadata</CardTitle></CardHeader><CardContent>{root.unixfs ? <dl className="grid gap-5 sm:grid-cols-2 lg:grid-cols-4"><Field label="Type" value={root.unixfs.type} /><Field label="Declared file size" value={root.unixfs.declaredFileSize === undefined ? undefined : formatBytes(root.unixfs.declaredFileSize)} /><Field label="Mode" value={root.unixfs.mode} /><Field label="Modified (UTC)" value={formatUnixSeconds(root.unixfs.mtime)} /></dl> : <p className="text-sm text-muted-foreground">Not reported.</p>}</CardContent></Card>
      {actions && <div className="flex flex-wrap gap-2"><Button asChild variant="outline" size="sm"><a href={actions.car}><ExternalLink className="size-4" />Open CAR response</a></Button><Badge variant="secondary" className="h-8 px-3">CAR response is not verified here</Badge></div>}
    </div>}
  </main></div>
}
