import { describe, expect, it } from 'vitest'
import { retrievalRoutePaths } from './retrieval-route-paths'

describe('Retrieval MPA route entry', () => {
  it('declares a direct CID route', () => {
    expect(retrievalRoutePaths).toEqual(['/retrieval', '/retrieval/', '/retrieval/:cid'])
  })
})
