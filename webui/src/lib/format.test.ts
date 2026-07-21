import { describe, expect, it } from 'vitest'
import { formatBytes, formatCount } from './format'

describe('formatCount', () => {
  it.each([
    [0, '0'],
    [42, '42'],
    [999, '999'],
    [1000, '1.0K'],
    [1500, '1.5K'],
    [1_200_000, '1.2M'],
    [3_400_000_000, '3.4B'],
  ])('formats %s as %s', (value, expected) => expect(formatCount(value)).toBe(expected))

  it('handles invalid input', () => {
    expect(formatCount(-5)).toBe('0')
    expect(formatCount(Number.NaN)).toBe('0')
  })
})

describe('formatBytes', () => {
  it.each([
    [0, '0 B'],
    [512, '512 B'],
    [1024, '1.0 KiB'],
    [1536, '1.5 KiB'],
    [1_048_576, '1.0 MiB'],
    [5_368_709_120, '5.0 GiB'],
  ])('formats %s as %s', (value, expected) => expect(formatBytes(value)).toBe(expected))

  it('handles invalid input', () => {
    expect(formatBytes(-1)).toBe('0 B')
    expect(formatBytes(Number.NaN)).toBe('0 B')
  })
})
