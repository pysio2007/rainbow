export function parseVersionText(contentType: string | null, body: string): string {
  if (!contentType || !/^text\/plain(?:\s*;|$)/i.test(contentType)) return ''
  const value = body.trim()
  if (!value || /<\/?[a-z][^>]*>/i.test(value)) return ''
  return value
}
