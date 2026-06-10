import type {
  DashboardState,
  DebugCaptureResult,
  DebugResult,
  DebugSelectionTestResult,
  DeviceInfo,
  PlatformAccountRecord,
  SystemConfig,
  TemplateRecord,
  TemplateTestResult,
  UpstreamConfigRecord,
} from './types'

const API_BASE = 'http://127.0.0.1:8080'

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${API_BASE}${path}`, init)
  if (!response.ok) {
    const text = await response.text()
    throw new Error(text || 'Request failed')
  }
  return response.json() as Promise<T>
}

export function getWsUrl(): string {
  return 'ws://127.0.0.1:8080/ws/events'
}

export function getAssetUrl(path?: string | null): string {
  if (!path) return ''
  if (/^https?:\/\//.test(path)) return path
  return `${API_BASE}${path}`
}

export function fetchState(rangeKey: string): Promise<DashboardState> {
  return request<DashboardState>(`/api/state?range_key=${rangeKey}`)
}

export function fetchDevices(): Promise<DeviceInfo[]> {
  return request<DeviceInfo[]>('/api/devices')
}

export function fetchTemplates(): Promise<TemplateRecord[]> {
  return request<TemplateRecord[]>('/api/templates')
}

export function connectDevice(endpoint: string): Promise<{ message: string }> {
  return request('/api/devices/connect', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ endpoint }),
  })
}

export function startTasks(deviceIds: string[], mode: 'mock' | 'live'): Promise<{ started: string[]; skipped: string[] }> {
  return request('/api/tasks/start', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ device_ids: deviceIds, mode }),
  })
}

export function stopTasks(deviceIds: string[]): Promise<{ stopped: string[]; missing: string[] }> {
  return request('/api/tasks/stop', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ device_ids: deviceIds }),
  })
}

export function exportTemplates(): void {
  window.open(`${API_BASE}/api/templates/export`, '_blank')
}

export function importTemplates(formData: FormData): Promise<{ message: string; imported_count: number; replace_existing: boolean }> {
  return request('/api/templates/import', {
    method: 'POST',
    body: formData,
  })
}

export function clearDetails(): Promise<{ message: string }> {
  return request('/api/details', {
    method: 'DELETE',
  })
}

export function updateSystemConfig(payload: SystemConfig): Promise<SystemConfig> {
  return request('/api/system-config', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export function createUpstreamConfig(payload: {
  name?: string | null
  upstream_type: 'mock_upstream' | 'laoqian_worker' | 'custom_http'
  enabled?: boolean
  priority?: number
  base_url: string
  token?: string | null
  notes?: string | null
}): Promise<UpstreamConfigRecord> {
  return request('/api/upstreams', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export function updateUpstreamConfig(upstreamId: string, payload: {
  name?: string | null
  upstream_type: 'mock_upstream' | 'laoqian_worker' | 'custom_http'
  enabled?: boolean
  priority?: number
  base_url: string
  token?: string | null
  notes?: string | null
}): Promise<UpstreamConfigRecord> {
  return request(`/api/upstreams/${upstreamId}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export function toggleUpstreamConfig(upstreamId: string, enabled: boolean): Promise<UpstreamConfigRecord> {
  return request(`/api/upstreams/${upstreamId}/toggle`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ enabled }),
  })
}

export function deleteUpstreamConfig(upstreamId: string): Promise<{ message: string }> {
  return request(`/api/upstreams/${upstreamId}`, {
    method: 'DELETE',
  })
}

export function importPlatformAccounts(payload: {
  upstream_code: string
  lines: string
  enabled?: boolean
}): Promise<PlatformAccountRecord[]> {
  return request('/api/platform-accounts/import', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export function togglePlatformAccount(accountId: string, enabled: boolean): Promise<PlatformAccountRecord> {
  return request(`/api/platform-accounts/${accountId}/toggle`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ enabled }),
  })
}

export function deletePlatformAccount(accountId: string): Promise<{ message: string }> {
  return request(`/api/platform-accounts/${accountId}`, {
    method: 'DELETE',
  })
}

export function createTemplate(formData: FormData): Promise<TemplateRecord> {
  return request('/api/templates', {
    method: 'POST',
    body: formData,
  })
}

export function updateTemplate(templateId: string, formData: FormData): Promise<TemplateRecord> {
  return request(`/api/templates/${templateId}`, {
    method: 'PUT',
    body: formData,
  })
}

export function deleteTemplate(templateId: string): Promise<{ message: string }> {
  return request(`/api/templates/${templateId}`, {
    method: 'DELETE',
  })
}

export function moveTemplate(templateId: string, direction: 'up' | 'down'): Promise<TemplateRecord[]> {
  const formData = new FormData()
  formData.set('direction', direction)
  return request(`/api/templates/${templateId}/move`, {
    method: 'POST',
    body: formData,
  })
}

export function testTemplate(templateId: string, formData: FormData): Promise<TemplateTestResult> {
  return request(`/api/templates/${templateId}/test`, {
    method: 'POST',
    body: formData,
  })
}

export function runDebug(payload: {
  device_id: string
  mode: 'url' | 'current'
  url?: string
}): Promise<DebugResult> {
  return request('/api/debug/run', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export function captureDebugScreen(deviceId: string): Promise<DebugCaptureResult> {
  return request('/api/debug/capture', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ device_id: deviceId }),
  })
}

export function runDebugSelectionTest(formData: FormData): Promise<DebugSelectionTestResult> {
  return request('/api/debug/match-selection', {
    method: 'POST',
    body: formData,
  })
}

export function runDebugOcrSelectionTest(formData: FormData): Promise<DebugSelectionTestResult> {
  return request('/api/debug/ocr-selection', {
    method: 'POST',
    body: formData,
  })
}
