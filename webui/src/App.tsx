import { Fragment, useEffect, useState } from 'react'
import type { FormEvent, ReactNode } from 'react'
import { ArrowUpRight, ChevronRight, CircleAlert, Compass, ExternalLink, FileAudio, FileCode, FileImage, FileText, FileVideo, Folder, Search, Sparkles } from 'lucide-react'
import { Link, Navigate, Route, Routes, useLocation, useNavigate, useParams } from 'react-router-dom'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import type { FilePreviewType } from '@/lib/file-preview'
import { isTextContentType, resolveFilePreviewType } from '@/lib/file-preview'
import { resolveMarkdownUrl } from '@/lib/markdown'
import { directoryApiPath, explorerPathToIpfsPath, gatewayPath, ipfsPathToExplorerPath, normalizeInput } from '@/lib/normalizer'
import { parseVersionText } from '@/lib/version'
import { formatBytes, formatCount } from '@/lib/format'
import type { GatewayStats } from '@/lib/stats'
import { fetchStats } from '@/lib/stats'
import { ModeToggle } from '@/components/mode-toggle'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Breadcrumb, BreadcrumbItem, BreadcrumbLink, BreadcrumbList, BreadcrumbPage, BreadcrumbSeparator } from '@/components/ui/breadcrumb'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Skeleton } from '@/components/ui/skeleton'

type Directory = { version: number; path: string; resolvedCid: string; entries: { name: string; cid: string }[] }
const sampleCid = 'QmYwAPJzv5CZsnAzt8auVZRnZQ5J7cV7Wc6YzS4hGJ5a6H'

function Header() {
  return (
    <header className="border-b">
      <div className="mx-auto flex max-w-5xl items-center justify-between px-6 py-4">
        <Link to="/" className="text-lg font-semibold tracking-tight">suse.cc</Link>
        <nav className="flex items-center gap-1">
          <Button asChild variant="ghost" size="sm">
            <Link to="/explore"><Compass className="size-4" />Explorer</Link>
          </Button>
          <Button asChild variant="ghost" size="sm">
            <a href="/version">Status<ArrowUpRight className="size-4" /></a>
          </Button>
          <ModeToggle />
        </nav>
      </div>
    </header>
  )
}

function SiteFooter({ version }: { version: string }) {
  return (
    <footer className="border-t">
      <div className="mx-auto flex max-w-5xl flex-wrap items-center justify-between gap-2 px-6 py-6 text-sm text-muted-foreground">
        <span>suse.cc · public IPFS gateway</span>
        {version && <span className="font-mono text-xs">{version.trim()}</span>}
      </div>
    </footer>
  )
}

function SearchBox({ initial = '' }: { initial?: string }) {
  const [value, setValue] = useState(initial)
  const [error, setError] = useState('')
  const navigate = useNavigate()
  let target = null
  try { target = value.trim() ? normalizeInput(value) : null } catch { target = null }

  function submit(event: FormEvent) {
    event.preventDefault()
    try {
      const result = normalizeInput(value)
      setError('')
      window.location.assign(gatewayPath(result.path))
    } catch (caught) { setError(caught instanceof Error ? caught.message : 'Invalid input') }
  }

  return (
    <form onSubmit={submit} className="w-full max-w-2xl space-y-3">
      <div className="flex flex-col gap-2 sm:flex-row">
        <div className="relative flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
          <Input
            value={value}
            onChange={(event) => { setValue(event.target.value); setError('') }}
            placeholder="Paste a CID, IPFS path, or gateway URL"
            aria-label="Content address"
            autoComplete="off"
            className="pl-9"
          />
        </div>
        <div className="flex gap-2">
          <Button type="submit">Open <ArrowUpRight className="size-4" /></Button>
          {target?.canExplore && (
            <Button type="button" variant="secondary" onClick={() => navigate(ipfsPathToExplorerPath(target.path))}>
              Explore <Compass className="size-4" />
            </Button>
          )}
        </div>
      </div>
      {error && <p role="alert" className="flex items-center gap-1.5 text-sm text-destructive"><CircleAlert className="size-4" />{error}</p>}
    </form>
  )
}

const features = [
  { n: '01', t: 'Open by CID or path' },
  { n: '02', t: 'Browse immutable directories' },
  { n: '03', t: 'Direct URL access' },
]

