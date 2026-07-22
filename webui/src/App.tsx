import { Suspense, lazy, useEffect, useState } from 'react'
import { Sparkles } from 'lucide-react'
import { Navigate, Route, Routes } from 'react-router-dom'
import { parseVersionText } from '@/lib/version'
import { formatBytes, formatCount } from '@/lib/format'
import type { GatewayStats } from '@/lib/stats'
import { fetchStats } from '@/lib/stats'
import { initialStats, initialVersion } from '@/lib/initial'
import { Header, SearchBox, SiteFooter } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Card, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

const Explorer = lazy(() => import('@/pages/explorer'))

const sampleCid = 'QmYwAPJzv5CZsnAzt8auVZRnZQ5J7cV7Wc6YzS4hGJ5a6H'

const features = [
  { n: '01', t: 'Open by CID or path' },
  { n: '02', t: 'Browse immutable directories' },
  { n: '03', t: 'Direct URL access' },
]

function Home() {
  const [version, setVersion] = useState(initialVersion)
  const [stats, setStats] = useState<GatewayStats | null>(initialStats)
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

export default function App() {
  return (
    <Routes>
      <Route path="/" element={<Home />} />
      <Route path="/explore/*" element={<Suspense fallback={null}><Explorer /></Suspense>} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}
