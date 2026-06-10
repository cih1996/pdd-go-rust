export function normalizeApiDateString(value?: string | null): string {
  const raw = value?.trim()
  if (!raw) return ''

  let normalized = raw.replace(/\.(\d{3})\d+(?=(Z|[+-]\d{2}:?\d{2})$)/i, '.$1')
  if (!/(Z|[+-]\d{2}:?\d{2})$/i.test(normalized)) {
    normalized = normalized.replace(/\.(\d{3})\d+$/, '.$1')
    normalized += 'Z'
  }
  return normalized
}

export function parseApiDate(value?: string | null): Date | null {
  const normalized = normalizeApiDateString(value)
  if (!normalized) return null
  const parsed = new Date(normalized)
  return Number.isNaN(parsed.getTime()) ? null : parsed
}

export function formatApiDateTime(value?: string | null): string {
  const parsed = parseApiDate(value)
  if (!parsed) return '-'
  return parsed.toLocaleString('zh-CN', { hour12: false })
}
