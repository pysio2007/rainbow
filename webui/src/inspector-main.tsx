import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Route, Routes } from 'react-router-dom'
import { ThemeProvider } from '@/components/theme-provider'
import { Header, SearchBox } from '@/components/layout'
import Inspector from '@/pages/inspector'
import Dag from '@/pages/dag'
import Ipns from '@/pages/ipns'
import Car from '@/pages/car'
import { inspectorRoutePaths } from './inspector-route-paths'
import './index.css'

export { inspectorRoutePaths }

function InspectorStart() {
  return <div className="flex min-h-svh flex-col"><Header /><main className="mx-auto w-full max-w-5xl flex-1 px-6 py-16"><div className="text-sm text-muted-foreground">CID Inspector</div><h1 className="mt-3 text-3xl font-semibold tracking-tight">Inspect a CID</h1><p className="mt-3 max-w-xl text-muted-foreground">Enter an IPFS CID to inspect its root metadata and observed links.</p><div className="mt-8"><SearchBox /></div></main></div>
}

createRoot(document.getElementById('root')!).render(
  <StrictMode><ThemeProvider defaultTheme="dark"><BrowserRouter><Routes><Route path={inspectorRoutePaths[0]} element={<InspectorStart />} /><Route path={inspectorRoutePaths[1]} element={<Ipns />} /><Route path={inspectorRoutePaths[2]} element={<Dag />} /><Route path={inspectorRoutePaths[3]} element={<Car />} /><Route path={inspectorRoutePaths[4]} element={<Inspector />} /></Routes></BrowserRouter></ThemeProvider></StrictMode>,
)
