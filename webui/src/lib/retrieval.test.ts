import { describe, expect, it } from 'vitest'
import { retrievalUrl } from './retrieval'

describe('Retrieval observation URL', () => {
  it('creates a shareable CID observation route without provider data', () => {
    expect(retrievalUrl('bafyroot')).toBe('/retrieval/bafyroot')
  })
})
