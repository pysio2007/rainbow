import { describe, expect, it } from 'vitest'
import { filterDirectoryEntries, resetDirectoryTools, rootCidFromPath, sortDirectoryEntries } from './explorer-data'

const entries = [
  { name: 'zeta', cid: 'b' },
  { name: 'Alpha', cid: 'c' },
  { name: 'notes', cid: 'a' },
]

describe('Explorer local data helpers', () => {
  it('filters loaded names and CIDs without changing the source list', () => {
    expect(filterDirectoryEntries(entries, 'ALP')).toEqual([entries[1]])
    expect(filterDirectoryEntries(entries, 'b')).toEqual([entries[0]])
    expect(entries).toHaveLength(3)
  })

  it('sorts by name or CID in both directions', () => {
    expect(sortDirectoryEntries(entries, 'name', 'asc').map((entry) => entry.name)).toEqual(['Alpha', 'notes', 'zeta'])
    expect(sortDirectoryEntries(entries, 'cid', 'desc').map((entry) => entry.cid)).toEqual(['c', 'b', 'a'])
  })

  it('resets local tools and derives the original root CID from a path', () => {
    expect(resetDirectoryTools()).toEqual({ filter: '', sortKey: 'name', sortDirection: 'asc' })
    expect(rootCidFromPath('/ipfs/bafyroot/folder/file')).toBe('bafyroot')
  })
})