function Home() {
  const [version, setVersion] = useState('')
  const [stats, setStats] = useState<GatewayStats | null>(null)
  useEffect(() => {
    fetch('/version').then(async (response) => response.ok ? parseVersionText(response.headers.get('content-type'), await response.text()) : '').then(setVersion).catch(() => undefined)
    fetchStats().then(setStats).catch(() => undefined)
  }, [])

  return (
    <div className="flex min-h-svh flex-col">
      <Header />
      <main className="mx-auto w-full max-w-5xl flex-1 px-6 py-16">
        <div className="flex items-center gap-2 text-sm text-muted-foreground"><Sparkles className="size-4" /> Public IPFS gateway</div>
        <h1 className="mt-4 max-w-2xl text-4xl font-semibold tracking-tight sm:text-5xl">Access IPFS content, instantly.</h1>
        <p className="mt-4 max-w-xl text-muted-foreground">Paste a CID, an IPFS path, or a gateway URL. The gateway takes you straight to the content you name.</p>
        <div className="mt-8"><SearchBox /></div>
        <div className="mt-4 flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
          <span>Try</span>
          <Button variant="link" className="h-auto p-0 font-mono" onClick={() => window.location.assign(`/ipfs/${sampleCid}`)}>{sampleCid.slice(0, 12)}…</Button>
        </div>

        {stats && (
          <div className="mt-10 grid max-w-md gap-4 sm:grid-cols-2">
            <Card>
              <CardHeader>
                <CardDescription>Files served</CardDescription>
                <CardTitle className="text-3xl tabular-nums">{formatCount(stats.filesProcessed)}</CardTitle>
              </CardHeader>
            </Card>
            <Card>
              <CardHeader>
                <CardDescription>Origin traffic</CardDescription>
                <CardTitle className="text-3xl tabular-nums">{formatBytes(stats.originBytes)}</CardTitle>
              </CardHeader>
            </Card>
          </div>
        )}

        <div className="mt-16 grid gap-4 sm:grid-cols-3">
          {features.map((feature) => (
            <Card key={feature.n}>
              <CardHeader>
                <CardDescription className="font-mono">{feature.n}</CardDescription>
                <CardTitle className="text-base font-medium">{feature.t}</CardTitle>
              </CardHeader>
            </Card>
          ))}
        </div>
      </main>
      <SiteFooter version={version} />
    </div>
  )
}

const previewTextLimit = 1 << 20 // 1 MiB
const typeNames: Record<FilePreviewType, string> = { image: 'Image', audio: 'Audio', video: 'Video', pdf: 'PDF', markdown: 'Markdown', text: 'Text', unknown: '' }

type FileMeta = { size: number | null; contentType: string | null }

function useFileMeta(url: string): FileMeta | null {
  const [meta, setMeta] = useState<FileMeta | null>(null)
  useEffect(() => {
    let active = true
    const controller = new AbortController()
    setMeta(null)
    fetch(url, { method: 'HEAD', signal: controller.signal })
      .then((response) => {
        if (!active) return
        if (!response.ok) { setMeta({ size: null, contentType: null }); return }
        const length = response.headers.get('content-length')
        const size = length && /^\d+$/.test(length) ? Number(length) : null
        setMeta({ size, contentType: response.headers.get('content-type') })
      })
      .catch(() => { if (active) setMeta({ size: null, contentType: null }) })
    return () => { active = false; controller.abort() }
  }, [url])
  return meta
}

type TextState = 'loading' | 'ready' | 'too-large' | 'error'

function useTextContent(url: string, enabled: boolean, size: number | null): { body: string; state: TextState } {
  const [body, setBody] = useState('')
  const [state, setState] = useState<TextState>('loading')
  useEffect(() => {
    if (!enabled) return
    if (size != null && size > previewTextLimit) { setState('too-large'); return }
    let active = true
    const controller = new AbortController()
    setState('loading')
    fetch(url, { signal: controller.signal })
      .then(async (response) => {
        if (!response.ok) throw new Error('fetch failed')
        const text = await response.text()
        if (active) { setBody(text); setState('ready') }
      })
      .catch((error) => { if (active && error?.name !== 'AbortError') setState('error') })
    return () => { active = false; controller.abort() }
  }, [url, enabled, size])
  return { body, state }
}

function PreviewNotice({ title, description, action }: { title: string; description: string; action: ReactNode }) {
  return (
    <Alert>
      <FileText className="size-4" />
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription>{description}</AlertDescription>
      <div className="mt-3">{action}</div>
    </Alert>
  )
}

