export type DirectoryEntry = { name: string; cid: string }
export type DirectorySortKey = 'name' | 'cid'
export type DirectorySortDirection = 'asc' | 'desc'
export const defaultDirectoryTools = { filter: '', sortKey: 'name' as DirectorySortKey, sortDirection: 'asc' as DirectorySortDirection }

export function resetDirectoryTools() { return { ...defaultDirectoryTools } }

export function rootCidFromPath(path: string): string {
  return path.split('/').filter(Boolean)[1] || ''
}

export function filterDirectoryEntries(entries: DirectoryEntry[], query: string): DirectoryEntry[] {
  const needle = query.trim().toLowerCase()
  if (!needle) return entries
  return entries.filter((entry) => entry.name.toLowerCase().includes(needle) || entry.cid.toLowerCase().includes(needle))
}

export function sortDirectoryEntries(entries: DirectoryEntry[], key: DirectorySortKey, direction: DirectorySortDirection): DirectoryEntry[] {
  const multiplier = direction === 'asc' ? 1 : -1
  return [...entries].sort((left, right) => left[key].localeCompare(right[key], undefined, { sensitivity: 'base' }) * multiplier)
}
