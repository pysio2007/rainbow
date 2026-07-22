import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

const retrieval = readFileSync(new URL('./retrieval.tsx', import.meta.url), 'utf8')
const ipns = readFileSync(new URL('./ipns.tsx', import.meta.url), 'utf8')

describe('Gate 4 factual copy', () => {
  it('keeps Retrieval wording limited to observations and browser delivery', () => {
    expect(retrieval).not.toContain('does not dial providers')
    expect(retrieval).toContain('A metadata observation may cause this gateway to retrieve the root block.')
    expect(retrieval).toContain('Provider lookup is a separate observation')
    expect(retrieval).toContain('does not deliver IPFS content to the browser')
  })

  it('describes the IPNS target as a reported record field', () => {
    expect(ipns).toContain('Reported record target')
    expect(ipns).not.toContain('Could not resolve this IPNS record')
    expect(ipns).not.toContain('Resolved target')
  })
})