function FilePreview({ path, type }: { path: string; type: FilePreviewType }) {
  const url = gatewayPath(path)
  const name = decodeURIComponent(path.split('/').pop() || 'IPFS file')
  const meta = useFileMeta(url)
  const settled = meta !== null
  const effectiveType: FilePreviewType = type === 'unknown' && settled && isTextContentType(meta.contentType) ? 'text' : type
  const isTextual = effectiveType === 'markdown' || effectiveType === 'text'
  const { body, state } = useTextContent(url, isTextual && settled, meta?.size ?? null)

  const icon = effectiveType === 'image' ? <FileImage className="size-4" />
    : effectiveType === 'audio' ? <FileAudio className="size-4" />
    : effectiveType === 'video' ? <FileVideo className="size-4" />
    : effectiveType === 'text' ? <FileCode className="size-4" />
    : <FileText className="size-4" />
  const label = effectiveType === 'unknown' ? 'Preview unavailable' : `${typeNames[effectiveType]} preview`

  const nativeButton = (
    <Button asChild variant="outline" size="sm">
      <a href={url}>Open native gateway <ArrowUpRight className="size-4" /></a>
    </Button>
  )

  function renderBody() {
    if (effectiveType === 'image') return <img src={url} alt={name} className="mx-auto max-h-[460px] w-auto rounded-md" />
    if (effectiveType === 'audio') return <audio controls preload="metadata" src={url} aria-label={`Play ${name}`} className="w-full">Your browser cannot play this audio file.</audio>
    if (effectiveType === 'video') return <video controls preload="metadata" src={url} aria-label={`Play ${name}`} className="mx-auto max-h-[460px] w-full rounded-md">Your browser cannot play this video file.</video>
    if (effectiveType === 'pdf') return <iframe src={url} title={`Preview of ${name}`} className="h-[520px] w-full rounded-md border" />
    if (isTextual) {
      if (!settled || state === 'loading') return <Skeleton className="h-64 w-full" />
      if (state === 'too-large') return <PreviewNotice title="This file is too large to preview" description="Open the native gateway to view or download it." action={nativeButton} />
      if (state === 'error') return <PreviewNotice title="This file could not be loaded" description="Open the native gateway to view or download it." action={nativeButton} />
      if (effectiveType === 'markdown') {
        return (
          <ScrollArea className="max-h-[560px] w-full">
            <article className="prose prose-sm dark:prose-invert max-w-none">
              <ReactMarkdown remarkPlugins={[remarkGfm]} urlTransform={(value) => resolveMarkdownUrl(path, value)}>{body}</ReactMarkdown>
            </article>
          </ScrollArea>
        )
      }
      return (
        <ScrollArea className="max-h-[560px] w-full rounded-md border bg-muted/40">
          <pre className="p-4 font-mono text-xs leading-relaxed">{body}</pre>
        </ScrollArea>
      )
    }
    if (!settled) return <Skeleton className="h-40 w-full" />
    return <PreviewNotice title="This file cannot be previewed here" description="Open the native gateway to view or download this content." action={nativeButton} />
  }

  const mediaType = effectiveType === 'image' || effectiveType === 'audio' || effectiveType === 'video' || effectiveType === 'pdf'
  const showFallback = mediaType || (isTextual && state === 'ready')

  return (
    <Card>
      <CardHeader>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <CardTitle className="flex items-center gap-2 text-base">{icon}{label}</CardTitle>
          <div className="flex items-center gap-2">
            {meta?.size != null && <Badge variant="secondary" className="tabular-nums">{formatBytes(meta.size)}</Badge>}
            <code className="max-w-[240px] truncate rounded bg-muted px-2 py-1 font-mono text-xs" title={name}>{name}</code>
          </div>
        </div>
      </CardHeader>
      <CardContent>{renderBody()}</CardContent>
      {showFallback && (
        <CardFooter className="justify-between gap-3 text-sm text-muted-foreground">
          <span>Having trouble loading it?</span>
          {nativeButton}
        </CardFooter>
      )}
    </Card>
  )
}

