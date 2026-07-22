import { existsSync, readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('Inspector MPA entry', () => {
  it('declares the Inspector entry module for the Vite build', () => {
    const path = new URL('../inspect/index.html', import.meta.url)
    expect(existsSync(path)).toBe(true)
    expect(readFileSync(path, 'utf8')).toContain('/src/inspector-main.tsx')
    const retrievalPath = new URL('../retrieval/index.html', import.meta.url)
    expect(existsSync(retrievalPath)).toBe(true)
    expect(readFileSync(retrievalPath, 'utf8')).toContain('/src/retrieval-main.tsx')
  })
})
