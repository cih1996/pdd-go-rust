import type {
  DashboardState,
  DetailListResponse,
  DebugCaptureResult,
  MockDataImportResult,
  DebugSelectionTestResult,
  DeviceInfo,
  PlatformAccountTestResult,
  PlatformAccountRecord,
  SystemConfig,
  TemplateRecord,
  TemplateTestResult,
  UpstreamConfigRecord,
} from './types'

const API_BASE = 'http://127.0.0.1:18080'

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${API_BASE}${path}`, init)
  if (!response.ok) {
    const text = await response.text()
    throw new Error(extractErrorMessage(text) || 'Request failed')
  }
  return response.json() as Promise<T>
}

function extractErrorMessage(text: string): string {
  const raw = text.trim()
  if (!raw) return ''
  try {
    const payload = JSON.parse(raw) as Record<string, unknown>
    const detail = typeof payload.detail === 'string' ? payload.detail.trim() : ''
    const error = typeof payload.error === 'string' ? payload.error.trim() : ''
    return detail || error || raw
  } catch {
    return raw
  }
}

export function getWsUrl(): string {
  return 'ws://127.0.0.1:18080/ws/events'
}

export function sendWsMessage(socket: WebSocket | null | undefined, type: string, data: Record<string, unknown>) {
  if (!socket || socket.readyState !== WebSocket.OPEN) {
    throw new Error('WebSocket 未连接')
  }
  socket.send(JSON.stringify({ type, data }))
}

export function getAssetUrl(path?: string | null): string {
  if (!path) return ''
  if (/^https?:\/\//.test(path)) return path
  return `${API_BASE}${path}`
}

export function fetchState(rangeKey: string): Promise<DashboardState> {
  return request<DashboardState>(`/api/state?range_key=${rangeKey}`)
}

export function fetchDetails(rangeKey: string, limit = 30, offset = 0): Promise<DetailListResponse> {
  return request<DetailListResponse>(
    `/api/details?range_key=${encodeURIComponent(rangeKey)}&limit=${limit}&offset=${offset}`,
  )
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

export function getSubmitCount(): Promise<{ submit_count: number }> {
  return request('/api/tasks/submit-count')
}

export function resetSubmitCount(): Promise<{ submit_count: number }> {
  return request('/api/tasks/submit-count/reset', { method: 'POST' })
}

export function updateDeviceURLTemplates(deviceId: string, templateIds: string[]): Promise<{ message: string; device: DeviceInfo }> {
  return request(`/api/devices/${encodeURIComponent(deviceId)}/url-templates`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ template_ids: templateIds }),
  })
}

export function updateDeviceTaskMode(deviceId: string, modeEx: string): Promise<{ message: string; device: DeviceInfo }> {
  return request(`/api/devices/${encodeURIComponent(deviceId)}/task-mode`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ mode_ex: modeEx }),
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
  proxy_url?: string | null
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
  proxy_url?: string | null
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

export function importMockData(payload: {
  lines: string
  replace_existing?: boolean
}): Promise<MockDataImportResult> {
  return request('/api/mock-data/import', {
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

export function testPlatformAccountFetch(accountId: string): Promise<PlatformAccountTestResult> {
  return request(`/api/platform-accounts/${accountId}/test-fetch`, {
    method: 'POST',
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

export function testUnsavedTemplate(formData: FormData): Promise<TemplateTestResult> {
  return request('/api/templates/test-unsaved', {
    method: 'POST',
    body: formData,
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
