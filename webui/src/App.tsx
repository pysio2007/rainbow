import { useEffect, useState } from 'react'
import { ArrowUpRight, ChevronRight, CircleAlert, Compass, ExternalLink, FileAudio, FileImage, FileText, FileVideo, Folder, Search, Sparkles } from 'lucide-react'
import { Link, Navigate, Route, Routes, useLocation, useNavigate, useParams } from 'react-router-dom'
import type { FilePreviewType } from '@/lib/file-preview'
import { resolveFilePreviewType } from '@/lib/file-preview'
import { directoryApiPath, explorerPathToIpfsPath, gatewayPath, ipfsPathToExplorerPath, normalizeInput } from '@/lib/normalizer'
import { parseVersionText } from '@/lib/version'
import type { GatewayStats } from '@/lib/stats'
import { fetchStats, formatBytes, formatCount } from '@/lib/stats'

type Directory = { version: number; path: string; resolvedCid: string; entries: { name: string; cid: string }[] }
const sampleCid = 'QmYwAPJzv5CZsnAzt8auVZRnZQ5J7cV7Wc6YzS4hGJ5a6H'

function Header() {
  return <header className="site-header"><Link to="/" className="wordmark">suse.cc</Link><nav><Link to="/explore">Explorer <Compass size={15} /></Link><a href="/version">Status <ArrowUpRight size={14} /></a></nav></header>
}

function SearchBox({ initial = '' }: { initial?: string }) {
  const [value, setValue] = useState(initial)
  const [error, setError] = useState('')
  const navigate = useNavigate()
  let target = null
  try { target = value.trim() ? normalizeInput(value) : null } catch { target = null }

  function submit(event: React.FormEvent) {
    event.preventDefault()
    try {
      const result = normalizeInput(value)
      setError('')
      window.location.assign(gatewayPath(result.path))
    } catch (caught) { setError(caught instanceof Error ? caught.message : 'Invalid input') }
  }

  return <form className="search-wrap" onSubmit={submit}>
    <Search size={20} aria-hidden="true" />
    <input value={value} onChange={(event) => { setValue(event.target.value); setError('') }} placeholder="Paste a CID, IPFS path, or gateway URL" aria-label="Content address" autoComplete="off" />
    {value && <button type="button" className="clear-button" onClick={() => setValue('')} aria-label="Clear input">×</button>}
    <button className="go-button" type="submit">Open <ArrowUpRight size={16} /></button>
    {target?.canExplore && <button type="button" className="explore-button" onClick={() => navigate(ipfsPathToExplorerPath(target.path))}>Explore <Compass size={15} /></button>}
    {error && <p className="field-error" role="alert"><CircleAlert size={15} />{error}</p>}
  </form>
}

function Home() {
  const [version, setVersion] = useState('')
  const [stats, setStats] = useState<GatewayStats | null>(null)
  useEffect(() => {
    fetch('/version').then(async (response) => response.ok ? parseVersionText(response.headers.get('content-type'), await response.text()) : '').then(setVersion).catch(() => undefined)
    fetchStats().then(setStats).catch(() => undefined)
  }, [])
  return <><Header /><main className="home">
    <section className="hero">
      <div className="hero-kicker"><Sparkles size={15} /> public IPFS gateway</div>
      <h1>suse.cc<br /><em>for IPFS content.</em></h1>
      <p className="hero-copy">Access IPFS content by pasting a CID, an IPFS path, or a gateway URL. The gateway takes you straight to the content you name.</p>
      <SearchBox />
      <div className="hero-hint"><span>Direct access</span><code>suse.cc/ipfs/&lt;CID&gt;</code><span>or try</span><button onClick={() => window.location.assign(`/ipfs/${sampleCid}`)}>{sampleCid.slice(0, 12)}…</button></div>
      <div className="hero-proof"><strong>Fast</strong><span>to open</span><strong>Free</strong><span>to use</span></div>
      {stats && <div className="hero-proof hero-stats"><strong>{formatCount(stats.filesProcessed)}</strong><span>files served</span><strong>{formatBytes(stats.originBytes)}</strong><span>origin traffic</span></div>}
    </section>
    <section className="home-bottom"><div><span className="eyebrow">A public route to IPFS</span><h2>Fast to open.<br />Free to use.</h2></div><div className="principles"><div><strong>01</strong><span>Open by CID or path</span></div><div><strong>02</strong><span>Browse immutable directories</span></div><div><strong>03</strong><span>Direct URL access</span></div></div></section>
  </main><footer className="site-footer"><span>suse.cc / public IPFS gateway</span>{version && <span>{version.trim()}</span>}</footer></>
}

