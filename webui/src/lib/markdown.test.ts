import { describe, expect, it } from 'vitest'
import { resolveMarkdownUrl } from './markdown'

const file = '/ipfs/QmCID/docs/guide.md'

describe('resolveMarkdownUrl', () => {
  it('resolves a sibling relative path against the file directory', () => {
    expect(resolveMarkdownUrl(file, 'image.png')).toBe('/ipfs/QmCID/docs/image.png')
    expect(resolveMarkdownUrl(file, './image.png')).toBe('/ipfs/QmCID/docs/image.png')
  })

  it('resolves parent-relative paths', () => {
    expect(resolveMarkdownUrl(file, '../assets/logo.svg')).toBe('/ipfs/QmCID/assets/logo.svg')
  })

  it('percent-encodes resolved segments', () => {
    expect(resolveMarkdownUrl(file, 'my file.png')).toBe('/ipfs/QmCID/docs/my%20file.png')
  })

  it('preserves query and fragment suffixes', () => {
    expect(resolveMarkdownUrl(file, 'page.md#section')).toBe('/ipfs/QmCID/docs/page.md#section')
  })

  it('leaves absolute, protocol-relative, fragment, and allowed-scheme URLs unchanged', () => {
    expect(resolveMarkdownUrl(file, '/ipfs/other/x.png')).toBe('/ipfs/other/x.png')
    expect(resolveMarkdownUrl(file, '//cdn.example/x.png')).toBe('//cdn.example/x.png')
    expect(resolveMarkdownUrl(file, '#heading')).toBe('#heading')
    expect(resolveMarkdownUrl(file, 'https://example.com/a')).toBe('https://example.com/a')
    expect(resolveMarkdownUrl(file, 'ipfs://QmX/a')).toBe('ipfs://QmX/a')
    expect(resolveMarkdownUrl(file, 'mailto:a@b.co')).toBe('mailto:a@b.co')
  })

  it('drops dangerous schemes', () => {
    expect(resolveMarkdownUrl(file, 'javascript:alert(1)')).toBe('')
    expect(resolveMarkdownUrl(file, 'data:text/html,<script>')).toBe('')
    expect(resolveMarkdownUrl(file, 'vbscript:msgbox')).toBe('')
  })
})
