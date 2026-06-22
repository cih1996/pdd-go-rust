<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import type { AdapterSubmitLogRecord, DetailRecord, DeviceInfo, PendingTaskRecord, TaskEvent, UrlTemplateRecord } from '../types'
import { parseApiDate } from '../utils/datetime'

const props = defineProps<{
  devices: DeviceInfo[]
  urlTemplates: UrlTemplateRecord[]
  eventLog: TaskEvent[]
  details: DetailRecord[]
  adapterSubmitLogs: AdapterSubmitLogRecord[]
  pendingTasks: PendingTaskRecord[]
  selectedDevices: string[]
  selectedAllRunning: boolean
  connecting: boolean
  connectEndpoint: string
}>()

const emit = defineEmits<{
  (event: 'toggle-device', value: string): void
  (event: 'select-all'): void
  (event: 'clear-selection'): void
  (event: 'update:connectEndpoint', value: string): void
  (event: 'connect-device'): void
  (event: 'start'): void
  (event: 'stop'): void
  (event: 'start-device', value: string): void
  (event: 'stop-device', value: string): void
  (event: 'inspect-device', value: string): void
  (event: 'save-device-url-templates', value: { device_id: string; template_ids: string[] }): void
  (event: 'refresh'): void
}>()

const searchDevice = ref('')
const searchLog = ref('')
const expandedLogId = ref('')
const deviceTemplateDialogVisible = ref(false)
const editingDeviceId = ref('')
const editingDeviceTemplateIDs = ref<string[]>([])
const TASK_WARNING_SECONDS = 6
const TASK_DANGER_SECONDS = 10

function taskElapsedSeconds(row: DeviceInfo) {
  if (!hasActiveTask(row) || !row.current_task?.started_at) return 0
  const startedAt = parseApiDate(row.current_task.started_at)
  if (!startedAt) return 0
  return Math.max(0, Math.floor((nowTs.value - startedAt.getTime()) / 1000))
}

function compareASCII(left: string, right: string) {
  if (left === right) return 0
  return left < right ? -1 : 1
}

function formatURLTemplateProgress(row: DeviceInfo) {
  const current = row.current_task?.url_template_index ?? 0
  const total = row.current_task?.url_template_total ?? 0
  if (!total || !current) return ''
  return `URL模板 ${current}/${total}`
}

function selectedURLTemplateCount(row: DeviceInfo) {
  return row.selected_url_template_ids?.length ?? 0
}

function openDeviceTemplateDialog(row: DeviceInfo) {
  editingDeviceId.value = row.serial
  editingDeviceTemplateIDs.value = [...(row.selected_url_template_ids ?? [])]
  deviceTemplateDialogVisible.value = true
}

function saveDeviceTemplateDialog() {
  emit('save-device-url-templates', {
    device_id: editingDeviceId.value,
    template_ids: [...editingDeviceTemplateIDs.value],
  })
  deviceTemplateDialogVisible.value = false
}

function templateDisplayText(item: UrlTemplateRecord, index: number) {
  const trimmed = item.template.trim()
  const preview = trimmed.length > 72 ? `${trimmed.slice(0, 72)}...` : trimmed
  const label = item.name?.trim() || `模板${index + 1}`
  return `${label} · ${preview}`
}

const filteredDevices = computed(() => {
  const keyword = searchDevice.value.trim()
  const base = keyword ? props.devices.filter((d) => d.serial.includes(keyword)) : [...props.devices]
  return [...base].sort((a, b) => compareASCII(a.serial, b.serial))
})

interface TerminalTaskLogItem {
  id: string
  timestamp: string
  device_id?: string | null
  level: TaskEvent['level']
  task_id: string
  upstream_task_ref: string
  recognition_result: string
  recognition_text: string
  message: string
  source_code: string
  goods_id: string
  sku_id: string
  report_payload?: Record<string, unknown> | null
  submit_status: string
  request_payload?: unknown
  response_payload?: unknown
  response_status?: number | null
  endpoint?: string | null
  submit_type?: string | null
  action?: string | null
  error?: string | null
  template_id?: string | null
  template_label?: string | null
  recognition_engine?: 'opencv' | 'ocr' | null
  adb_command?: string | null
}

