export type GatewayStats = { version: number; filesProcessed: number; originBytes: number }

const statsApiPath = '/_rainbow/api/v1/stats'

export async function fetchStats(): Promise<GatewayStats | null> {
  try {
    const response = await fetch(statsApiPath, { headers: { Accept: 'application/json' } })
    if (!response.ok) return null
    const data = (await response.json()) as GatewayStats
    if (typeof data?.filesProcessed !== 'number' || typeof data?.originBytes !== 'number') return null
    return data
  } catch {
    return null
  }
}

export { formatBytes, formatCount } from './format'
