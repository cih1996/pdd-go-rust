<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import {
  captureDebugScreen,
  clearDetails,
  connectDevice,
  createTemplate,
  createUpstreamConfig,
  deleteUpstreamConfig,
  deletePlatformAccount,
  deleteTemplate,
  exportTemplates,
  fetchState,
  getAssetUrl,
  getWsUrl,
  importTemplates,
  importPlatformAccounts,
  moveTemplate,
  runDebug,
  runDebugOcrSelectionTest,
  runDebugSelectionTest,
  startTasks,
  stopTasks,
  testTemplate,
  togglePlatformAccount,
  toggleUpstreamConfig,
  updateTemplate,
  updateUpstreamConfig,
  updateSystemConfig,
} from './api'
import AdapterSubmitLogTab from './components/AdapterSubmitLogTab.vue'
import DebugTab from './components/DebugTab.vue'
import DetailTab from './components/DetailTab.vue'
import PlatformAccountTab from './components/PlatformAccountTab.vue'
import SystemConfigTab from './components/SystemConfigTab.vue'
import TaskTab from './components/TaskTab.vue'
import TemplateTab from './components/TemplateTab.vue'
import UpstreamConfigTab from './components/UpstreamConfigTab.vue'
import { normalizeApiDateString } from './utils/datetime'
import type {
  AdapterSubmitLogRecord,
  DashboardState,
  DebugCaptureResult,
  DebugResult,
  DebugSelectionTestResult,
  DesktopUpdateStatus,
  DetailRecord,
  DeviceInfo,
  PlatformAccountRecord,
  PendingTaskRecord,
  ServiceLinkStatus,
  SystemConfig,
  TaskEvent,
  TemplateRecord,
  TemplateTestResult,
  UpstreamConfigRecord,
} from './types'

import { ElMessage, ElMessageBox } from 'element-plus'

const activeTab = ref('task')
const rangeKey = ref('today')
const loading = ref(false)
const savingTemplate = ref(false)
const testingTemplateId = ref('')
const debugRunning = ref(false)
const debugCapturing = ref(false)
const debugSelectionTesting = ref(false)
const connecting = ref(false)
const clearingDetails = ref(false)
const savingSystemConfig = ref(false)
const savingUpstreamId = ref('')
const importingAccounts = ref(false)
const savingAccountId = ref('')
const launchingServiceKey = ref('')
const desktopUpdateLoading = ref(false)
const importingTemplatePack = ref(false)
const connectEndpoint = ref('')
const selectedDevices = ref<string[]>([])
const inspectedDeviceId = ref('')
const debugResult = ref<DebugResult | null>(null)
const debugCapture = ref<DebugCaptureResult | null>(null)
const debugSelectionResult = ref<DebugSelectionTestResult | null>(null)
const templateTestResult = ref<TemplateTestResult | null>(null)
const errorMessage = ref('')
const state = ref<DashboardState>({
  devices: [],
  templates: [],
  details: [],
  summary: { total: 0, success: 0, failure: 0 },
  event_log: [],
  pending_tasks: [],
  adapter_submit_logs: [],
  system_config: {
    open_url_delay_seconds: 2,
    click_image_delay_seconds: 1.2,
    max_task_sku_count: 0,
    use_url_templates: false,
    url_templates: [],
  },
  upstream_configs: [],
  platform_accounts: [],
  upstream_options: [],
  service_links: [],
})

let ws: WebSocket | null = null
let wsReconnectTimer: number | null = null
let statePollTimer: number | null = null
let wsManuallyClosed = false
const wsConnected = ref(false)

