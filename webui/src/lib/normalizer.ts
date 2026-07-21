import { CID } from 'multiformats/cid'

export type ResolvedTarget = {
  path: `/ipfs/${string}` | `/ipns/${string}`
  kind: 'ipfs' | 'ipns'
  canExplore: boolean
}

function cleanPath(value: string): string {
  const decoded = decodeURIComponent(value)
  if (!decoded.startsWith('/') || decoded.includes('\0') || decoded.split('/').some((part) => part === '..')) {
    throw new Error('Invalid path')
  }
  return decoded.replace(/\/+/g, '/').replace(/\/+$/, '') || '/'
}

function target(namespace: 'ipfs' | 'ipns', value: string): ResolvedTarget {
  const path = cleanPath(`/${namespace}/${value}`)
  const parts = path.split('/').filter(Boolean)
  if (!parts[1]) throw new Error('Missing name')
  if (namespace === 'ipfs') {
    try { CID.parse(parts[1]) } catch { throw new Error('Invalid CID') }
  }
  return { path: path as ResolvedTarget['path'], kind: namespace, canExplore: namespace === 'ipfs' }
}

export function normalizeInput(input: string, baseOrigin = window.location.origin): ResolvedTarget {
  const value = input.trim()
  if (!value) throw new Error('Enter a path or CID')

  const uriMatch = value.match(/^(ipfs|ipns):\/\/([^/?#]+)(\/[^?#]*)?(?:[?#].*)?$/i)
  if (uriMatch) {
    return target(uriMatch[1].toLowerCase() as 'ipfs' | 'ipns', `${uriMatch[2]}${uriMatch[3] || ''}`)
  }

  if (/^https?:\/\//i.test(value)) {
    const url = new URL(value)
    if (url.origin !== baseOrigin || !/^\/(?:ipfs|ipns)\//.test(url.pathname)) throw new Error('Unsafe gateway URL')
    return target(url.pathname.split('/')[1] as 'ipfs' | 'ipns', url.pathname.split('/').slice(2).join('/'))
  }

  if (value.startsWith('/') || /^(?:ipfs|ipns)\//i.test(value)) {
    const path = cleanPath(value.startsWith('/') ? value : `/${value}`)
    const match = path.match(/^\/(ipfs|ipns)\/(.+)$/)
    if (!match) throw new Error('Expected an IPFS or IPNS path')
    return target(match[1] as 'ipfs' | 'ipns', match[2])
  }
  try { CID.parse(value); return target('ipfs', value) } catch { /* fall through to the user-facing error */ }
  throw new Error('Could not recognize that input')
}

export function directoryApiPath(path: string): string {
  const normalized = cleanPath(path)
  const parts = normalized.split('/').filter(Boolean)
  if (parts[0] !== 'ipfs' || !parts[1]) throw new Error('Explorer only supports immutable paths')
  try { CID.parse(parts[1]) } catch { throw new Error('Invalid CID') }
  return normalized
}

function encodedSegments(path: string): string[] {
  return path.split('/').filter(Boolean).map((part) => encodeURIComponent(part))
}

export function ipfsPathToExplorerPath(path: string): string {
  const normalized = directoryApiPath(path)
  return `/explore/${encodedSegments(normalized).slice(1).join('/')}`
}

export function explorerPathToIpfsPath(path: string): string {
  const parts = path.split('/').filter(Boolean)
  if (parts.shift()?.toLowerCase() !== 'explore' || !parts.length) throw new Error('Missing immutable path')
  const decoded = parts.map((part) => decodeURIComponent(part))
  return directoryApiPath(`/ipfs/${decoded.join('/')}`)
}

export function gatewayPath(path: string): string {
  return `/${encodedSegments(path).join('/')}`
}
