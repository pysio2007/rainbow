import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { compression } from 'vite-plugin-compression2'
import path from 'node:path'

export default defineConfig({
  base: '/',
  appType: 'mpa',
  build: { rolldownOptions: { input: { home: path.resolve(__dirname, 'index.html'), explore: path.resolve(__dirname, 'explore/index.html'), providers: path.resolve(__dirname, 'network/providers/index.html') } } },
  plugins: [
    react(),
    tailwindcss(),
    // Precompress hashed JS/CSS assets at build time so the Go server can serve
    // .gz/.br bytes directly with zero request-time compression cost.
    compression({ algorithms: ['gzip', 'brotliCompress'], include: /\.(js|css)$/, threshold: 1024 }),
  ],
  resolve: { alias: { '@': path.resolve(__dirname, './src') } },
})