const devices = computed<DeviceInfo[]>(() => state.value.devices)
const templates = computed<TemplateRecord[]>(() => state.value.templates)
const details = computed<DetailRecord[]>(() => state.value.details)
const eventLog = computed<TaskEvent[]>(() => state.value.event_log)
const pendingTasks = computed<PendingTaskRecord[]>(() => state.value.pending_tasks)
const adapterSubmitLogs = computed<AdapterSubmitLogRecord[]>(() => state.value.adapter_submit_logs)
const upstreamConfigs = computed<UpstreamConfigRecord[]>(() => state.value.upstream_configs)
const platformAccounts = computed<PlatformAccountRecord[]>(() => state.value.platform_accounts)
const serviceLinks = computed<ServiceLinkStatus[]>(() => state.value.service_links)
const isElectron = window.desktopApp?.isElectron === true
const desktopUpdateStatus = ref<DesktopUpdateStatus | null>(null)
const selectedDeviceRecords = computed(() => devices.value.filter((item) => selectedDevices.value.includes(item.serial)))
const selectedAllRunning = computed(
  () => selectedDeviceRecords.value.length > 0 && selectedDeviceRecords.value.every((item) => item.running),
)
let disposeDesktopUpdateListener: (() => void) | null = null

function normalizeDetail(detail: DetailRecord): DetailRecord {
  return {
    ...detail,
    timestamp: normalizeApiDateString(detail.timestamp),
    capture_url: getAssetUrl(detail.capture_url),
    capture_urls: (detail.capture_urls ?? (detail.capture_url ? [detail.capture_url] : [])).map((item) => getAssetUrl(item)),
  }
}

function normalizeEvent(event: TaskEvent): TaskEvent {
  return {
    ...event,
    timestamp: normalizeApiDateString(event.timestamp),
  }
}

function normalizeAdapterSubmitLog(log: AdapterSubmitLogRecord): AdapterSubmitLogRecord {
  return {
    ...log,
    timestamp: normalizeApiDateString(log.timestamp),
  }
}

function normalizeState(nextState: DashboardState): DashboardState {
  return {
    ...nextState,
    devices: nextState.devices.map((device) => ({
      ...device,
      current_task: device.current_task ? {
        ...device.current_task,
        started_at: normalizeApiDateString(device.current_task.started_at)
      } : null
    })),
    event_log: nextState.event_log.map(normalizeEvent),
    pending_tasks: (nextState.pending_tasks ?? []).map((item) => ({
      ...item,
      prefetched_at: normalizeApiDateString(item.prefetched_at),
    })),
    adapter_submit_logs: (nextState.adapter_submit_logs ?? []).map(normalizeAdapterSubmitLog),
    templates: nextState.templates.map((item) => ({ ...item, image_url: getAssetUrl(item.image_url) })),
    details: nextState.details.map(normalizeDetail),
    service_links: nextState.service_links ?? [],
  }
}

function mergeState(nextState: DashboardState) {
  state.value = normalizeState(nextState)
}

async function loadState() {
  loading.value = true
  try {
    mergeState(await fetchState(rangeKey.value))
    errorMessage.value = ''
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '加载失败'
    ElMessage.error(errorMessage.value)
  } finally {
    loading.value = false
  }
}

function clearWsReconnectTimer() {
  if (wsReconnectTimer !== null) {
    window.clearTimeout(wsReconnectTimer)
    wsReconnectTimer = null
  }
}

function scheduleWsReconnect() {
  if (wsManuallyClosed || wsReconnectTimer !== null) return
  wsReconnectTimer = window.setTimeout(() => {
    wsReconnectTimer = null
    connectWs()
  }, 2000)
}

function startStatePoll() {
  if (statePollTimer !== null) return
  statePollTimer = window.setInterval(() => {
    if (!wsConnected.value && !loading.value) {
      void loadState()
    }
  }, 3000)
}

function stopStatePoll() {
  if (statePollTimer !== null) {
    window.clearInterval(statePollTimer)
    statePollTimer = null
  }
}

