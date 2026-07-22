import { useState } from 'react'
import type { FormEvent } from 'react'
import { Activity, ArrowUpRight, CircleAlert, Compass, Network, Search, ScanSearch } from 'lucide-react'
import { gatewayPath, ipfsPathToExplorerPath, normalizeInput } from '@/lib/normalizer'
import { providerUrl } from '@/lib/providers'
import { ModeToggle } from '@/components/mode-toggle'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

export const headerContainerClassName = 'mx-auto flex w-full max-w-5xl flex-col items-start gap-y-2 px-6 py-4 sm:flex-row sm:items-center sm:justify-between'
export const headerNavClassName = 'flex w-full flex-wrap items-center justify-start gap-1 sm:w-auto sm:justify-end'

export function Header() {
  return (
    <header className="border-b">
      <div className={headerContainerClassName}>
        <a href="/" className="text-lg font-semibold tracking-tight">suse.cc</a>
        <nav className={headerNavClassName}>
          <Button asChild variant="ghost" size="sm">
            <a href="/explore/"><Compass className="size-4" />Explorer</a>
          </Button>
          <Button asChild variant="ghost" size="sm"><a href="/network/providers/"><Network className="size-4" />Providers</a></Button>
          <Button asChild variant="ghost" size="sm"><a href="/inspect/"><ScanSearch className="size-4" />Inspector</a></Button>
          <Button asChild variant="ghost" size="sm"><a href="/retrieval/"><Activity className="size-4" />Retrieval</a></Button>
          <Button asChild variant="ghost" size="sm">
            <a href="/version">Status<ArrowUpRight className="size-4" /></a>
          </Button>
          <ModeToggle />
        </nav>
      </div>
    </header>
  )
}

export function SiteFooter({ version }: { version: string }) {
  return (
    <footer className="border-t">
      <div className="mx-auto flex max-w-5xl flex-wrap items-center justify-between gap-2 px-6 py-6 text-sm text-muted-foreground">
        <span>suse.cc · public IPFS gateway</span>
        {version && <span className="font-mono text-xs">{version.trim()}</span>}
      </div>
    </footer>
  )
}

export function SearchBox({ initial = '' }: { initial?: string }) {
  const [value, setValue] = useState(initial)
  const [error, setError] = useState('')
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
            <Button type="button" variant="secondary" onClick={() => window.location.assign(ipfsPathToExplorerPath(target.path))}>
              Explore <Compass className="size-4" />
            </Button>
          )}
          {target?.kind === 'ipfs' && (
            <Button type="button" variant="secondary" onClick={() => window.location.assign(providerUrl(target.path))}>
              Providers <Network className="size-4" />
            </Button>
          )}
          {target?.kind === 'ipfs' && !target.path.slice('/ipfs/'.length).includes('/') && (
            <Button type="button" variant="secondary" onClick={() => window.location.assign(`/inspect/${encodeURIComponent(target.path.slice('/ipfs/'.length))}`)}>
              Inspect <ScanSearch className="size-4" />
            </Button>
          )}
          {target?.kind === 'ipfs' && !target.path.slice('/ipfs/'.length).includes('/') && (
            <Button type="button" variant="secondary" onClick={() => window.location.assign(`/retrieval/${encodeURIComponent(target.path.slice('/ipfs/'.length))}`)}>
              Retrieval <Activity className="size-4" />
            </Button>
          )}
        </div>
      </div>
      {error && <p role="alert" className="flex items-center gap-1.5 text-sm text-destructive"><CircleAlert className="size-4" />{error}</p>}
    </form>
  )
}
