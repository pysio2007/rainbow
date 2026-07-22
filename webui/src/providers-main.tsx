import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { ThemeProvider } from '@/components/theme-provider'
import Providers from '@/pages/providers'
import './index.css'

createRoot(document.getElementById('root')!).render(<StrictMode><ThemeProvider defaultTheme="dark"><Providers /></ThemeProvider></StrictMode>)
