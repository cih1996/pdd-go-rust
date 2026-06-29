export type TaskMode = 'mock' | 'live'
export type TemplateType = 'account_risk' | 'fail_release' | 'success_image' | 'click_image'
export type RecognitionEngine = 'opencv' | 'ocr'

export interface DeviceStats {
  total: number
  success: number
  failure: number
}

export interface TaskProgress {
  task_id: string
  task_mode: string
  started_at: string
  loop_count: number
  current_stage: string
  current_message: string
  last_matched_template?: string | null
  last_matched_template_type?: TemplateType | null
  last_matched_recognition_engine?: RecognitionEngine | null
  click_capture_url?: string | null
  url_template_id?: string | null
  url_template_index?: number | null
  url_template_total?: number | null
}

export interface DeviceInfo {
  serial: string
  status: string
  connected: boolean
  running: boolean
  stats: DeviceStats
  selected_url_template_ids?: string[]
  current_task?: TaskProgress | null
}

export interface CropRegion {
  x: number
  y: number
  width?: number | null
  height?: number | null
}

export interface DebugCaptureResult {
  device_id: string
  capture_url: string
}

export interface TemplateRecord {
  id: string
  label: string
  template_type: TemplateType
  recognition_engine: RecognitionEngine
  priority: number
  expected_text?: string | null
  requires_click?: boolean
  match_once_per_task?: boolean
  image_name?: string | null
  image_url?: string | null
  threshold: number
  method: 'ccoeff_normed' | 'ccorr_normed' | 'sqdiff_normed'
  grayscale: boolean
  crop?: CropRegion | null
  enabled: boolean
  created_at: string
}

export interface DetailRecord {
  id: string
  timestamp: string
  task_id: string
  upstream_task_ref?: string | null
  task_mode: string
  device_id: string
  goods_id?: string | null
  sku_id?: string | null
  url?: string | null
  status: string
  recognition: string
  image_count: number
  capture_url?: string | null
  capture_urls?: string[]
  message?: string | null
  submit_status_code?: number | null
  submit_error?: string | null
  template_id?: string | null
  template_label?: string | null
  recognition_engine?: RecognitionEngine | null
  adb_command?: string | null
}

export interface TaskEvent {
  id: string
  timestamp: string
  device_id?: string | null
  level: 'info' | 'warning' | 'error'
  message: string
  payload: Record<string, unknown>
}

export interface DashboardSummary {
  total: number
  success: number
  failure: number
  daily?: Record<string, { total: number; success: number; failure: number }>
}

export interface PendingTaskRecord {
  task_id: string
  upstream_task_ref?: string | null
  source_code?: string | null
  source_name?: string | null
  account_id?: string | null
  account_name?: string | null
  task_items?: Array<{
    goods_id?: string | null
    sku_id?: string | null
    step_index?: number | null
  }>
  item_count: number
  total_item_count?: number | null
  pending_count?: number | null
  active_count?: number | null
  completed_count?: number | null
  status?: string | null
  prefetched_at?: string | null
}

export interface AdapterSubmitLogRecord {
  id: string
  timestamp: string
  action: string
  request_method: string
  endpoint: string
  task_id?: string | null
  upstream_task_ref?: string | null
  source_code?: string | null
  device_id?: string | null
  submit_type?: string | null
  request_payload?: unknown
  response_status?: number | null
  response_payload?: unknown
  error?: string | null
}

export interface UrlTemplateRecord {
  id: string
  name?: string | null
  template: string
  trigger_count: number
  success_count: number
  risk_count: number
}

export interface SystemConfig {
  open_url_delay_seconds: number
  click_image_delay_seconds: number
  max_task_sku_count: number
  external_api_enabled: boolean
  use_url_templates: boolean
  url_templates: UrlTemplateRecord[]
}

export interface PlatformAccountStats {
  fetched_count: number
  reported_success_count: number
  reported_failure_count: number
}

export interface PlatformAccountRecord {
  id: string
  name: string
  upstream_code: string
  upstream_type: string
  enabled: boolean
  notes?: string | null
  created_at: string
  stats: PlatformAccountStats
  bound_device_ids: string[]
}

export interface PlatformAccountTestResult {
  success: boolean
  fetched: boolean
  released: boolean
  upstream_code: string
  upstream_type: string
  account_id: string
  account_name: string
  task_id?: string | null
  upstream_task_ref?: string | null
  item_count?: number | null
  message: string
}

export interface UpstreamOption {
  code: string
  name: string
  upstream_type: string
  enabled: boolean
}

export interface UpstreamConfigStats {
  fetched_count: number
  reported_success_count: number
  reported_failure_count: number
}

