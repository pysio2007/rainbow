import { describe, expect, it } from 'vitest'
import { explorerRoutePaths } from './explorer-route-paths'

describe('Explorer MPA route entry', () => {
  it('declares both the Explorer root and wildcard deep-link routes', () => {
    expect(explorerRoutePaths).toEqual(['/explore', '/explore/*'])
  })
})
