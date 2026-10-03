/** 展示层通用格式化工具。原先每个页面各写一份，这里收敛避免重复。 */

export function formatSize(bytes: number): string {
  if (!bytes || bytes < 0) return '0 B'
  const k = 1024
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(k)))
  return `${(bytes / Math.pow(k, i)).toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

export function formatTime(iso: string): string {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return iso
  return d.toLocaleString()
}