function connectWs() {
  clearWsReconnectTimer()
  ws?.close()
  ws = new WebSocket(getWsUrl())
  ws.onopen = () => {
    wsConnected.value = true
    void loadState()
  }
  ws.onmessage = (event) => {
    const payload = JSON.parse(event.data) as { type: string; data: DashboardState | TaskEvent | DetailRecord }
    if (payload.type === 'state') {
      mergeState(payload.data as DashboardState)
    } else if (payload.type === 'event') {
      state.value.event_log = [normalizeEvent(payload.data as TaskEvent), ...state.value.event_log].slice(0, 200)
    } else if (payload.type === 'detail') {
      state.value.details = [normalizeDetail(payload.data as DetailRecord), ...state.value.details].slice(0, 200)
    }
  }
  ws.onerror = () => {
    wsConnected.value = false
  }
  ws.onclose = () => {
    wsConnected.value = false
    ws = null
    scheduleWsReconnect()
  }
}

function toggleDevice(deviceId: string) {
  if (selectedDevices.value.includes(deviceId)) {
    selectedDevices.value = selectedDevices.value.filter((item) => item !== deviceId)
  } else {
    selectedDevices.value = [...selectedDevices.value, deviceId]
  }
}

async function handleConnectDevice() {
  if (!connectEndpoint.value.trim()) return
  connecting.value = true
  try {
    await connectDevice(connectEndpoint.value.trim())
    ElMessage.success('设备连接成功')
    connectEndpoint.value = ''
    await loadState()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '连接失败'
    ElMessage.error(errorMessage.value)
  } finally {
    connecting.value = false
  }
}

async function handleStart() {
  if (!selectedDevices.value.length) {
    ElMessage.warning('请先选择设备')
    return
  }
  try {
    await startTasks(selectedDevices.value, 'mock')
    ElMessage.success('任务已启动')
    await loadState()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '启动失败'
    ElMessage.error(errorMessage.value)
  }
}

async function handleStartSingle(deviceId: string) {
  selectedDevices.value = [deviceId]
  try {
    await startTasks([deviceId], 'mock')
    ElMessage.success(`设备 ${deviceId} 任务已启动`)
    await loadState()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '启动失败'
    ElMessage.error(errorMessage.value)
  }
}

async function handleStop() {
  if (!selectedDevices.value.length) return
  try {
    await stopTasks(selectedDevices.value)
    ElMessage.success('任务已停止')
    await loadState()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '停止失败'
    ElMessage.error(errorMessage.value)
  }
}

async function handleStopSingle(deviceId: string) {
  selectedDevices.value = [deviceId]
  try {
    await stopTasks([deviceId])
    ElMessage.success(`设备 ${deviceId} 任务已停止`)
    await loadState()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '停止失败'
    ElMessage.error(errorMessage.value)
  }
}

async function handleCreateTemplate(payload: FormData) {
  savingTemplate.value = true
  try {
    await createTemplate(payload)
    ElMessage.success('模板创建成功')
    await loadState()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '模板保存失败'
    ElMessage.error(errorMessage.value)
  } finally {
    savingTemplate.value = false
  }
}

async function handleUpdateTemplate(templateId: string, payload: FormData) {
  savingTemplate.value = true
  try {
    await updateTemplate(templateId, payload)
    ElMessage.success('模板更新成功')
    await loadState()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '模板更新失败'
    ElMessage.error(errorMessage.value)
  } finally {
    savingTemplate.value = false
  }
}

async function handleDeleteTemplate(templateId: string) {
  savingTemplate.value = true
  try {
    await deleteTemplate(templateId)
    ElMessage.success('模板删除成功')
    if (templateTestResult.value?.template.id === templateId) {
      templateTestResult.value = null
    }
    await loadState()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '模板删除失败'
    ElMessage.error(errorMessage.value)
  } finally {
    savingTemplate.value = false
  }
}

async function handleMoveTemplate(templateId: string, direction: 'up' | 'down') {
  savingTemplate.value = true
  try {
    const nextTemplates = await moveTemplate(templateId, direction)
    state.value.templates = nextTemplates.map((item) => ({ ...item, image_url: getAssetUrl(item.image_url) }))
    ElMessage.success(direction === 'up' ? '模板已前移' : '模板已后移')
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '模板排序失败'
    ElMessage.error(errorMessage.value)
  } finally {
    savingTemplate.value = false
  }
}

function handleExportTemplates() {
  exportTemplates()
  ElMessage.success('模板包导出已开始')
}

