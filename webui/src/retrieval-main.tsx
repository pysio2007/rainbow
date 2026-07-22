import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Route, Routes } from 'react-router-dom'
import { ThemeProvider } from '@/components/theme-provider'
import Retrieval from '@/pages/retrieval'
import { Header } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Activity } from 'lucide-react'
import { useState } from 'react'
import { normalizeProviderTarget } from '@/lib/providers'
import { retrievalUrl } from '@/lib/retrieval'
import { retrievalRoutePaths } from './retrieval-route-paths'
import './index.css'

export { retrievalRoutePaths }

function RetrievalStart() {
  const [value, setValue] = useState(''); const [error, setError] = useState('')
  function submit(event: React.FormEvent) { event.preventDefault(); try { window.location.assign(retrievalUrl(normalizeProviderTarget(value))) } catch { setError('Enter a valid IPFS CID.') } }
  return <div className="flex min-h-svh flex-col"><Header /><main className="mx-auto w-full max-w-5xl flex-1 px-6 py-16"><div className="flex items-center gap-2 text-sm text-muted-foreground"><Activity className="size-4" /> Retrieval observations</div><h1 className="mt-3 text-3xl font-semibold tracking-tight">Observe a CID</h1><p className="mt-3 max-w-xl text-muted-foreground">Enter a CID to run separate root metadata and provider observations.</p><form className="mt-8 flex flex-col gap-2 sm:flex-row" onSubmit={submit}><Input value={value} onChange={(event) => { setValue(event.target.value); setError('') }} placeholder="CID or IPFS path" aria-label="CID or IPFS path" /><Button type="submit">Continue</Button></form>{error && <p role="alert" className="mt-3 text-sm text-destructive">{error}</p>}</main></div>
}

createRoot(document.getElementById('root')!).render(<StrictMode><ThemeProvider defaultTheme="dark"><BrowserRouter><Routes><Route path={retrievalRoutePaths[0]} element={<RetrievalStart />} /><Route path={retrievalRoutePaths[1]} element={<RetrievalStart />} /><Route path={retrievalRoutePaths[2]} element={<Retrieval />} /></Routes></BrowserRouter></ThemeProvider></StrictMode>)
