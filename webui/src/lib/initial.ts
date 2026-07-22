import type { GatewayStats } from './stats'

type InitialData = { version?: string; stats?: GatewayStats }

declare global {
  interface Window { __INITIAL__?: InitialData }
}

export function initialVersion(): string {
  return window.__INITIAL__?.version ?? ''
}

export function initialStats(): GatewayStats | null {
  return window.__INITIAL__?.stats ?? null
}