function toTerminalTaskLog(event: TaskEvent): TerminalTaskLogItem | null {
  const payload = event.payload ?? {}
  if (payload.log_kind !== 'task_terminal') return null
  return {
    id: event.id,
    timestamp: event.timestamp,
    device_id: event.device_id,
    level: event.level,
    task_id: String(payload.task_id ?? ''),
    upstream_task_ref: String(payload.upstream_task_ref ?? ''),
    recognition_result: String(payload.recognition_type ?? '-'),
    recognition_text: String(payload.recognition_content ?? '-'),
    message: String(payload.message ?? ''),
    source_code: String(payload.source_code ?? ''),
    goods_id: String(payload.goods_id ?? ''),
    sku_id: String(payload.sku_id ?? ''),
    report_payload:
      payload.report_payload && typeof payload.report_payload === 'object'
        ? (payload.report_payload as Record<string, unknown>)
        : null,
    submit_status: '未提交',
    template_id: typeof payload.template_id === 'string' ? payload.template_id : null,
    template_label: typeof payload.template_label === 'string' ? payload.template_label : null,
    recognition_engine:
      payload.recognition_engine === 'ocr' || payload.recognition_engine === 'opencv'
        ? payload.recognition_engine
        : null,
    adb_command: typeof payload.adb_command === 'string' ? payload.adb_command : null,
  }
}

function formatRecognitionResult(detail: DetailRecord) {
  const mapping: Record<string, string> = {
    success_image: '成功',
    fail_release: '失败释放',
    account_risk: '账号风控',
    open_url_failed: '异常停止',
    loop_failed: '异常停止',
  }
  return mapping[detail.recognition] || (detail.status === 'success' ? '成功' : detail.status === 'failure' ? '失败' : detail.recognition || '-')
}

function formatRecognitionText(detail: DetailRecord) {
  return detail.template_label || detail.message || detail.recognition || '-'
}

function toTerminalTaskLogFromDetail(detail: DetailRecord): TerminalTaskLogItem {
  return {
    id: detail.id,
    timestamp: detail.timestamp,
    device_id: detail.device_id,
    level: detail.status === 'success' ? 'info' : 'warning',
    task_id: detail.task_id,
    upstream_task_ref: detail.upstream_task_ref || '',
    recognition_result: formatRecognitionResult(detail),
    recognition_text: formatRecognitionText(detail),
    message: detail.message || '',
    source_code: '',
    goods_id: detail.goods_id || '',
    sku_id: detail.sku_id || '',
    report_payload: null,
    submit_status: '未提交',
    template_id: detail.template_id || '',
    template_label: detail.template_label || '',
    recognition_engine: detail.recognition_engine ?? inferRecognitionEngine(detail),
    adb_command: detail.adb_command || '',
  }
}

function inferRecognitionEngine(detail: DetailRecord): 'opencv' | 'ocr' | null {
  if (!detail.template_label && !detail.template_id) return null
  const text = `${detail.template_label || ''} ${detail.message || ''}`.toLowerCase()
  if (text.includes('ocr')) return 'ocr'
  return 'opencv'
}

function adapterSubmitStatus(item: AdapterSubmitLogRecord) {
  if (item.error) return '提交失败'
  if ((item.response_status ?? 0) >= 400) return '提交失败'
  if (item.response_status && item.response_status >= 200) return '提交成功'
  return '提交中'
}

function buildSubmitLogKey(taskId?: string | null, upstreamTaskRef?: string | null, deviceId?: string | null) {
  return [taskId || '', upstreamTaskRef || '', deviceId || ''].join('::')
}

