export function formatCount(value: number): string {
  if (!Number.isFinite(value) || value < 0) return '0'
  if (value < 1000) return String(value)
  const units = ['', 'K', 'M', 'B', 'T']
  let scaled = value
  let unit = 0
  while (scaled >= 1000 && unit < units.length - 1) {
    scaled /= 1000
    unit += 1
  }
  return `${scaled >= 100 ? Math.round(scaled) : scaled.toFixed(1)}${units[unit]}`
}

export function formatBytes(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  let scaled = value
  let unit = 0
  while (scaled >= 1024 && unit < units.length - 1) {
    scaled /= 1024
    unit += 1
  }
  const rounded = unit === 0 ? String(scaled) : scaled >= 100 ? Math.round(scaled).toString() : scaled.toFixed(1)
  return `${rounded} ${units[unit]}`
}
