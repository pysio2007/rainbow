import { describe, expect, it } from 'vitest'
import { isTextContentType, resolveFilePreviewType } from './file-preview'

describe('resolveFilePreviewType', () => {
  it.each([
    ['/ipfs/cid/photo.JPG', 'image'],
    ['/ipfs/cid/photo.jpg', 'image'],
    ['/ipfs/cid/sound.MP3', 'audio'],
    ['/ipfs/cid/clip.webm', 'video'],
    ['/ipfs/cid/document.PDF', 'pdf'],
    ['/ipfs/cid/readme.md', 'markdown'],
    ['/ipfs/cid/NOTES.Markdown', 'markdown'],
    ['/ipfs/cid/data.json', 'text'],
    ['/ipfs/cid/main.go', 'text'],
    ['/ipfs/cid/script.TS', 'text'],
    ['/ipfs/cid/notes.txt', 'text'],
    ['/ipfs/cid/archive.tar', 'unknown'],
    ['/ipfs/cid/no-extension', 'unknown'],
  ])('resolves %s as %s', (path, type) => expect(resolveFilePreviewType(path)).toBe(type))
})

describe('isTextContentType', () => {
  it.each([
    ['text/plain; charset=utf-8', true],
    ['text/markdown', true],
    ['application/json', true],
    ['application/xml', true],
    ['application/javascript; charset=utf-8', true],
  ])('accepts %s', (value, expected) => expect(isTextContentType(value)).toBe(expected))

  it.each([
    ['text/html; charset=utf-8', false],
    ['application/xhtml+xml', false],
    ['image/png', false],
    ['application/octet-stream', false],
    [null, false],
    ['', false],
  ])('rejects %s', (value, expected) => expect(isTextContentType(value)).toBe(expected))
})
