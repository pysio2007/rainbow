import { describe, expect, it } from 'vitest'
import { directoryApiPath, explorerPathToIpfsPath, gatewayPath, ipfsPathToExplorerPath, normalizeInput } from './normalizer'

describe('normalizeInput', () => {
  const root = 'QmYwAPJzv5CZsnAzt8auVZRnZQ5J7cV7Wc6YzS4hGJ5a6H'
  it.each([
    [root, `/ipfs/${root}`],
    [`ipfs://${root}/docs/`, `/ipfs/${root}/docs`],
    [`/ipfs/${root}/a%20b`, `/ipfs/${root}/a b`],
    [`ipfs/${root}/docs`, `/ipfs/${root}/docs`],
    [`ipns/example.com/site`, '/ipns/example.com/site'],
    [`ipns://example.com/site`, '/ipns/example.com/site'],
    [`http://127.0.0.1:8090/ipfs/${root}/`, `/ipfs/${root}`],
  ])('normalizes %s', (input, path) => expect(normalizeInput(input, 'http://127.0.0.1:8090').path).toBe(path))
  it.each(['', 'not a target', 'javascript:alert(1)', 'https://evil.example/ipfs/x', '/ipfs/../secret', '/foo/bar', '/ipfs/Qmabc'])('rejects %s', (input) => expect(() => normalizeInput(input, 'http://127.0.0.1:8090')).toThrow())
})

describe('directoryApiPath', () => {
  it('removes only trailing slashes', () => expect(directoryApiPath('/ipfs/QmYwAPJzv5CZsnAzt8auVZRnZQ5J7cV7Wc6YzS4hGJ5a6H/docs///')).toBe('/ipfs/QmYwAPJzv5CZsnAzt8auVZRnZQ5J7cV7Wc6YzS4hGJ5a6H/docs'))
  it('rejects non immutable paths', () => expect(() => directoryApiPath('/ipns/name')).toThrow())
})

describe('explorer path mapping', () => {
  const root = '/ipfs/QmYwAPJzv5CZsnAzt8auVZRnZQ5J7cV7Wc6YzS4hGJ5a6H/folder/a b'
  it('maps immutable paths canonically in both directions', () => {
    const explorer = ipfsPathToExplorerPath(root)
    expect(explorer).toBe('/explore/QmYwAPJzv5CZsnAzt8auVZRnZQ5J7cV7Wc6YzS4hGJ5a6H/folder/a%20b')
    expect(explorerPathToIpfsPath(explorer)).toBe(root)
  })
  it('encodes native gateway segments', () => expect(gatewayPath(root)).toContain('/folder/a%20b'))
})
