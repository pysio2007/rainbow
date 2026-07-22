import { describe, expect, it } from 'vitest'
import { inspectorRoutePaths } from './inspector-route-paths'

describe('Inspector MPA route entry', () => {
  it('declares a direct CID route for refreshable and shareable inspection', () => {
    expect(inspectorRoutePaths).toEqual(['/inspect/', '/inspect/ipns/:name', '/inspect/:cid/dag', '/inspect/:cid/car', '/inspect/:cid'])
  })
})