async function handleImportTemplates(payload: { file: File; replaceExisting: boolean }) {
  const formData = new FormData()
  formData.set('package', payload.file)
  formData.set('replace_existing', String(payload.replaceExisting))
  importingTemplatePack.value = true
  try {
    const result = await importTemplates(formData)
    ElMessage.success(`${result.message}，共导入 ${result.imported_count} 个模板`)
    await loadState()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '模板导入失败'
    ElMessage.error(errorMessage.value)
  } finally {
    importingTemplatePack.value = false
  }
}

async function handleTestTemplate(templateId: string, payload: FormData) {
  testingTemplateId.value = templateId
  try {
    const result = await testTemplate(templateId, payload)
    templateTestResult.value = {
      ...result,
      capture_url: getAssetUrl(result.capture_url),
    }
    ElMessage.success('模板测试完成')
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '模板测试失败'
    ElMessage.error(errorMessage.value)
  } finally {
    testingTemplateId.value = ''
  }
}

async function handleRunDebug(payload: { device_id: string; mode: 'url' | 'current'; url?: string }) {
  debugRunning.value = true
  try {
    const result = await runDebug(payload)
    debugResult.value = {
      ...result,
      detail: {
        ...result.detail,
        capture_url: getAssetUrl(result.detail.capture_url),
        capture_urls: (result.detail.capture_urls ?? []).map((item) => getAssetUrl(item)),
      },
    }
    ElMessage.success('调试执行完成')
    await loadState()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '调试执行失败'
    ElMessage.error(errorMessage.value)
  } finally {
    debugRunning.value = false
  }
}

async function handleCaptureDebugScreen(deviceId: string) {
  debugCapturing.value = true
  try {
    const result = await captureDebugScreen(deviceId)
    debugCapture.value = {
      ...result,
      capture_url: getAssetUrl(result.capture_url),
    }
    debugSelectionResult.value = null
    errorMessage.value = ''
    ElMessage.success('抓取截图成功')
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '抓取截图失败'
    ElMessage.error(errorMessage.value)
  } finally {
    debugCapturing.value = false
  }
}

async function handleRunDebugSelectionTest(payload: FormData) {
  debugSelectionTesting.value = true
  try {
    debugSelectionResult.value = await runDebugSelectionTest(payload)
    errorMessage.value = ''
    ElMessage.success('框选找图测试完成')
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '框选找图测试失败'
    ElMessage.error(errorMessage.value)
  } finally {
    debugSelectionTesting.value = false
  }
}

async function handleRunDebugOcrSelectionTest(payload: FormData) {
  debugSelectionTesting.value = true
  try {
    debugSelectionResult.value = await runDebugOcrSelectionTest(payload)
    errorMessage.value = ''
    ElMessage.success('OCR 调试测试完成')
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : 'OCR 调试测试失败'
    ElMessage.error(errorMessage.value)
  } finally {
    debugSelectionTesting.value = false
  }
}

async function handleInspectDevice(deviceId: string) {
  inspectedDeviceId.value = deviceId
  activeTab.value = 'debug'
  await handleCaptureDebugScreen(deviceId)
}

