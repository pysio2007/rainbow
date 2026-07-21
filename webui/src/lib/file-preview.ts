export type FilePreviewType = 'image' | 'audio' | 'video' | 'pdf' | 'unknown'

const extensions: Record<Exclude<FilePreviewType, 'unknown'>, string[]> = {
  image: ['avif', 'gif', 'jpeg', 'jpg', 'png', 'svg', 'webp'],
  audio: ['aac', 'flac', 'm4a', 'mp3', 'ogg', 'wav'],
  video: ['m4v', 'mov', 'mp4', 'ogv', 'webm'],
  pdf: ['pdf'],
}

export function resolveFilePreviewType(path: string): FilePreviewType {
  const name = path.split('/').pop() || ''
  const extension = name.split('.').pop()?.toLowerCase() || ''
  for (const [type, supported] of Object.entries(extensions)) {
    if (supported.includes(extension)) return type as Exclude<FilePreviewType, 'unknown'>
  }
  return 'unknown'
}
