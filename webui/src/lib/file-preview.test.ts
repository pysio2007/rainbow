import { describe, expect, it } from 'vitest'
import { resolveFilePreviewType } from './file-preview'

describe('resolveFilePreviewType', () => {
  it.each([
    ['/ipfs/cid/photo.JPG', 'image'],
    ['/ipfs/cid/photo.jpg', 'image'],
    ['/ipfs/cid/sound.MP3', 'audio'],
    ['/ipfs/cid/clip.webm', 'video'],
    ['/ipfs/cid/document.PDF', 'pdf'],
    ['/ipfs/cid/archive.tar', 'unknown'],
    ['/ipfs/cid/no-extension', 'unknown'],
  ])('resolves %s as %s', (path, type) => expect(resolveFilePreviewType(path)).toBe(type))
})
