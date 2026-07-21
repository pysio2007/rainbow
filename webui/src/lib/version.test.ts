import { describe, expect, it } from 'vitest'
import { parseVersionText } from './version'

describe('parseVersionText', () => {
  it('keeps a valid plain-text version response', () => {
    expect(parseVersionText('text/plain; charset=utf-8', 'Client: suse.cc\nVersion: 1.2.3\n')).toBe('Client: suse.cc\nVersion: 1.2.3')
  })

  it.each([
    ['text/html; charset=utf-8', '<!doctype html><html><body>suse.cc</body></html>'],
    ['application/json', '{"version":"1.2.3"}'],
    ['text/plain', ''],
  ])('rejects non-version response %s', (contentType, body) => expect(parseVersionText(contentType, body)).toBe(''))
})