async function handleClearDetails() {
  try {
    await ElMessageBox.confirm('确认清空全部执行明细吗？该操作不可恢复。', '清空确认', {
      type: 'warning',
      confirmButtonText: '清空',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }

  clearingDetails.value = true
  try {
    await clearDetails()
    ElMessage.success('执行明细已清空')
    await loadState()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '清空执行明细失败'
    ElMessage.error(errorMessage.value)
  } finally {
    clearingDetails.value = false
  }
}

async function handleSaveSystemConfig(payload: {
  open_url_delay_seconds: number
  click_image_delay_seconds: number
  max_task_sku_count: number
  use_url_templates: boolean
  url_templates: SystemConfig['url_templates']
}) {
  savingSystemConfig.value = true
  try {
    const nextConfig = await updateSystemConfig(payload)
    state.value.system_config = nextConfig
    ElMessage.success('系统配置已保存')
    await loadState()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '保存系统配置失败'
    ElMessage.error(errorMessage.value)
  } finally {
    savingSystemConfig.value = false
  }
}

async function handleCreateUpstreamConfig(payload: {
  name?: string | null
  upstream_type: 'mock_upstream' | 'laoqian_worker' | 'custom_http'
  enabled?: boolean
  priority?: number
  base_url: string
  token?: string | null
  notes?: string | null
}) {
  savingUpstreamId.value = '__creating__'
  try {
    await createUpstreamConfig(payload)
    ElMessage.success('上游配置创建成功')
    await loadState()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '创建上游配置失败'
    ElMessage.error(errorMessage.value)
  } finally {
    savingUpstreamId.value = ''
  }
}

async function handleUpdateUpstreamConfig(payload: {
  upstreamId: string
  data: {
    name?: string | null
    upstream_type: 'mock_upstream' | 'laoqian_worker' | 'custom_http'
    enabled?: boolean
    priority?: number
    base_url: string
    token?: string | null
    notes?: string | null
  }
}) {
  savingUpstreamId.value = payload.upstreamId
  try {
    await updateUpstreamConfig(payload.upstreamId, payload.data)
    ElMessage.success('上游配置已更新')
    await loadState()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '更新上游配置失败'
    ElMessage.error(errorMessage.value)
  } finally {
    savingUpstreamId.value = ''
  }
}

async function handleToggleUpstreamConfig(payload: { upstreamId: string; enabled: boolean }) {
  savingUpstreamId.value = payload.upstreamId
  try {
    await toggleUpstreamConfig(payload.upstreamId, payload.enabled)
    ElMessage.success(payload.enabled ? '上游配置已启用' : '上游配置已停用')
    await loadState()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '更新上游配置失败'
    ElMessage.error(errorMessage.value)
  } finally {
    savingUpstreamId.value = ''
  }
}

async function handleDeleteUpstreamConfig(upstreamId: string) {
  savingUpstreamId.value = upstreamId
  try {
    await deleteUpstreamConfig(upstreamId)
    ElMessage.success('上游配置已删除')
    await loadState()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '删除上游配置失败'
    ElMessage.error(errorMessage.value)
  } finally {
    savingUpstreamId.value = ''
  }
}

async function handleImportPlatformAccounts(payload: { upstream_code: string; lines: string }) {
  importingAccounts.value = true
  try {
    state.value.platform_accounts = await importPlatformAccounts(payload)
    ElMessage.success('平台账号导入成功')
    await loadState()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '平台账号导入失败'
    ElMessage.error(errorMessage.value)
  } finally {
    importingAccounts.value = false
  }
}

async function handleTogglePlatformAccount(payload: { accountId: string; enabled: boolean }) {
  savingAccountId.value = payload.accountId
  try {
    await togglePlatformAccount(payload.accountId, payload.enabled)
    ElMessage.success(payload.enabled ? '平台账号已启用' : '平台账号已停用')
    await loadState()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '更新平台账号失败'
    ElMessage.error(errorMessage.value)
  } finally {
    savingAccountId.value = ''
  }
}

async function handleDeletePlatformAccount(accountId: string) {
  savingAccountId.value = accountId
  try {
    await deletePlatformAccount(accountId)
    ElMessage.success('平台账号已删除')
    await loadState()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '删除平台账号失败'
    ElMessage.error(errorMessage.value)
  } finally {
    savingAccountId.value = ''
  }
}

async function handleStartDesktopService(serviceKey: 'adapter' | 'opencv' | 'ocr') {
  if (!window.desktopApp?.isElectron) {
    ElMessage.warning('当前仅桌面客户端支持一键启动独立服务')
    return
  }
  launchingServiceKey.value = serviceKey
  try {
    const result = await window.desktopApp.startService(serviceKey)
    ElMessage.success(result.message)
    await new Promise((resolve) => window.setTimeout(resolve, 1800))
    await loadState()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '服务启动失败'
    ElMessage.error(errorMessage.value)
  } finally {
    launchingServiceKey.value = ''
  }
}

async function refreshDesktopUpdateStatus() {
  if (!window.desktopApp?.isElectron) {
    desktopUpdateStatus.value = null
    return
  }
  desktopUpdateStatus.value = await window.desktopApp.getUpdateStatus()
}

async function handleConfigureDesktopUpdate() {
  if (!window.desktopApp?.isElectron) {
    return
  }
  try {
    const result = await ElMessageBox.prompt(
      '请输入热更新清单地址，客户端会读取 version.json 或 manifest.json 并自动下载 update.zip。',
      '配置热更新地址',
      {
        inputValue: desktopUpdateStatus.value?.manifest_url ?? '',
        confirmButtonText: '保存',
        cancelButtonText: '取消',
        inputPlaceholder: 'https://your-domain.example.com/pdd/version.json',
      },
    )
    desktopUpdateLoading.value = true
    desktopUpdateStatus.value = await window.desktopApp.setUpdateManifestUrl(result.value.trim())
    ElMessage.success('热更新地址已保存')
  } catch (error) {
    if (error === 'cancel' || error === 'close') {
      return
    }
    errorMessage.value = error instanceof Error ? error.message : '保存热更新地址失败'
    ElMessage.error(errorMessage.value)
  } finally {
    desktopUpdateLoading.value = false
  }
}

async function handleCheckDesktopUpdate() {
  if (!window.desktopApp?.isElectron) {
    return
  }
  desktopUpdateLoading.value = true
  try {
    const result = await window.desktopApp.checkForUpdates()
    desktopUpdateStatus.value = result.status
    if (result.ok) {
      ElMessage.success(result.message)
    } else {
      ElMessage.warning(result.message)
    }
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '检查热更新失败'
    ElMessage.error(errorMessage.value)
    await refreshDesktopUpdateStatus()
  } finally {
    desktopUpdateLoading.value = false
  }
}

async function handleRestartForDesktopUpdate() {
  if (!window.desktopApp?.isElectron || !desktopUpdateStatus.value?.update_ready) {
    return
  }
  try {
    await ElMessageBox.confirm(
      `检测到待应用版本 ${desktopUpdateStatus.value.pending_version}，确认现在重启客户端并切换吗？`,
      '应用热更新',
      {
        type: 'warning',
        confirmButtonText: '立即重启',
        cancelButtonText: '稍后再说',
      },
    )
  } catch {
    return
  }
  await window.desktopApp.restartForUpdate()
}

onMounted(async () => {
  wsManuallyClosed = false
  startStatePoll()
  await loadState()
  if (window.desktopApp?.isElectron) {
    await refreshDesktopUpdateStatus()
    disposeDesktopUpdateListener = window.desktopApp.onUpdateStatus((status) => {
      desktopUpdateStatus.value = status
    })
  }
  connectWs()
})

onUnmounted(() => {
  wsManuallyClosed = true
  clearWsReconnectTimer()
  stopStatePoll()
  ws?.close()
  ws = null
  disposeDesktopUpdateListener?.()
  disposeDesktopUpdateListener = null
})
</script>

<template>
  <el-container class="dashboard-container">
    <el-aside width="240px" style="background-color: #fff; border-right: 1px solid #e6e6e6;">
      <div style="padding: 24px 20px; border-bottom: 1px solid #f0f2f5;">
        <div class="header-logo">
          <el-icon :size="28" color="#409EFF"><Monitor /></el-icon>
          <span>拼多多中控系统</span>
        </div>
        <div style="margin-top: 8px; font-size: 12px; color: #909399;">
          全自动化任务管理平台
        </div>
      </div>
      
      <el-menu
        :default-active="activeTab"
        style="border-right: none; padding-top: 16px;"
        @select="activeTab = $event"
      >
        <el-menu-item index="task">
          <el-icon><List /></el-icon>
          <span>设备与任务</span>
        </el-menu-item>
        <el-menu-item index="detail">
          <el-icon><DataLine /></el-icon>
          <span>执行明细</span>
        </el-menu-item>
        <el-menu-item index="adapter-log">
          <el-icon><Document /></el-icon>
          <span>适配器交互日志</span>
        </el-menu-item>
        <el-menu-item index="account">
          <el-icon><User /></el-icon>
          <span>平台账号</span>
        </el-menu-item>
        <el-menu-item index="upstream">
          <el-icon><Share /></el-icon>
          <span>上游配置</span>
        </el-menu-item>
        <el-menu-item index="template">
          <el-icon><Picture /></el-icon>
          <span>模板库管理</span>
        </el-menu-item>
        <el-menu-item index="debug">
          <el-icon><Aim /></el-icon>
          <span>单步调试台</span>
        </el-menu-item>
        <el-menu-item index="system">
          <el-icon><Setting /></el-icon>
          <span>系统配置</span>
        </el-menu-item>
      </el-menu>
    </el-aside>

    <el-container>
      <el-header style="background: #fff; border-bottom: 1px solid #e6e6e6; display: flex; align-items: center; justify-content: space-between; padding: 0 24px; height: 64px;">
        <div style="font-size: 16px; font-weight: 500; color: #303133;">
          {{
            activeTab === 'task'
              ? '设备与任务'
              : activeTab === 'detail'
                ? '执行明细'
                : activeTab === 'adapter-log'
                  ? '适配器完整交互日志'
                : activeTab === 'account'
                  ? '平台账号'
                : activeTab === 'upstream'
                  ? '上游配置'
                : activeTab === 'template'
                  ? '模板库管理'
                  : activeTab === 'debug'
                    ? '单步调试台'
                    : '系统配置'
          }}
        </div>
        <div class="flex-row" style="gap: 8px; flex-wrap: wrap; justify-content: flex-end;">
          <el-button
            v-if="isElectron"
            size="small"
            :loading="launchingServiceKey === 'adapter'"
            @click="handleStartDesktopService('adapter')"
          >
            启动适配器
          </el-button>
          <el-button
            v-if="isElectron"
            size="small"
            :loading="launchingServiceKey === 'opencv'"
            @click="handleStartDesktopService('opencv')"
          >
            启动 OpenCV
          </el-button>
          <el-button
            v-if="isElectron"
            size="small"
            :loading="launchingServiceKey === 'ocr'"
            @click="handleStartDesktopService('ocr')"
          >
            启动 OCR
          </el-button>
          <el-button
            v-if="isElectron"
            size="small"
            :loading="desktopUpdateLoading || desktopUpdateStatus?.checking"
            @click="handleConfigureDesktopUpdate"
          >
            配置热更新
          </el-button>
          <el-button
            v-if="isElectron"
            size="small"
            type="primary"
            plain
            :loading="desktopUpdateLoading || desktopUpdateStatus?.checking"
            @click="handleCheckDesktopUpdate"
          >
            检查热更新
          </el-button>
          <el-button
            v-if="isElectron && desktopUpdateStatus?.update_ready"
            size="small"
            type="warning"
            @click="handleRestartForDesktopUpdate"
          >
            重启应用更新
          </el-button>
          <el-tooltip
            v-if="isElectron && desktopUpdateStatus"
            :content="`当前版本：${desktopUpdateStatus.current_version}\n清单地址：${desktopUpdateStatus.manifest_url || '未配置'}${desktopUpdateStatus.last_error ? `\n最近错误：${desktopUpdateStatus.last_error}` : ''}`"
            placement="bottom"
          >
            <el-tag :type="desktopUpdateStatus.update_ready ? 'warning' : 'info'" effect="light" round>
              热更新 · {{ desktopUpdateStatus.current_source === 'updated' ? '已启用更新版' : '内置版' }}
              <template v-if="desktopUpdateStatus.pending_version">
                · 待切换 {{ desktopUpdateStatus.pending_version }}
              </template>
            </el-tag>
          </el-tooltip>
          <el-tooltip
            v-for="item in serviceLinks"
            :key="item.key"
            :content="`${item.url}${item.message ? `\n${item.message}` : ''}`"
            placement="bottom"
          >
            <el-tag :type="item.healthy ? 'success' : 'danger'" effect="light" round>
              {{ item.name }} · {{ item.healthy ? '在线' : '离线' }}
            </el-tag>
          </el-tooltip>
          <el-tag
            :type="loading ? 'warning' : 'success'"
            effect="light"
            round
            class="mr-2"
          >
            {{ loading ? '同步中...' : '已连接服务端' }}
          </el-tag>
          <el-button @click="loadState" :icon="loading ? 'Loading' : 'Refresh'" circle></el-button>
        </div>
      </el-header>

      <el-main style="padding: 24px;">
        <TaskTab
          v-if="activeTab === 'task'"
          :devices="devices"
          :event-log="eventLog"
          :pending-tasks="pendingTasks"
          :selected-devices="selectedDevices"
          :selected-all-running="selectedAllRunning"
          :connect-endpoint="connectEndpoint"
          :connecting="connecting"
          @toggle-device="toggleDevice"
          @select-all="selectedDevices = devices.map((item) => item.serial)"
          @clear-selection="selectedDevices = []"
          @update:connect-endpoint="connectEndpoint = $event"
          @connect-device="handleConnectDevice"
          @start="handleStart"
          @stop="handleStop"
          @start-device="handleStartSingle"
          @stop-device="handleStopSingle"
          @inspect-device="handleInspectDevice"
          @refresh="loadState"
        />

        <AdapterSubmitLogTab
          v-if="activeTab === 'adapter-log'"
          :logs="adapterSubmitLogs"
          @refresh="loadState"
        />

        <DetailTab
          v-if="activeTab === 'detail'"
          :summary="state.summary"
          :details="details"
          :range-key="rangeKey"
          :clearing="clearingDetails"
          @update:range-key="rangeKey = $event; loadState()"
          @clear-details="handleClearDetails"
        />

        <PlatformAccountTab
          v-if="activeTab === 'account'"
          :accounts="platformAccounts"
          :upstream-options="state.upstream_options"
          :importing="importingAccounts"
          :saving-account-id="savingAccountId"
          @import="handleImportPlatformAccounts"
          @toggle="handleTogglePlatformAccount"
          @delete="handleDeletePlatformAccount"
        />

        <UpstreamConfigTab
          v-if="activeTab === 'upstream'"
          :upstreams="upstreamConfigs"
          :saving-upstream-id="savingUpstreamId"
          @create="handleCreateUpstreamConfig"
          @update="handleUpdateUpstreamConfig"
          @toggle="handleToggleUpstreamConfig"
          @delete="handleDeleteUpstreamConfig"
        />

        <TemplateTab
          v-if="activeTab === 'template'"
          :templates="templates"
          :devices="devices"
          :saving="savingTemplate"
          :testing-id="testingTemplateId"
          :test-result="templateTestResult"
          :importing-template-pack="importingTemplatePack"
          @create-template="handleCreateTemplate"
          @update-template="handleUpdateTemplate"
          @delete-template="handleDeleteTemplate"
          @move-template="handleMoveTemplate"
          @test-template="handleTestTemplate"
          @export-templates="handleExportTemplates"
          @import-templates="handleImportTemplates"
        />

        <DebugTab
          v-if="activeTab === 'debug'"
          :devices="devices"
          :result="debugResult"
          :running="debugRunning"
          :capture="debugCapture"
          :capturing="debugCapturing"
          :selection-testing="debugSelectionTesting"
          :selection-result="debugSelectionResult"
          :selected-device-id="inspectedDeviceId"
          @run="handleRunDebug"
          @capture-screen="handleCaptureDebugScreen"
          @run-selection-test="handleRunDebugSelectionTest"
          @run-ocr-selection-test="handleRunDebugOcrSelectionTest"
        />

        <SystemConfigTab
          v-if="activeTab === 'system'"
          :config="state.system_config"
          :saving="savingSystemConfig"
          @save="handleSaveSystemConfig"
        />
      </el-main>
    </el-container>
  </el-container>
</template>
