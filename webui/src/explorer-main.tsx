import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Route, Routes } from 'react-router-dom'
import { ThemeProvider } from '@/components/theme-provider'
import Explorer from '@/pages/explorer'
import { explorerRoutePaths } from './explorer-route-paths'
import './index.css'

export { explorerRoutePaths }

function ExplorerRoutes() {
  return (
    <Routes>
      <Route path={explorerRoutePaths[0]} element={<Explorer />} />
      <Route path={explorerRoutePaths[1]} element={<Explorer />} />
    </Routes>
  )
}

createRoot(document.getElementById('root')!).render(<StrictMode><ThemeProvider defaultTheme="dark"><BrowserRouter><ExplorerRoutes /></BrowserRouter></ThemeProvider></StrictMode>)
