import { useEffect, useState } from 'react'
import { ArrowUpRight, ChevronRight, CircleAlert, Compass, ExternalLink, Folder, Search, Sparkles } from 'lucide-react'
import { Link, Navigate, Route, Routes, useLocation, useNavigate, useParams } from 'react-router-dom'
import { directoryApiPath, explorerPathToIpfsPath, gatewayPath, ipfsPathToExplorerPath, normalizeInput } from '@/lib/normalizer'
import { parseVersionText } from '@/lib/version'

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
  useEffect(() => {
    fetch('/version').then(async (response) => response.ok ? parseVersionText(response.headers.get('content-type'), await response.text()) : '').then(setVersion).catch(() => undefined)
  }, [])
  return <><Header /><main className="home">
    <section className="hero">
      <div className="hero-kicker"><Sparkles size={15} /> public IPFS gateway</div>
      <h1>suse.cc<br /><em>for IPFS content.</em></h1>
      <p className="hero-copy">Access IPFS content by pasting a CID, an IPFS path, or a gateway URL. The gateway takes you straight to the content you name.</p>
      <SearchBox />
      <div className="hero-hint"><span>Direct access</span><code>suse.cc/ipfs/&lt;CID&gt;</code><span>or try</span><button onClick={() => window.location.assign(`/ipfs/${sampleCid}`)}>{sampleCid.slice(0, 12)}…</button></div>
      <div className="hero-proof"><strong>Fast</strong><span>to open</span><strong>Free</strong><span>to use</span></div>
    </section>
    <section className="home-bottom"><div><span className="eyebrow">A public route to IPFS</span><h2>Fast to open.<br />Free to use.</h2></div><div className="principles"><div><strong>01</strong><span>Open by CID or path</span></div><div><strong>02</strong><span>Browse immutable directories</span></div><div><strong>03</strong><span>Direct URL access</span></div></div></section>
  </main><footer className="site-footer"><span>suse.cc / public IPFS gateway</span>{version && <span>{version.trim()}</span>}</footer></>
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
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    if (!rawPath) { setLoading(false); return }
    let active = true
    setLoading(true); setError(''); setDirectory(null)
    let apiPath: string
    try { apiPath = directoryApiPath(rawPath) } catch { setError('This is not a valid immutable path.'); setLoading(false); return }
    fetch(`/_rainbow/api/v1/directory?path=${encodeURIComponent(apiPath)}`, { headers: { Accept: 'application/json' } })
      .then(async (response) => { if (!response.ok) throw new Error((await response.json().catch(() => null))?.error?.code || 'Directory unavailable'); return response.json() })
      .then((data: Directory) => { if (active) setDirectory(data) }).catch((caught) => { if (active) setError(caught instanceof Error ? caught.message : 'Directory unavailable') }).finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [rawPath])

  if (!rawPath) return <><Header /><main className="explore-empty"><div className="empty-icon"><Compass size={27} /></div><h1>Explore an immutable directory</h1><p>Start with an IPFS CID. Directory entries are fetched from this gateway.</p><SearchBox /></main></>
  const breadcrumb = rawPath.split('/').filter(Boolean).slice(1)
  return <><Header /><main className="explorer"><div className="explorer-top"><div><span className="eyebrow">Immutable explorer</span><h1>Directory</h1></div><a className="native-link" href={gatewayPath(rawPath)}>Open native gateway <ExternalLink size={15} /></a></div>
    <div className="breadcrumbs"><Link to="/explore">Explorer</Link>{breadcrumb.map((part, index) => <span key={`${part}-${index}`}><ChevronRight size={14} /><Link to={ipfsPathToExplorerPath(`/ipfs/${breadcrumb.slice(0, index + 1).join('/')}`)}>{index === 0 ? `${part.slice(0, 12)}…` : part}</Link></span>)}</div>
    {loading && <div className="status"><div className="spinner" />Loading directory</div>}
    {error && <div className="status error-status"><CircleAlert size={18} /><div><strong>Could not open this directory</strong><p>{error.replaceAll('_', ' ')}</p><a href={gatewayPath(rawPath)}>Open it in the native gateway <ArrowUpRight size={14} /></a></div></div>}
    {directory && <div className="directory-panel"><div className="directory-meta"><span>{directory.entries.length} {directory.entries.length === 1 ? 'entry' : 'entries'}</span><code>{directory.resolvedCid}</code></div>{directory.entries.length === 0 ? <div className="empty-list">This directory is empty.</div> : <div className="entry-list">{directory.entries.map((entry) => <Link className="entry" key={`${entry.name}-${entry.cid}`} to={ipfsPathToExplorerPath(`${rawPath}/${entry.name}`)}><span className="entry-name"><Folder size={18} />{entry.name}</span><code>{entry.cid}</code><ChevronRight size={17} /></Link>)}</div>}</div>}
    <p className="explorer-note">Directory view shows names and content identifiers only.</p>
  </main></>
}

export default function App() { return <Routes><Route path="/" element={<Home />} /><Route path="/explore/*" element={<Explorer />} /><Route path="*" element={<Navigate to="/" replace />} /></Routes> }