export interface UpstreamConfigRecord {
  id: string
  name: string
  code: string
  upstream_type: 'mock_upstream' | 'laoqian_worker' | 'custom_http'
  enabled: boolean
  priority: number
  base_url: string
  proxy_url?: string | null
  fetch_path?: string | null
  report_success_path?: string | null
  report_failure_path?: string | null
  headers: Record<string, string>
  notes?: string | null
  created_at: string
  stats: UpstreamConfigStats
}

export interface MockDataImportResult {
  imported_count: number
  total_count: number
  replace_existing: boolean
}

export interface ServiceLinkStatus {
  key: string
  name: string
  url: string
  healthy: boolean
  message?: string | null
}

export interface AdapterMockDataStatus {
  imported_total: number
  remaining_total: number
  consumed_total: number
}

export interface AdapterSnapshot {
  id: string
  timestamp: string
  action: string
  status: string
  source_code?: string | null
  task_id?: string | null
  upstream_task_ref?: string | null
  message?: string | null
  payload: Record<string, unknown>
}

export interface AdapterStatePayload {
  recent_logs: TaskEvent[]
  recent_reports: Array<Record<string, unknown>>
  mock_data_status: AdapterMockDataStatus
  recent_snapshots: AdapterSnapshot[]
}

export interface DesktopUpdateStatus {
  manifest_url: string
  app_version: string
  current_version: string
  current_source: 'bundled' | 'updated'
  pending_version?: string | null
  update_ready: boolean
  checking: boolean
  last_checked_at?: string | null
  last_error?: string | null
}

export interface DashboardState {
  devices: DeviceInfo[]
  templates: TemplateRecord[]
  summary: DashboardSummary
  event_log: TaskEvent[]
  pending_tasks: PendingTaskRecord[]
  adapter_submit_logs: AdapterSubmitLogRecord[]
  system_config: SystemConfig
  upstream_configs: UpstreamConfigRecord[]
  platform_accounts: PlatformAccountRecord[]
  upstream_options: UpstreamOption[]
  service_links: ServiceLinkStatus[]
  adapter_state?: AdapterStatePayload | null
}

export interface DetailListResponse {
  details: DetailRecord[]
  total: number
  offset: number
  limit: number
  has_more: boolean
  summary: DashboardSummary
}

export interface DebugResult {
  task_id: string
  matched: boolean
  should_stop: boolean
  detail: DetailRecord
  opencv_results: DebugTemplateResult[]
  timing?: DebugTimingSummary
}

export interface DebugTemplateResult {
  template_id: string
  template_label: string
  template_type: TemplateType
  recognition_engine: RecognitionEngine
  loop_count: number
  stage_name: string
  request_elapsed_ms: number
  ocr_result?: {
    matched_text?: string
    full_text?: string
    expected_tokens?: string[]
    used_cache?: boolean
    executed?: boolean
    ocr_exec_elapsed_ms?: number
    results?: Array<{
      text: string
      confidence: number
      box?: Array<[number, number]> | number[][]
      bounding_box?: Array<[number, number]> | number[][]
    }>
  } | null
  match: {
    found: boolean
    confidence: number
    elapsed_ms: number
    threshold: number
    method: 'ccoeff_normed' | 'ccorr_normed' | 'sqdiff_normed' | 'ocr'
    top_left?: [number, number] | null
    center?: [number, number] | null
    width?: number | null
    height?: number | null
    search_region?: [number, number, number, number] | null
    matched_text?: string | null
    full_text?: string | null
    candidate_texts?: string[]
    ocr_used_cache?: boolean
    ocr_executed?: boolean
    ocr_exec_elapsed_ms?: number
  }
}

export interface DebugTimingSummary {
  total_elapsed_ms: number
  open_url_elapsed_ms?: number | null
  capture_steps: {
    loop_count: number
    elapsed_ms: number
  }[]
}

export interface DebugRunStreamEvent {
  request_id: string
  loop_count?: number
  stage_key?: string
  stage_name?: string
  capture_url?: string
  elapsed_ms?: number
  request_elapsed_ms?: number
  template_count?: number
  task_id?: string
  mode?: 'url' | 'current'
  url?: string
  max_loops?: number
  message?: string
  center?: [number, number] | null
  template_id?: string
  template_label?: string
  template_type?: TemplateType
  recognition_engine?: RecognitionEngine
  found?: boolean
  confidence?: number
  matched_text?: string | null
  ocr_used_cache?: boolean
  ocr_executed?: boolean
  ocr_exec_elapsed_ms?: number
  result?: DebugResult
}

export interface TemplateTestResult {
  template: TemplateRecord
  match: DebugTemplateResult['match']
  capture_url: string
  recognition_engine: RecognitionEngine
  ocr_result?: DebugTemplateResult['ocr_result']
}

export interface DebugSelectionTestResult {
  recognition_engine?: RecognitionEngine
  match: DebugTemplateResult['match']
  search_crop?: CropRegion | null
  ocr_result?: DebugTemplateResult['ocr_result']
}