function FilePreview({ path, type }: { path: string; type: FilePreviewType }) {
  const url = gatewayPath(path)
  const name = decodeURIComponent(path.split('/').pop() || 'IPFS file')
  const icon = type === 'image' ? <FileImage size={18} /> : type === 'audio' ? <FileAudio size={18} /> : type === 'video' ? <FileVideo size={18} /> : <FileText size={18} />
  const label = type === 'unknown' ? 'Preview unavailable' : `${type.toUpperCase()} preview`

  return <section className={`file-preview file-preview-${type}`} aria-label={`${label}: ${name}`}>
    <div className="file-preview-heading"><div className="file-preview-label">{icon}<span>{label}</span></div><code title={name}>{name}</code></div>
    {type === 'image' && <img className="preview-image" src={url} alt={name} />}
    {type === 'audio' && <audio className="preview-audio" controls preload="metadata" src={url} aria-label={`Play ${name}`}>Your browser cannot play this audio file.</audio>}
    {type === 'video' && <video className="preview-video" controls preload="metadata" src={url} aria-label={`Play ${name}`}>Your browser cannot play this video file.</video>}
    {type === 'pdf' && <iframe className="preview-pdf" src={url} title={`Preview of ${name}`} />}
    {type === 'unknown' && <div className="preview-unavailable"><FileText size={28} /><h2 id="file-preview-title">This file cannot be previewed here</h2><p>Open the native gateway to view or download this content.</p></div>}
    {type !== 'unknown' && <p className="preview-fallback">Having trouble loading it? <a href={url}>Open native gateway <ArrowUpRight size={14} /></a></p>}
    {type === 'unknown' && <a className="preview-open" href={url}>Open native gateway <ArrowUpRight size={14} /></a>}
  </section>
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
      .then((data: Directory) => { if (active) setDirectory(data) }).catch((caught) => { if (!active) return; if (caught instanceof Error && caught.message === 'not_directory') setPreviewType(resolveFilePreviewType(rawPath)); else setError(caught instanceof Error ? caught.message : 'Directory unavailable') }).finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [rawPath])

  if (!rawPath) return <><Header /><main className="explore-empty"><div className="empty-icon"><Compass size={27} /></div><h1>Explore an immutable directory</h1><p>Start with an IPFS CID. Directory entries are fetched from this gateway.</p><SearchBox /></main></>
  const breadcrumb = rawPath.split('/').filter(Boolean).slice(1)
  return <><Header /><main className="explorer"><div className="explorer-top"><div><span className="eyebrow">Immutable explorer</span><h1>Directory</h1></div><a className="native-link" href={gatewayPath(rawPath)}>Open native gateway <ExternalLink size={15} /></a></div>
    <div className="breadcrumbs"><Link to="/explore">Explorer</Link>{breadcrumb.map((part, index) => <span key={`${part}-${index}`}><ChevronRight size={14} /><Link to={ipfsPathToExplorerPath(`/ipfs/${breadcrumb.slice(0, index + 1).join('/')}`)}>{index === 0 ? `${part.slice(0, 12)}…` : part}</Link></span>)}</div>
    {loading && <div className="status"><div className="spinner" />Loading directory</div>}
    {error && <div className="status error-status"><CircleAlert size={18} /><div><strong>Could not open this directory</strong><p>{error.replaceAll('_', ' ')}</p><a href={gatewayPath(rawPath)}>Open it in the native gateway <ArrowUpRight size={14} /></a></div></div>}
    {previewType && <FilePreview path={rawPath} type={previewType} />}
    {directory && <div className="directory-panel"><div className="directory-meta"><span>{directory.entries.length} {directory.entries.length === 1 ? 'entry' : 'entries'}</span><code>{directory.resolvedCid}</code></div>{directory.entries.length === 0 ? <div className="empty-list">This directory is empty.</div> : <div className="entry-list">{directory.entries.map((entry) => <Link className="entry" key={`${entry.name}-${entry.cid}`} to={ipfsPathToExplorerPath(`${rawPath}/${entry.name}`)}><span className="entry-name"><Folder size={18} />{entry.name}</span><code>{entry.cid}</code><ChevronRight size={17} /></Link>)}</div>}</div>}
    <p className="explorer-note">Directory view shows names and content identifiers only.</p>
  </main></>
}

export default function App() { return <Routes><Route path="/" element={<Home />} /><Route path="/explore/*" element={<Explorer />} /><Route path="*" element={<Navigate to="/" replace />} /></Routes> }
