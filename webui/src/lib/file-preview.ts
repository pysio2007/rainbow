export type FilePreviewType = 'image' | 'audio' | 'video' | 'pdf' | 'markdown' | 'text' | 'unknown'

const extensions: Record<Exclude<FilePreviewType, 'unknown'>, string[]> = {
  image: ['avif', 'gif', 'jpeg', 'jpg', 'png', 'svg', 'webp'],
  audio: ['aac', 'flac', 'm4a', 'mp3', 'ogg', 'wav'],
  video: ['m4v', 'mov', 'mp4', 'ogv', 'webm'],
  pdf: ['pdf'],
  markdown: ['md', 'markdown', 'mdown', 'mkd'],
  text: [
    'txt', 'text', 'log', 'csv', 'tsv', 'json', 'ndjson', 'xml', 'yaml', 'yml', 'toml', 'ini', 'conf', 'env',
    'js', 'mjs', 'cjs', 'jsx', 'ts', 'tsx', 'go', 'py', 'rs', 'rb', 'c', 'h', 'cpp', 'hpp', 'cc', 'java', 'kt',
    'swift', 'php', 'sh', 'bash', 'zsh', 'sql', 'css', 'scss', 'less', 'dockerfile', 'makefile',
  ],
}

export function resolveFilePreviewType(path: string): FilePreviewType {
  const name = path.split('/').pop() || ''
  const extension = name.split('.').pop()?.toLowerCase() || ''
  for (const [type, supported] of Object.entries(extensions)) {
    if (supported.includes(extension)) return type as Exclude<FilePreviewType, 'unknown'>
  }
  return 'unknown'
}

// isTextContentType reports whether a gateway Content-Type header should be
// treated as previewable text. Used as a fallback when the file extension is
// unrecognized. HTML is deliberately excluded (rendered as source elsewhere or
// not at all) to avoid ambiguity with active content.
export function isTextContentType(contentType: string | null): boolean {
  if (!contentType) return false
  const type = contentType.split(';')[0].trim().toLowerCase()
  if (!type) return false
  if (type === 'text/html' || type === 'application/xhtml+xml') return false
  if (type.startsWith('text/')) return true
  return [
    'application/json',
    'application/ld+json',
    'application/xml',
    'application/javascript',
    'application/x-yaml',
    'application/yaml',
    'application/toml',
  ].includes(type)
}