const filteredLogs = computed(() => {
  const submitLogMap = new Map<string, AdapterSubmitLogRecord>()
  props.adapterSubmitLogs
    .filter((item) => item.action === 'submit-task')
    .sort((a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime())
    .forEach((item) => {
      const key = buildSubmitLogKey(item.task_id, item.upstream_task_ref, item.device_id)
      if (!submitLogMap.has(key)) {
        submitLogMap.set(key, item)
      }
    })
  const detailBase = props.details.map(toTerminalTaskLogFromDetail)
  const eventBase = props.eventLog
    .map(toTerminalTaskLog)
    .filter((item): item is TerminalTaskLogItem => Boolean(item))
  const base = (detailBase.length > 0 ? detailBase : eventBase)
    .map((item) => {
      const submitLog = submitLogMap.get(buildSubmitLogKey(item.task_id, item.upstream_task_ref, item.device_id))
      if (!submitLog) return item
      return {
        ...item,
        submit_status: adapterSubmitStatus(submitLog),
        request_payload: submitLog.request_payload,
        response_payload: submitLog.response_payload,
        response_status: submitLog.response_status,
        endpoint: submitLog.endpoint,
        submit_type: submitLog.submit_type,
        action: submitLog.action,
        error: submitLog.error,
      }
    })
    .sort((a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime())
  if (!searchLog.value) return base
  const keyword = searchLog.value.trim()
  return base.filter((item) =>
    [
      item.recognition_result,
      item.recognition_text,
      item.submit_status,
      item.source_code,
      item.goods_id,
      item.sku_id,
      item.device_id,
      item.task_id,
      item.upstream_task_ref,
      item.message,
      item.endpoint,
      item.submit_type,
      String(item.response_status ?? ''),
      item.error,
      item.template_label,
      item.template_id,
      item.recognition_engine,
      item.adb_command,
    ].some((value) => value?.includes(keyword)),
  )
})

const nowTs = ref(Date.now())
let timer: number | null = null

function formatStage(stage?: string) {
  const mapping: Record<string, string> = {
    bootstrap: '启动中',
    waiting: '等待中',
    fetching: '领取任务',
    queue_wait: '等待候选区',
    queue_assigned: '任务已派发',
    open_url: '跳转链接',
    init: '任务初始化',
    capture: '截图识别',
    account_risk: '账号风控',
    account_risk_stop: '风控停机',
    fail_release: '失败释放',
    click_image: '点击图',
    click_action: '执行点击',
    success_image: '成功图',
    loop_wait: '继续下一轮',
    success: '已成功',
    failure: '已失败',
    submit_limit_stop: '提交限额停机',
  }
  return mapping[stage || ''] || stage || '-'
}

function formatTemplateType(type?: string | null) {
  const mapping: Record<string, string> = {
    account_risk: '账号风控',
    fail_release: '失败释放',
    click_image: '点击图',
    success_image: '成功图',
  }
  if (!type) return '-'
  return mapping[type] || type
}

function currentTaskMatchedTemplateText(row: DeviceInfo) {
  const task = row.current_task
  if (!task?.last_matched_template) return ''
  const parts = [task.last_matched_template]
  if (task.last_matched_template_type) {
    parts.push(formatTemplateType(task.last_matched_template_type))
  }
  if (task.last_matched_recognition_engine) {
    parts.push(recognitionEngineLabel(task.last_matched_recognition_engine))
  }
  return parts.join(' / ')
}

function formatDuration(startedAt?: string) {
  const parsed = parseApiDate(startedAt)
  if (!parsed) return '-'
  const diff = Math.max(0, Math.floor((nowTs.value - parsed.getTime()) / 1000))
  const minutes = Math.floor(diff / 60)
  const seconds = diff % 60
  return `${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`
}

function queueStatusLabel(status?: string | null) {
  if (status === 'active') return '处理中'
  if (status === 'completed') return '已完成'
  if (status === 'released') return '已释放'
  return '候选中'
}

function pendingTaskItemKey(taskId: string, index: number, goodsId?: string | null, skuId?: string | null) {
  return `${taskId}-${index}-${goodsId || ''}-${skuId || ''}`
}

function formatLogPayload(payload?: Record<string, unknown> | null) {
  if (!payload || Object.keys(payload).length === 0) return ''
  return JSON.stringify(payload, null, 2)
}

function recognitionEngineLabel(engine?: 'opencv' | 'ocr' | null) {
  if (engine === 'ocr') return 'OCR'
  if (engine === 'opencv') return '找图'
  return '-'
}

function toggleLogExpand(logId: string) {
  expandedLogId.value = expandedLogId.value === logId ? '' : logId
}

function recognitionTagClass(resultType: string) {
  if (resultType === '成功') return 'tag-success'
  if (resultType === '失败释放' || resultType === '异常停止') return 'tag-danger'
  if (resultType === '账号风控' || resultType === '账号风控停机' || resultType === '服务中断') return 'tag-warning'
  return 'tag-info'
}

function submitStatusTagClass(status: string) {
  if (status === '提交成功') return 'tag-success'
  if (status === '提交失败') return 'tag-danger'
  if (status === '提交中') return 'tag-warning'
  return 'tag-info'
}

function hasActiveTask(row: DeviceInfo) {
  return Boolean(row.current_task && row.current_task.task_id !== 'waiting')
}

function taskTimerType(row: DeviceInfo) {
  if (!hasActiveTask(row) || !row.current_task?.started_at) return 'info'
  const diff = taskElapsedSeconds(row)
  if (diff >= TASK_DANGER_SECONDS) return 'danger'
  if (diff >= TASK_WARNING_SECONDS) return 'warning'
  return 'success'
}

function formatLogTime(value?: string | null) {
  if (!value) return '-'
  return new Date(value).toLocaleTimeString('zh-CN', { hour12: false })
}

function rowClassName({ row }: { row: DeviceInfo }) {
  const timerType = taskTimerType(row)
  if (timerType === 'danger') return 'task-row-danger'
  if (timerType === 'warning') return 'task-row-warning'
  return ''
}

const handleSelectionChange = (selection: DeviceInfo[]) => {
  // We need to map Element Plus selection to our custom toggle logic
  // To keep it simple, we just emit clear and then toggle the selected ones
  emit('clear-selection')
  selection.forEach(device => emit('toggle-device', device.serial))
}

onMounted(() => {
  timer = window.setInterval(() => {
    nowTs.value = Date.now()
  }, 1000)
})

onBeforeUnmount(() => {
  if (timer !== null) window.clearInterval(timer)
})
</script>

<template>
  <div class="task-dashboard">
    <el-row :gutter="24">
      <!-- Left Column: Devices -->
      <el-col :span="16">
        <el-card shadow="never" class="modern-card device-panel">
          <template #header>
            <div class="flex-between card-header-custom">
              <div class="header-title">
                <span class="title-text">设备集群与任务调度</span>
                <el-tag type="info" effect="light" round class="ml-3">在线 {{ filteredDevices.length }} 台</el-tag>
              </div>
              <div class="header-actions">
                <el-button type="primary" plain @click="emit('refresh')">刷新状态</el-button>
              </div>
            </div>
          </template>

          <div class="action-bar mb-5">
            <div class="action-group">
              <el-button type="primary" @click="emit('start')" :disabled="selectedDevices.length === 0 || selectedAllRunning" class="control-btn">
                批量启动
              </el-button>
              <el-button type="danger" plain @click="emit('stop')" :disabled="selectedDevices.length === 0 || !selectedAllRunning" class="control-btn">
                批量停止
              </el-button>
            </div>
            
            <div class="filter-group">
              <el-input
                v-model="searchDevice"
                placeholder="搜索设备号"
                clearable
                class="search-input"
              />
            </div>
          </div>

          <div class="connect-bar mb-5">
            <el-input
              :model-value="connectEndpoint"
              placeholder="输入 IP:端口 连接新设备，例如 192.168.0.2:5555"
              @input="emit('update:connectEndpoint', $event)"
              @keydown.enter="emit('connect-device')"
              class="connect-input"
            >
              <template #prefix>
                <span class="prefix-text">TCP/IP</span>
              </template>
            </el-input>
            <el-button type="success" :loading="connecting" @click="emit('connect-device')" class="connect-btn">
              连接设备
            </el-button>
          </div>

          <el-table
            :data="filteredDevices"
            class="modern-table"
            @selection-change="handleSelectionChange"
            :row-class-name="rowClassName"
            row-key="serial"
          >
            <el-table-column type="selection" width="45" align="center" />
            
            <el-table-column label="设备终端" min-width="160" show-overflow-tooltip>
              <template #default="{ row }">
                <div class="device-identity">
                  <span class="serial mono-text">{{ row.serial }}</span>
                  <div class="status-badges">
                    <span class="badge" :class="row.status === 'device' ? 'bg-green' : 'bg-gray'">
                      {{ row.status === 'device' ? '在线' : row.status }}
                    </span>
                    <span class="badge" :class="row.running ? 'bg-blue' : 'bg-gray'">
                      {{ row.running ? '运行中' : '空闲' }}
                    </span>
                  </div>
                </div>
              </template>
            </el-table-column>

            <el-table-column label="当前任务执行进度" min-width="260">
              <template #default="{ row }">
                <div v-if="row.current_task" class="task-progress-cell">
                  <div class="task-id-row">
                    <span class="label">任务</span>
                    <span class="value mono-text truncate" :title="row.current_task.task_id">{{ row.current_task.task_id }}</span>
                  </div>
                  <div class="stage-row">
                    <span class="stage-tag">{{ formatStage(row.current_task.current_stage) }}</span>
                    <span class="loop-tag">第 {{ row.current_task.loop_count }} 轮</span>
                    <span v-if="formatURLTemplateProgress(row)" class="loop-tag">{{ formatURLTemplateProgress(row) }}</span>
                  </div>
                  <div class="msg-row truncate" :title="row.current_task.current_message">
                    {{ row.current_task.current_message || '-' }}
                  </div>
                  <div
                    v-if="currentTaskMatchedTemplateText(row)"
                    class="match-row truncate"
                    :title="currentTaskMatchedTemplateText(row)"
                  >
                    最近命中：{{ currentTaskMatchedTemplateText(row) }}
                  </div>
                </div>
                <div v-else class="empty-task-cell">当前无派发任务</div>
              </template>
            </el-table-column>

            <el-table-column label="运行耗时" width="100" align="center">
              <template #default="{ row }">
                <div v-if="hasActiveTask(row)" class="duration-badge" :class="taskTimerType(row)">
                  {{ formatDuration(row.current_task.started_at) }}
                </div>
                <span v-else class="text-gray text-xs">--:--</span>
              </template>
            </el-table-column>

            <el-table-column label="产出(成/败)" width="120" align="center">
              <template #default="{ row }">
                <div class="stats-compact">
                  <span class="text-green font-medium">{{ row.stats.success }}</span>
                  <span class="text-gray mx-1">/</span>
                  <span class="text-red font-medium">{{ row.stats.failure }}</span>
                </div>
              </template>
            </el-table-column>

            <el-table-column label="操作" width="180" align="center" fixed="right">
              <template #default="{ row }">
                <div class="table-actions">
                  <el-button link type="info" @click="openDeviceTemplateDialog(row)">
                    模板{{ selectedURLTemplateCount(row) > 0 ? `(${selectedURLTemplateCount(row)})` : '' }}
                  </el-button>
                  <el-button link type="primary" @click="emit('inspect-device', row.serial)">查看</el-button>
                  <el-button
                    v-if="!row.running"
                    link
                    type="primary"
                    @click="emit('start-device', row.serial)"
                  >启动</el-button>
                  <el-button
                    v-else
                    link
                    type="danger"
                    @click="emit('stop-device', row.serial)"
                  >停止</el-button>
                </div>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-col>

      <!-- Right Column: Queue & Logs -->
      <el-col :span="8">
        <div class="right-panels-stack">
          <!-- Pending Queue -->
          <el-card shadow="never" class="modern-card side-panel">
            <template #header>
              <div class="flex-between">
                <span class="card-title">调度候选区</span>
                <span class="count-badge">{{ props.pendingTasks.length }}</span>
              </div>
            </template>

            <div v-if="props.pendingTasks.length === 0" class="empty-panel">
              <div class="empty-text">候选区暂无等待任务</div>
            </div>
            
            <div v-else class="queue-list">
              <div v-for="task in props.pendingTasks" :key="task.task_id" class="queue-card">
                <div class="qc-header">
                  <span class="qc-id mono-text truncate" :title="task.task_id">{{ task.task_id }}</span>
                  <span class="qc-status" :class="task.status">{{ queueStatusLabel(task.status) }}</span>
                </div>
                
                <div class="qc-body">
                  <div class="qc-row">
                    <span class="qc-label">上游</span>
                    <span class="qc-value truncate" :title="task.source_name || task.source_code || '-'">{{ task.source_name || task.source_code || '-' }}</span>
                  </div>
                  <div class="qc-row">
                    <span class="qc-label">账号</span>
                    <span class="qc-value truncate" :title="task.account_name || '-'">{{ task.account_name || '默认账号' }}</span>
                  </div>
                  <div class="qc-row">
                    <span class="qc-label">进度</span>
                    <span class="qc-value">待 {{ task.pending_count ?? task.item_count }} · 中 {{ task.active_count ?? 0 }} · 成 {{ task.completed_count ?? 0 }}</span>
                  </div>
                  <div v-if="task.task_items?.length" class="qc-items">
                    <div class="qc-items-title">任务项</div>
                    <div
                      v-for="(item, index) in task.task_items"
                      :key="pendingTaskItemKey(task.task_id, index, item.goods_id, item.sku_id)"
                      class="qc-item-row"
                    >
                      <span class="qc-item-index">#{{ (item.step_index ?? index) + 1 }}</span>
                      <span class="qc-item-code">
                        <span class="qc-item-label">goods_id</span>
                        <span class="mono-text">{{ item.goods_id || '-' }}</span>
                      </span>
                      <span class="qc-item-code">
                        <span class="qc-item-label">sku_id</span>
                        <span class="mono-text">{{ item.sku_id || '-' }}</span>
                      </span>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </el-card>

          <!-- Task Logs -->
          <el-card shadow="never" class="modern-card side-panel logs-panel">
            <template #header>
              <div class="flex-between">
                <span class="card-title">实时任务日志</span>
                <el-input
                  v-model="searchLog"
                  placeholder="检索日志..."
                  class="log-search"
                  clearable
                />
              </div>
            </template>
            
            <div class="log-container">
              <div v-if="filteredLogs.length === 0" class="empty-panel">
                <div class="empty-text">暂无执行日志</div>
              </div>
              
              <div v-else class="modern-log-list">
                <div
                  v-for="event in filteredLogs"
                  :key="event.id"
                  class="log-card"
                  :class="[`level-${event.level}`, { 'is-expanded': expandedLogId === event.id }]"
                  @click="toggleLogExpand(event.id)"
                >
                  <div class="lc-summary">
                    <div class="lc-time">{{ formatLogTime(event.timestamp) }}</div>
                    <div class="lc-main">
                      <div class="lc-tags">
                        <span class="lc-tag" :class="recognitionTagClass(event.recognition_result)">{{ event.recognition_result }}</span>
                        <span class="lc-tag" :class="submitStatusTagClass(event.submit_status)">{{ event.submit_status }}</span>
                      </div>
                      <div class="lc-text truncate" :title="event.recognition_text">{{ event.recognition_text }}</div>
                    </div>
                  </div>
                  
                  <div v-if="expandedLogId === event.id" class="lc-detail">
                    <div class="lc-detail-grid">
                      <div class="lc-detail-item">
                        <span class="label">时间</span>
                        <span class="value">{{ new Date(event.timestamp).toLocaleString('zh-CN', { hour12: false }) }}</span>
                      </div>
                      <div class="lc-detail-item">
                        <span class="label">设备</span>
                        <span class="value text-blue">{{ event.device_id || '-' }}</span>
                      </div>
                      <div class="lc-detail-item">
                        <span class="label">识别结果</span>
                        <span class="value">{{ event.recognition_result || '-' }}</span>
                      </div>
                      <div class="lc-detail-item">
                        <span class="label">识别文本</span>
                        <span class="value">{{ event.recognition_text || '-' }}</span>
                      </div>
                      <div class="lc-detail-item">
                        <span class="label">模板引擎</span>
                        <span class="value">{{ recognitionEngineLabel(event.recognition_engine) }}</span>
                      </div>
                      <div class="lc-detail-item">
                        <span class="label">模板名称</span>
                        <span class="value">{{ event.template_label || '-' }}</span>
                      </div>
                      <div class="lc-detail-item">
                        <span class="label">模板ID</span>
                        <span class="value mono-text">{{ event.template_id || '-' }}</span>
                      </div>
                      <div class="lc-detail-item">
                        <span class="label">提交状态</span>
                        <span class="value">{{ event.submit_status || '-' }}</span>
                      </div>
                      <div class="lc-detail-item">
                        <span class="label">goods_id</span>
                        <span class="value mono-text">{{ event.goods_id || '-' }}</span>
                      </div>
                      <div class="lc-detail-item">
                        <span class="label">sku_id</span>
                        <span class="value mono-text">{{ event.sku_id || '-' }}</span>
                      </div>
                      <div class="lc-detail-item">
                        <span class="label">任务ID</span>
                        <span class="value mono-text">{{ event.task_id || '-' }}</span>
                      </div>
                      <div class="lc-detail-item">
                        <span class="label">说明</span>
                        <span class="value">{{ event.message || '-' }}</span>
                      </div>
                    </div>
                    
                    <div v-if="formatLogPayload(event.report_payload)" class="lc-code-block">
                      <div class="code-title">上报参数</div>
                      <pre>{{ formatLogPayload(event.report_payload) }}</pre>
                    </div>

                    <div v-if="event.adb_command" class="lc-code-block">
                      <div class="code-title">ADB命令</div>
                      <pre>{{ event.adb_command }}</pre>
                    </div>
                    
                    <div v-if="event.error" class="lc-error-block">
                      {{ event.error }}
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </el-card>
        </div>
      </el-col>
    </el-row>

    <el-dialog
      v-model="deviceTemplateDialogVisible"
      title="选择设备轮换 URL 模板"
      width="700px"
      destroy-on-close
    >
      <div class="device-template-dialog">
        <div class="mb-4 text-sm text-gray">
          设备：<span class="mono-text">{{ editingDeviceId || '-' }}</span>
        </div>
        <el-alert
          title="只会在勾选的 URL 模板里循环；如果一个都不勾选，则默认使用全部 URL 模板。保存后该设备会从新的第 1 个模板重新开始轮换。"
          type="info"
          :closable="false"
          class="mb-4"
        />
        <el-empty v-if="props.urlTemplates.length === 0" description="当前没有可选的 URL 模板" :image-size="72" />
        <el-checkbox-group v-else v-model="editingDeviceTemplateIDs" class="device-template-group">
          <el-checkbox
            v-for="(item, index) in props.urlTemplates"
            :key="item.id"
            :label="item.id"
            class="device-template-option"
          >
            <div class="device-template-option-text">
              <div class="device-template-option-title">{{ item.name?.trim() || `模板 ${index + 1}` }}</div>
              <div class="device-template-option-preview mono-text" :title="item.template">{{ templateDisplayText(item, index) }}</div>
            </div>
          </el-checkbox>
        </el-checkbox-group>
      </div>
      <template #footer>
        <el-button @click="deviceTemplateDialogVisible = false">取消</el-button>
        <el-button type="primary" @click="saveDeviceTemplateDialog">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
/* Base Layout */
.task-dashboard {
  display: flex;
  flex-direction: column;
}

.right-panels-stack {
  display: flex;
  flex-direction: column;
  gap: 24px;
  height: calc(100vh - 120px);
}

.mb-5 { margin-bottom: 20px; }
.ml-3 { margin-left: 12px; }
.mx-1 { margin-left: 4px; margin-right: 4px; }
.truncate { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }

/* Modern Card Design */
.modern-card {
  border-radius: 12px;
  border: 1px solid #f1f5f9;
  box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.05), 0 2px 4px -1px rgba(0, 0, 0, 0.03);
  background: #ffffff;
}

.modern-card :deep(.el-card__header) {
  padding: 16px 20px;
  border-bottom: 1px solid #f1f5f9;
}

.modern-card :deep(.el-card__body) {
  padding: 20px;
}

.card-header-custom {
  display: flex;
  align-items: center;
}

.title-text, .card-title {
  font-size: 16px;
  font-weight: 600;
  color: #0f172a;
}

.flex-between {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

/* Actions & Filters */
.action-bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: 16px;
}

.action-group, .filter-group {
  display: flex;
  gap: 12px;
}

.control-btn {
  padding-left: 20px;
  padding-right: 20px;
  font-weight: 500;
}

.sort-select { width: 140px; }
.search-input { width: 200px; }

.connect-bar {
  display: flex;
  gap: 12px;
  background: #f8fafc;
  padding: 12px;
  border-radius: 8px;
  border: 1px solid #e2e8f0;
}

.connect-input { flex: 1; }
.prefix-text {
  font-size: 12px;
  font-weight: 600;
  color: #64748b;
  display: flex;
  align-items: center;
  height: 100%;
}
.connect-btn { font-weight: 500; }

/* Table Typography & Badges */
.modern-table {
  border-radius: 8px;
  border: 1px solid #e2e8f0;
}

.modern-table :deep(th.el-table__cell) {
  background-color: #f8fafc;
  color: #475569;
  font-weight: 600;
  height: 44px;
}

.mono-text {
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, Courier, monospace;
}

.device-identity {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.device-identity .serial {
  font-size: 13px;
  font-weight: 500;
  color: #1e293b;
}

.status-badges {
  display: flex;
  gap: 6px;
}

.badge {
  font-size: 11px;
  font-weight: 600;
  padding: 2px 6px;
  border-radius: 4px;
  line-height: 1;
}
.bg-green { background: #dcfce7; color: #166534; }
.bg-blue { background: #e0f2fe; color: #075985; }
.bg-gray { background: #f1f5f9; color: #475569; }

/* Task Progress Cell */
.task-progress-cell {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.task-id-row {
  display: flex;
  align-items: baseline;
  gap: 6px;
}
.task-id-row .label { font-size: 11px; color: #94a3b8; }
.task-id-row .value { font-size: 12px; color: #334155; font-weight: 500; }

.stage-row {
  display: flex;
  gap: 6px;
}
.stage-tag, .loop-tag {
  font-size: 11px;
  background: #f1f5f9;
  color: #475569;
  padding: 2px 6px;
  border-radius: 4px;
}
.stage-tag { background: #e0e7ff; color: #1e40af; font-weight: 500; }

.msg-row {
  font-size: 12px;
  color: #64748b;
}

.match-row {
  font-size: 12px;
  color: #2563eb;
}

.empty-task-cell {
  font-size: 12px;
  color: #94a3b8;
  font-style: italic;
}

/* Duration Badges */
.duration-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 4px 8px;
  border-radius: 6px;
  font-size: 12px;
  font-weight: 600;
  font-family: monospace;
}
.duration-badge.success { background: #f8fafc; color: #475569; }
.duration-badge.warning { background: #fef3c7; color: #b45309; }
.duration-badge.danger { background: #fee2e2; color: #b91c1c; }

/* Row Highlight Classes */
:deep(.el-table .task-row-warning td) { background-color: #fffbeb !important; }
:deep(.el-table .task-row-danger td) { background-color: #fef2f2 !important; }

.stats-compact {
  font-size: 13px;
  font-family: monospace;
}
.text-green { color: #10b981; }
.text-red { color: #ef4444; }
.text-blue { color: #0ea5e9; }
.text-gray { color: #94a3b8; }
.font-medium { font-weight: 600; }

.table-actions {
  display: flex;
  gap: 8px;
  justify-content: center;
}

.device-template-dialog {
  display: flex;
  flex-direction: column;
}

.device-template-group {
  display: flex;
  flex-direction: column;
  gap: 10px;
  max-height: 420px;
  overflow-y: auto;
}

.device-template-option {
  margin-right: 0;
  padding: 10px 12px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
}

.device-template-option-text {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.device-template-option-title {
  font-size: 13px;
  font-weight: 600;
  color: #0f172a;
}

.device-template-option-preview {
  font-size: 12px;
  color: #475569;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* Queue Panel */
.side-panel {
  display: flex;
  flex-direction: column;
}
.side-panel :deep(.el-card__body) {
  padding: 0;
  flex: 1;
  overflow: hidden;
}

.count-badge {
  background: #f1f5f9;
  color: #475569;
  padding: 2px 8px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 600;
}

.empty-panel {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 120px;
}
.empty-text {
  font-size: 13px;
  color: #94a3b8;
}

.queue-list {
  max-height: 280px;
  overflow-y: auto;
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 12px;
  background: #f8fafc;
}

.queue-card {
  background: #ffffff;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  padding: 12px;
}

.qc-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
  padding-bottom: 8px;
  border-bottom: 1px dashed #f1f5f9;
}
.qc-id { font-size: 13px; font-weight: 600; color: #1e293b; max-width: 140px; }
.qc-status { font-size: 11px; padding: 2px 6px; border-radius: 4px; font-weight: 500; }
.qc-status.active { background: #fef3c7; color: #b45309; }
.qc-status.completed { background: #dcfce7; color: #166534; }
.qc-status.released { background: #f1f5f9; color: #475569; }

.qc-body {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.qc-row {
  display: flex;
  font-size: 12px;
}
.qc-label { color: #94a3b8; width: 36px; flex-shrink: 0; }
.qc-value { color: #475569; }

.qc-items {
  margin-top: 4px;
  padding-top: 8px;
  border-top: 1px dashed #e2e8f0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.qc-items-title {
  font-size: 11px;
  font-weight: 600;
  color: #64748b;
}

.qc-item-row {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 8px;
  border-radius: 6px;
  background: #f8fafc;
  border: 1px solid #e2e8f0;
}

.qc-item-index {
  font-size: 11px;
  color: #64748b;
  font-weight: 600;
}

.qc-item-code {
  display: flex;
  gap: 6px;
  align-items: baseline;
  font-size: 12px;
  color: #334155;
  word-break: break-all;
}

.qc-item-label {
  color: #94a3b8;
  flex-shrink: 0;
}

/* Logs Panel */
.logs-panel { flex: 1; }
.log-search { width: 140px; }

.log-container {
  height: 100%;
  overflow-y: auto;
  background: #f8fafc;
  padding: 12px;
}

.modern-log-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.log-card {
  background: #ffffff;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  padding: 12px;
  cursor: pointer;
  transition: all 0.2s ease;
}
.log-card:hover { border-color: #cbd5e1; }
.log-card.is-expanded { border-color: #bae6fd; background: #f0f9ff; }

.lc-summary {
  display: flex;
  gap: 12px;
  align-items: flex-start;
}
.lc-time {
  font-size: 12px;
  color: #64748b;
  font-family: monospace;
  padding-top: 2px;
  flex-shrink: 0;
}
.lc-main {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
  flex: 1;
}
.lc-tags {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}
.lc-tag {
  font-size: 11px;
  padding: 2px 6px;
  border-radius: 4px;
  font-weight: 500;
}
/* Tag classes mapped in setup */
.tag-success { background: #dcfce7; color: #166534; }
.tag-danger { background: #fee2e2; color: #b91c1c; }
.tag-warning { background: #fef3c7; color: #b45309; }
.tag-info { background: #f1f5f9; color: #475569; }

.lc-text {
  font-size: 12px;
  color: #334155;
}

.lc-detail {
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px dashed #cbd5e1;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.lc-detail-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
}
.lc-detail-item {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.lc-detail-item .label { font-size: 11px; color: #94a3b8; }
.lc-detail-item .value { font-size: 12px; color: #334155; word-break: break-all; }

.lc-code-block {
  background: #1e293b;
  border-radius: 6px;
  overflow: hidden;
}
.code-title {
  background: #334155;
  color: #cbd5e1;
  font-size: 11px;
  padding: 4px 8px;
}
.lc-code-block pre {
  margin: 0;
  padding: 8px;
  font-size: 11px;
  color: #e2e8f0;
  font-family: 'SFMono-Regular', Consolas, monospace;
  white-space: pre-wrap;
  word-break: break-all;
}

.lc-error-block {
  background: #fef2f2;
  border: 1px solid #fecaca;
  color: #b91c1c;
  padding: 8px;
  border-radius: 6px;
  font-size: 12px;
  white-space: pre-wrap;
}
</style>