function Explorer() {
  const params = useParams<{ '*': string }>()
  const location = useLocation()
  const explorerPath = location.pathname
  let rawPath = ''
  let pathError = ''
  if (params['*']) {
    try { rawPath = explorerPathToIpfsPath(explorerPath) } catch { pathError = 'This is not a valid immutable path.' }
  }
  const [directory, setDirectory] = useState<Directory | null>(null)
  const [error, setError] = useState(pathError)
  const [previewType, setPreviewType] = useState<FilePreviewType | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    if (!rawPath) { setLoading(false); return }
    let active = true
    setLoading(true); setError(''); setDirectory(null); setPreviewType(null)
    let apiPath: string
    try { apiPath = directoryApiPath(rawPath) } catch { setError('This is not a valid immutable path.'); setLoading(false); return }
    fetch(`/_rainbow/api/v1/directory?path=${encodeURIComponent(apiPath)}`, { headers: { Accept: 'application/json' } })
      .then(async (response) => { if (!response.ok) { const code = (await response.json().catch(() => null))?.error?.code || 'Directory unavailable'; throw new Error(code) }; return response.json() })
      .then((data: Directory) => { if (active) setDirectory(data) })
      .catch((caught) => { if (!active) return; if (caught instanceof Error && caught.message === 'not_directory') setPreviewType(resolveFilePreviewType(rawPath)); else setError(caught instanceof Error ? caught.message : 'Directory unavailable') })
      .finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [rawPath])

  if (!rawPath) {
    return (
      <div className="flex min-h-svh flex-col">
        <Header />
        <main className="mx-auto w-full max-w-5xl flex-1 px-6 py-16">
          <div className="flex items-center gap-2 text-sm text-muted-foreground"><Compass className="size-4" /> Immutable explorer</div>
          <h1 className="mt-4 text-3xl font-semibold tracking-tight">Explore an immutable directory</h1>
          <p className="mt-3 max-w-xl text-muted-foreground">Start with an IPFS CID. Directory entries are fetched from this gateway.</p>
          <div className="mt-8"><SearchBox /></div>
        </main>
      </div>
    )
  }

  const breadcrumb = rawPath.split('/').filter(Boolean).slice(1)
  return (
    <div className="flex min-h-svh flex-col">
      <Header />
      <main className="mx-auto w-full max-w-5xl flex-1 px-6 py-10">
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div>
            <div className="text-sm text-muted-foreground">Immutable explorer</div>
            <h1 className="text-2xl font-semibold tracking-tight">Directory</h1>
          </div>
          <Button asChild variant="outline" size="sm">
            <a href={gatewayPath(rawPath)}>Open native gateway <ExternalLink className="size-4" /></a>
          </Button>
        </div>

        <Breadcrumb className="mt-6">
          <BreadcrumbList>
            <BreadcrumbItem><BreadcrumbLink asChild><Link to="/explore">Explorer</Link></BreadcrumbLink></BreadcrumbItem>
            {breadcrumb.map((part, index) => {
              const label = index === 0 ? `${part.slice(0, 12)}…` : part
              const last = index === breadcrumb.length - 1
              return (
                <Fragment key={`${part}-${index}`}>
                  <BreadcrumbSeparator />
                  <BreadcrumbItem>
                    {last
                      ? <BreadcrumbPage className="max-w-[200px] truncate">{label}</BreadcrumbPage>
                      : <BreadcrumbLink asChild><Link to={ipfsPathToExplorerPath(`/ipfs/${breadcrumb.slice(0, index + 1).join('/')}`)}>{label}</Link></BreadcrumbLink>}
                  </BreadcrumbItem>
                </Fragment>
              )
            })}
          </BreadcrumbList>
        </Breadcrumb>

        <div className="mt-6 space-y-6">
          {loading && (
            <div className="space-y-3">
              <Skeleton className="h-11 w-full" />
              <Skeleton className="h-11 w-full" />
              <Skeleton className="h-11 w-full" />
            </div>
          )}
          {error && (
            <Alert variant="destructive">
              <CircleAlert className="size-4" />
              <AlertTitle>Could not open this directory</AlertTitle>
              <AlertDescription>
                {error.replaceAll('_', ' ')}. <a className="underline underline-offset-4" href={gatewayPath(rawPath)}>Open it in the native gateway</a>.
              </AlertDescription>
            </Alert>
          )}
          {previewType && <FilePreview path={rawPath} type={previewType} />}
          {directory && (
            <Card>
              <CardHeader>
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <CardTitle className="text-base tabular-nums">{directory.entries.length} {directory.entries.length === 1 ? 'entry' : 'entries'}</CardTitle>
                  <code className="max-w-[280px] truncate rounded bg-muted px-2 py-1 font-mono text-xs">{directory.resolvedCid}</code>
                </div>
              </CardHeader>
              <CardContent>
                {directory.entries.length === 0
                  ? <p className="text-sm text-muted-foreground">This directory is empty.</p>
                  : (
                    <div className="divide-y overflow-hidden rounded-md border">
                      {directory.entries.map((entry) => (
                        <Link
                          key={`${entry.name}-${entry.cid}`}
                          to={ipfsPathToExplorerPath(`${rawPath}/${entry.name}`)}
                          className="flex items-center justify-between gap-3 px-3 py-2.5 text-sm transition-colors hover:bg-accent"
                        >
                          <span className="flex min-w-0 items-center gap-2"><Folder className="size-4 shrink-0 text-muted-foreground" /><span className="truncate">{entry.name}</span></span>
                          <span className="flex items-center gap-2 text-muted-foreground">
                            <code className="hidden max-w-[220px] truncate font-mono text-xs sm:block">{entry.cid}</code>
                            <ChevronRight className="size-4 shrink-0" />
                          </span>
                        </Link>
                      ))}
                    </div>
                  )}
              </CardContent>
            </Card>
          )}
        </div>

        <p className="mt-6 text-xs text-muted-foreground">Directory view shows names and content identifiers only.</p>
      </main>
    </div>
  )
}

export default function App() {
  return (
    <Routes>
      <Route path="/" element={<Home />} />
      <Route path="/explore/*" element={<Explorer />} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}
