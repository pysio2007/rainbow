export type DagLink = { cid: string; name?: string }
export type DagNode = { cid: string; depth: number; codec: string; status: string; blockBytes: number; blockVerified: boolean; links: { total: number; shown: number; truncated: boolean; items: DagLink[] } }
export type DagResponseV1 = { version: 1; root: string; requestedDepth: number; observation: { status: string; truncated: boolean; limitsHit: string[]; nodesAttempted: number; parsedBytes: number }; nodes: DagNode[] }
export type IpnsResponseV1 = { version: 1; name: string; record: { status: string; target?: { status: string; path: string }; sequence?: string; eol?: string; ttlNanos?: string } }
export type CarCheckV1 = { version: 1; cid: string; verification: { status: string; scope: string; carVersion?: number; declaredRootMatches?: boolean; rootBlockPresent?: boolean; rootBlockVerified?: boolean; blocksRead?: number; bytesRead?: number } }

function record(value: unknown): Record<string, unknown> | null { return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null }
function stringValue(value: unknown): string | null { return typeof value === 'string' && value.length > 0 ? value : null }
function numberValue(value: unknown): number | null { return typeof value === 'number' && Number.isFinite(value) ? value : null }
function booleanValue(value: unknown): boolean | null { return typeof value === 'boolean' ? value : null }
function requiredString(source: Record<string, unknown> | null, key: string): string | null { return source ? stringValue(source[key]) : null }
function requiredNumber(source: Record<string, unknown> | null, key: string): number | null { return source ? numberValue(source[key]) : null }
function requiredBoolean(source: Record<string, unknown> | null, key: string): boolean | null { return source ? booleanValue(source[key]) : null }

function normalizeDagLinks(value: unknown): DagNode['links'] | null {
  const source = record(value)
  const total = requiredNumber(source, 'total'); const shown = requiredNumber(source, 'shown'); const truncated = requiredBoolean(source, 'truncated')
  if (total === null || shown === null || truncated === null || !Array.isArray(source?.items)) return null
  const items: DagLink[] = []
  for (const value of source.items) {
    const link = record(value); const cid = requiredString(link, 'cid')
    if (!cid) return null
    if (link?.name !== undefined && !stringValue(link.name)) return null
    items.push(link?.name === undefined ? { cid } : { cid, name: link.name as string })
  }
  return items.length === shown ? { total, shown, truncated, items } : null
}

export function normalizeDag(value: unknown): DagResponseV1 | null {
  const source = record(value)
  if (!source || source.version !== 1 || !stringValue(source.root) || ![1, 2].includes(source.requestedDepth as number)) return null
  const observation = record(source.observation)
  const status = requiredString(observation, 'status'); const truncated = requiredBoolean(observation, 'truncated'); const attempted = requiredNumber(observation, 'nodesAttempted'); const parsedBytes = requiredNumber(observation, 'parsedBytes')
  if (!observation || !status || truncated === null || attempted === null || parsedBytes === null || !Array.isArray(observation.limitsHit) || observation.limitsHit.some((item) => !stringValue(item)) || !Array.isArray(source.nodes)) return null
  const nodes: DagNode[] = []
  for (const value of source.nodes) {
    const node = record(value); const cid = requiredString(node, 'cid'); const depth = requiredNumber(node, 'depth'); const codec = requiredString(node, 'codec'); const nodeStatus = requiredString(node, 'status'); const blockBytes = requiredNumber(node, 'blockBytes'); const blockVerified = requiredBoolean(node, 'blockVerified'); const links = normalizeDagLinks(node?.links)
    if (!cid || depth === null || !codec || !nodeStatus || blockBytes === null || blockVerified === null || !links) return null
    nodes.push({ cid, depth, codec, status: nodeStatus, blockBytes, blockVerified, links })
  }
  return { version: 1, root: source.root as string, requestedDepth: source.requestedDepth as number, observation: { status, truncated, limitsHit: observation.limitsHit as string[], nodesAttempted: attempted, parsedBytes }, nodes }
}

export function normalizeIpns(value: unknown): IpnsResponseV1 | null {
  const source = record(value); const recordSource = record(source?.record); const status = requiredString(recordSource, 'status'); const name = requiredString(source, 'name')
  if (!source || source.version !== 1 || !name || !status) return null
  const result: IpnsResponseV1 = { version: 1, name, record: { status } }
  if (recordSource?.target !== undefined) {
    const target = record(recordSource.target); const targetStatus = requiredString(target, 'status'); const path = requiredString(target, 'path')
    if (!targetStatus || !path) return null
    result.record.target = { status: targetStatus, path }
  }
  for (const key of ['sequence', 'eol', 'ttlNanos'] as const) if (recordSource?.[key] !== undefined) { const item = stringValue(recordSource[key]); if (!item) return null; result.record[key] = item }
  return result
}

export function normalizeCarCheck(value: unknown): CarCheckV1 | null {
  const source = record(value); const verification = record(source?.verification); const cid = requiredString(source, 'cid'); const status = requiredString(verification, 'status'); const scope = requiredString(verification, 'scope')
  if (!source || source.version !== 1 || !cid || !status || !scope) return null
  const result: CarCheckV1 = { version: 1, cid, verification: { status, scope } }
  for (const key of ['carVersion'] as const) if (verification?.[key] !== undefined) { const item = numberValue(verification[key]); if (item === null) return null; result.verification[key] = item }
  for (const key of ['declaredRootMatches', 'rootBlockPresent', 'rootBlockVerified'] as const) if (verification?.[key] !== undefined) { const item = booleanValue(verification[key]); if (item === null) return null; result.verification[key] = item }
  for (const key of ['blocksRead', 'bytesRead'] as const) if (verification?.[key] !== undefined) { const item = numberValue(verification[key]); if (item === null) return null; result.verification[key] = item }
  return result
}

export function phase4Status(status: string | undefined): string { return status ? status.replaceAll('_', ' ') : 'Not reported' }
