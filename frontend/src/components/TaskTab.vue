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
  (event: 'save-device-task-mode', value: { device_id: string; mode_ex: string }): void
  (event: 'refresh'): void
}>()

const searchDevice = ref('')
const deviceTemplateDialogVisible = ref(false)
const editingDeviceId = ref('')
const editingDeviceTemplateIDs = ref<string[]>([])
const deviceTaskModeDialogVisible = ref(false)
const editingDeviceTaskModeId = ref('')
const editingDeviceTaskModeEx = ref('stealth')
const TASK_WARNING_SECONDS = 6
const TASK_DANGER_SECONDS = 10

function openDeviceTaskModeDialog(row: DeviceInfo) {
  editingDeviceTaskModeId.value = row.serial
  editingDeviceTaskModeEx.value = row.selected_task_mode_ex || 'stealth'
  deviceTaskModeDialogVisible.value = true
}

function saveDeviceTaskModeDialog() {
  emit('save-device-task-mode', {
    device_id: editingDeviceTaskModeId.value,
    mode_ex: editingDeviceTaskModeEx.value,
  })
  deviceTaskModeDialogVisible.value = false
}

function modeDisplayLabel(modeEx?: string | null) {
  if (modeEx === 'detail') return '详情'
  return '无痕'
}

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

const queueStats = computed(() => {
  let totalTasks = props.pendingTasks.length
  let totalPending = 0
  let totalActive = 0
  let totalCompleted = 0
  for (const task of props.pendingTasks) {
    totalPending += task.pending_count ?? task.item_count ?? 0
    totalActive += task.active_count ?? 0
    totalCompleted += task.completed_count ?? 0
  }
  return { totalTasks, totalPending, totalActive, totalCompleted }
})

const logStats = computed(() => {
  let success = 0
  let failure = 0
  let risk = 0
  
  for (const detail of props.details) {
    if (detail.status === 'success' || detail.recognition === 'success_image') success++
    else if (detail.status === 'failure' || detail.recognition === 'fail_release' || detail.recognition === 'open_url_failed') failure++
    else if (detail.recognition === 'account_risk' || detail.recognition === 'account_risk_stop') risk++
  }
  
  return {
    totalEvents: props.eventLog.length,
    totalDetails: props.details.length,
    totalSubmits: props.adapterSubmitLogs.length,
    success,
    failure,
    risk
  }
})

const nowTs = ref(Date.now())
let timer: number | null = null

function formatDuration(startedAt?: string) {
  const parsed = parseApiDate(startedAt)
  if (!parsed) return '-'
  const diff = Math.max(0, Math.floor((nowTs.value - parsed.getTime()) / 1000))
  const minutes = Math.floor(diff / 60)
  const seconds = diff % 60
  return `${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`
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
t                  <el-button link type="info" @click="openDeviceTemplateDialog(row)">
t                    模板{{ selectedURLTemplateCount(row) > 0 ? `(${selectedURLTemplateCount(row)})` : "" }}
t                  </el-button>
t                  <el-button link type="info" @click="openDeviceTaskModeDialog(row)">
t                    模式({{ modeDisplayLabel(row.selected_task_mode_ex) }})
t                  </el-button>
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

      <!-- Right Column: Queue & Logs Stats -->
      <el-col :span="8">
        <div class="right-panels-stack">
          <!-- Pending Queue Stats -->
          <el-card shadow="never" class="modern-card side-panel">
            <template #header>
              <div class="flex-between">
                <span class="card-title">调度候选区统计</span>
              </div>
            </template>

            <div class="stats-panel">
              <div class="stats-grid">
                <div class="stats-item">
                  <span class="stats-label">待调度任务数</span>
                  <span class="stats-value text-blue">{{ queueStats.totalTasks }}</span>
                </div>
                <div class="stats-item">
                  <span class="stats-label">等待执行项</span>
                  <span class="stats-value text-gray">{{ queueStats.totalPending }}</span>
                </div>
                <div class="stats-item">
                  <span class="stats-label">执行中项</span>
                  <span class="stats-value text-warning">{{ queueStats.totalActive }}</span>
                </div>
                <div class="stats-item">
                  <span class="stats-label">已完成项</span>
                  <span class="stats-value text-green">{{ queueStats.totalCompleted }}</span>
                </div>
              </div>
            </div>
          </el-card>

          <!-- Task Logs Stats -->
          <el-card shadow="never" class="modern-card side-panel logs-panel">
            <template #header>
              <div class="flex-between">
                <span class="card-title">任务执行统计</span>
              </div>
            </template>
            
            <div class="stats-panel">
              <div class="stats-grid">
                <div class="stats-item">
                  <span class="stats-label">执行事件总数</span>
                  <span class="stats-value">{{ logStats.totalEvents }}</span>
                </div>
                <div class="stats-item">
                  <span class="stats-label">识别总数</span>
                  <span class="stats-value">{{ logStats.totalDetails }}</span>
                </div>
                <div class="stats-item">
                  <span class="stats-label">成功识别</span>
                  <span class="stats-value text-green">{{ logStats.success }}</span>
                </div>
                <div class="stats-item">
                  <span class="stats-label">失败/异常</span>
                  <span class="stats-value text-danger">{{ logStats.failure }}</span>
                </div>
                <div class="stats-item">
                  <span class="stats-label">触发风控</span>
                  <span class="stats-value text-warning">{{ logStats.risk }}</span>
                </div>
                <div class="stats-item">
                  <span class="stats-label">提交任务数</span>
                  <span class="stats-value">{{ logStats.totalSubmits }}</span>
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
    <el-dialog
      v-model="deviceTaskModeDialogVisible"
      title="选择任务模式"
      width="450px"
      destroy-on-close
    >
      <div class="mb-4 text-sm text-gray">
        设备：<span class="mono-text">{{ editingDeviceTaskModeId || "-" }}</span>
      </div>
      <el-radio-group v-model="editingDeviceTaskModeEx">
        <el-radio value="stealth" class="mode-option">
          <div>
            <div class="font-medium">无痕模式 (Stealth)</div>
            <div class="text-xs text-gray">自动识别商品确认页，处理优惠券后直接通过</div>
          </div>
        </el-radio>
        <el-radio value="detail" class="mode-option">
          <div>
            <div class="font-medium">详情模式 (Detail)</div>
            <div class="text-xs text-gray">在无痕模式的基础上，进入规格选择页 OCR 校验 SKU 名称完整性</div>
          </div>
        </el-radio>
      </el-radio-group>
      <template #footer>
        <el-button @click="deviceTaskModeDialogVisible = false">取消</el-button>
        <el-button type="primary" @click="saveDeviceTaskModeDialog">保存</el-button>
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

/* Stats Panel */
.stats-panel {
  padding: 20px;
  background: #f8fafc;
  height: 100%;
}

.stats-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 16px;
}

.stats-item {
  background: #ffffff;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  align-items: center;
  justify-content: center;
  box-shadow: 0 1px 2px 0 rgba(0, 0, 0, 0.05);
}

.stats-label {
  font-size: 13px;
  color: #64748b;
  font-weight: 500;
}

.stats-value {
  font-size: 24px;
  font-weight: 600;
  font-family: monospace;
  color: #1e293b;
}

.text-warning { color: #f59e0b; }
.text-danger { color: #ef4444; }

.side-panel {
  display: flex;
  flex-direction: column;
}
.side-panel :deep(.el-card__body) {
  padding: 0;
  flex: 1;
  overflow: hidden;
}
</style>
