<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import type { DeviceInfo, PendingTaskRecord, TaskEvent } from '../types'
import { Monitor, Refresh, VideoPlay, VideoPause, Connection, Search, Clock } from '@element-plus/icons-vue'
import { formatApiDateTime, parseApiDate } from '../utils/datetime'

const props = defineProps<{
  devices: DeviceInfo[]
  eventLog: TaskEvent[]
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
  (event: 'refresh'): void
}>()

const searchDevice = ref('')
const searchLog = ref('')
const sortMode = ref<'default' | 'duration_desc'>('default')
const expandedLogId = ref('')
const TASK_WARNING_SECONDS = 6
const TASK_DANGER_SECONDS = 10

function taskElapsedSeconds(row: DeviceInfo) {
  if (!hasActiveTask(row) || !row.current_task?.started_at) return 0
  const startedAt = parseApiDate(row.current_task.started_at)
  if (!startedAt) return 0
  return Math.max(0, Math.floor((nowTs.value - startedAt.getTime()) / 1000))
}

function taskPriority(row: DeviceInfo) {
  const elapsed = taskElapsedSeconds(row)
  if (elapsed >= TASK_DANGER_SECONDS) return 3
  if (elapsed >= TASK_WARNING_SECONDS) return 2
  if (hasActiveTask(row)) return 1
  return 0
}

const filteredDevices = computed(() => {
  const keyword = searchDevice.value.trim()
  const base = keyword ? props.devices.filter((d) => d.serial.includes(keyword)) : [...props.devices]

  return [...base].sort((a, b) => {
    const priorityDiff = taskPriority(b) - taskPriority(a)
    if (priorityDiff !== 0) return priorityDiff

    if (sortMode.value === 'duration_desc') {
      const elapsedDiff = taskElapsedSeconds(b) - taskElapsedSeconds(a)
      if (elapsedDiff !== 0) return elapsedDiff
    }

    if (a.running !== b.running) return a.running ? -1 : 1

    const activeElapsedDiff = taskElapsedSeconds(b) - taskElapsedSeconds(a)
    if (activeElapsedDiff !== 0) return activeElapsedDiff

    return a.serial.localeCompare(b.serial)
  })
})

interface TerminalTaskLogItem {
  id: string
  timestamp: string
  device_id?: string | null
  level: TaskEvent['level']
  task_id: string
  upstream_task_ref: string
  report_result: string
  recognition_type: string
  recognition_content: string
  message: string
  source_code: string
  report_payload?: Record<string, unknown> | null
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
    report_result: String(payload.report_result ?? '-'),
    recognition_type: String(payload.recognition_type ?? '-'),
    recognition_content: String(payload.recognition_content ?? '-'),
    message: String(payload.message ?? ''),
    source_code: String(payload.source_code ?? ''),
    report_payload:
      payload.report_payload && typeof payload.report_payload === 'object'
        ? (payload.report_payload as Record<string, unknown>)
        : null,
  }
}

const filteredLogs = computed(() => {
  const base = props.eventLog.map(toTerminalTaskLog).filter((item): item is TerminalTaskLogItem => Boolean(item))
  if (!searchLog.value) return base
  const keyword = searchLog.value.trim()
  return base.filter((item) =>
    [
      item.report_result,
      item.recognition_type,
      item.recognition_content,
      item.device_id,
      item.task_id,
      item.upstream_task_ref,
      item.message,
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
  }
  return mapping[stage || ''] || stage || '-'
}

function formatDuration(startedAt?: string) {
  const parsed = parseApiDate(startedAt)
  if (!parsed) return '-'
  const diff = Math.max(0, Math.floor((nowTs.value - parsed.getTime()) / 1000))
  const minutes = Math.floor(diff / 60)
  const seconds = diff % 60
  return `${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`
}

function formatDateTime(value?: string | null) {
  return formatApiDateTime(value)
}

function formatLogPayload(payload?: Record<string, unknown> | null) {
  if (!payload || Object.keys(payload).length === 0) return ''
  return JSON.stringify(payload, null, 2)
}

function toggleLogExpand(logId: string) {
  expandedLogId.value = expandedLogId.value === logId ? '' : logId
}

function reportTagType(result: string) {
  return result === '上报成功' ? 'success' : result === '上报失败' ? 'danger' : 'info'
}

function recognitionTagType(resultType: string) {
  if (resultType === '成功') return 'success'
  if (resultType === '失败释放') return 'danger'
  if (resultType === '账号风控') return 'warning'
  if (resultType === '账号风控停机') return 'warning'
  if (resultType === '手动停止') return 'info'
  if (resultType === '服务中断') return 'warning'
  if (resultType === '异常停止') return 'danger'
  return 'info'
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
  <el-row :gutter="20">
    <el-col :span="17">
      <el-card shadow="never" class="mb-4">
        <template #header>
          <div class="flex-between">
            <span style="font-size: 16px; font-weight: 500;">设备管理</span>
            <div>
              <el-button type="primary" :icon="Refresh" plain @click="emit('refresh')">刷新状态</el-button>
            </div>
          </div>
        </template>

        <div class="flex-between mb-4">
          <div class="flex-row" style="gap: 12px;">
            <el-button type="primary" :icon="VideoPlay" @click="emit('start')" :disabled="selectedDevices.length === 0 || selectedAllRunning">
              批量启动
            </el-button>
            <el-button type="danger" :icon="VideoPause" @click="emit('stop')" :disabled="selectedDevices.length === 0 || !selectedAllRunning">
              批量停止
            </el-button>
          </div>
          <div class="flex-row" style="gap: 12px;">
            <el-select v-model="sortMode" style="width: 140px">
              <el-option label="智能置顶" value="default" />
              <el-option label="耗时最长" value="duration_desc" />
            </el-select>
            <el-input
              v-model="searchDevice"
              placeholder="搜索设备号"
              :prefix-icon="Search"
              style="width: 160px"
              clearable
            />
          </div>
        </div>

        <div class="flex-row mb-4" style="gap: 12px;">
          <el-input
            :model-value="connectEndpoint"
            placeholder="输入 IP:端口 连接新设备，例如 192.168.0.2:5555"
            @input="emit('update:connectEndpoint', $event)"
            @keydown.enter="emit('connect-device')"
            style="flex: 1;"
          >
            <template #prefix>
              <el-icon><Monitor /></el-icon>
            </template>
          </el-input>
          <el-button type="success" :icon="Connection" :loading="connecting" @click="emit('connect-device')">
            连接设备
          </el-button>
        </div>

        <el-alert
          title="任务由后端自动轮询账号池领取并派发给空闲设备，当前页面不再需要给设备单独绑定账号。"
          type="info"
          :closable="false"
          class="mb-4"
        />

        <el-table
          :data="filteredDevices"
          style="width: 100%"
          border
          size="small"
          @selection-change="handleSelectionChange"
          :row-class-name="rowClassName"
          row-key="serial"
        >
          <el-table-column type="selection" width="45" />
          <el-table-column prop="serial" label="设备序列号" min-width="130" show-overflow-tooltip />
          <el-table-column label="状态" width="70">
            <template #default="{ row }">
              <el-tag :type="row.status === 'device' ? 'success' : 'info'" size="small">
                {{ row.status === 'device' ? '在线' : row.status }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="运行情况" width="80">
            <template #default="{ row }">
              <el-tag :type="row.running ? 'primary' : 'info'" effect="dark" size="small">
                {{ row.running ? '任务中' : '空闲' }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="本次任务进度" min-width="220">
            <template #default="{ row }">
              <div v-if="row.current_task" style="display: flex; flex-direction: column; gap: 2px;">
                <div style="font-size: 12px; color: #303133;" class="truncate" :title="row.current_task.task_id">
                  <strong>{{ row.current_task.task_id }}</strong>
                </div>
                <div style="font-size: 11px; color: #606266;">
                  {{ formatStage(row.current_task.current_stage) }} / 第 {{ row.current_task.loop_count }} 轮
                </div>
                <div style="font-size: 11px; color: #909399;" class="truncate" :title="row.current_task.current_message">
                  {{ row.current_task.current_message || '-' }}
                </div>
              </div>
              <span v-else style="color: #c0c4cc; font-size: 12px;">暂无任务</span>
            </template>
          </el-table-column>
          <el-table-column label="耗时" width="90">
            <template #default="{ row }">
              <el-tag v-if="hasActiveTask(row)" :type="taskTimerType(row)" effect="dark" size="small">
                <el-icon style="vertical-align: middle; margin-right: 2px;"><Clock /></el-icon>
                <span style="vertical-align: middle;">{{ formatDuration(row.current_task.started_at) }}</span>
              </el-tag>
              <span v-else style="color: #c0c4cc; font-size: 12px;">--:--</span>
            </template>
          </el-table-column>
          <el-table-column label="统计(总/成/败)" width="110">
            <template #default="{ row }">
              <span style="font-size: 12px;">
                <span style="color: #909399">{{ row.stats.total }}</span> / 
                <span style="color: #67C23A">{{ row.stats.success }}</span> / 
                <span style="color: #F56C6C">{{ row.stats.failure }}</span>
              </span>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="130" fixed="right">
            <template #default="{ row }">
              <el-button type="primary" link size="small" @click="emit('inspect-device', row.serial)">
                查看
              </el-button>
              <el-button
                v-if="!row.running"
                type="primary"
                link
                size="small"
                @click="emit('start-device', row.serial)"
              >
                启动
              </el-button>
              <el-button
                v-else
                type="danger"
                link
                size="small"
                @click="emit('stop-device', row.serial)"
              >
                停止
              </el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-card>
    </el-col>

    <el-col :span="7">
      <el-card shadow="never" class="dashboard-card mb-4">
        <template #header>
          <div class="flex-between">
            <span style="font-size: 16px; font-weight: 500;">候选区列表</span>
            <el-tag size="small" type="info">{{ props.pendingTasks.length }} 个任务</el-tag>
          </div>
        </template>

        <div v-if="props.pendingTasks.length === 0">
          <el-empty description="候选区暂无等待任务" :image-size="52" />
        </div>
        <div v-else class="queue-list">
          <div v-for="task in props.pendingTasks" :key="task.task_id" class="queue-item">
            <div class="queue-item-head">
              <div class="truncate" :title="task.task_id">
                <strong>{{ task.task_id }}</strong>
              </div>
              <el-tag size="small" type="primary">{{ task.item_count }} 个 SKU</el-tag>
            </div>
            <div class="queue-item-line">
              <span>来源：</span>
              <span class="truncate" :title="task.source_name || task.source_code || '-'">
                {{ task.source_name || task.source_code || '-' }}
              </span>
            </div>
            <div class="queue-item-line">
              <span>账号：</span>
              <span class="truncate" :title="task.account_name || '-'">
                {{ task.account_name || '默认账号' }}
              </span>
            </div>
            <div class="queue-item-line">
              <span>上游任务号：</span>
              <span class="truncate" :title="task.upstream_task_ref || '-'">
                {{ task.upstream_task_ref || '-' }}
              </span>
            </div>
            <div class="queue-item-line">
              <span>进入候选区：</span>
              <span>{{ formatDateTime(task.prefetched_at) }}</span>
            </div>
          </div>
        </div>
      </el-card>

      <el-card shadow="never" class="dashboard-card">
        <template #header>
          <div class="flex-between">
            <span style="font-size: 16px; font-weight: 500;">实时任务日志</span>
            <el-input
              v-model="searchLog"
              placeholder="筛选结果..."
              :prefix-icon="Search"
              style="width: 120px"
              size="small"
              clearable
            />
          </div>
        </template>
        
        <div class="log-container">
          <el-empty v-if="filteredLogs.length === 0" description="暂无结果日志" :image-size="60" />
          <div v-else class="log-list">
            <div class="log-list-head">
              <span>上报结果</span>
              <span>识别结果类型</span>
              <span>识别结果内容</span>
            </div>
            <div
              v-for="event in filteredLogs"
              :key="event.id"
              class="log-item"
              :class="[`log-${event.level}`, { 'log-item-expanded': expandedLogId === event.id }]"
              @click="toggleLogExpand(event.id)"
            >
              <div class="log-main-row">
                <div class="log-col">
                  <el-tag :type="reportTagType(event.report_result)" effect="dark" size="small">
                    {{ event.report_result }}
                  </el-tag>
                </div>
                <div class="log-col">
                  <el-tag :type="recognitionTagType(event.recognition_type)" size="small">
                    {{ event.recognition_type }}
                  </el-tag>
                </div>
                <div class="log-col log-col-content" :title="event.recognition_content">
                  {{ event.recognition_content }}
                </div>
              </div>
              <div v-if="expandedLogId === event.id" class="log-detail">
                <div class="log-header">
                  <span class="log-time">{{ new Date(event.timestamp).toLocaleString('zh-CN', { hour12: false }) }}</span>
                  <span class="log-device" v-if="event.device_id" :title="event.device_id">{{ event.device_id }}</span>
                </div>
                <div class="log-detail-line"><strong>任务ID：</strong>{{ event.task_id || '-' }}</div>
                <div class="log-detail-line"><strong>上游任务号：</strong>{{ event.upstream_task_ref || '-' }}</div>
                <div class="log-detail-line"><strong>结果说明：</strong>{{ event.message || '-' }}</div>
                <pre v-if="formatLogPayload(event.report_payload)" class="log-payload">{{ formatLogPayload(event.report_payload) }}</pre>
              </div>
            </div>
          </div>
        </div>
      </el-card>
    </el-col>
  </el-row>
</template>

<style scoped>
.dashboard-card :deep(.el-card__body) {
  flex: 1;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  padding: 0;
}

.truncate {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.queue-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: 240px;
  overflow-y: auto;
  padding: 0 12px 12px;
}

.log-container {
  max-height: 320px;
  overflow-y: auto;
  padding: 0 12px 12px;
}

.queue-item {
  border: 1px solid #ebeef5;
  border-radius: 8px;
  background: #fff;
  padding: 10px 12px;
}

.queue-item-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 8px;
  font-size: 13px;
  color: #303133;
}

.queue-item-line {
  display: grid;
  grid-template-columns: 72px 1fr;
  gap: 6px;
  align-items: start;
  font-size: 12px;
  color: #606266;
  line-height: 1.6;
}

:deep(.el-table .task-row-warning td) {
  background: #fff7e6 !important;
}

:deep(.el-table .task-row-danger td) {
  background: #fff1f0 !important;
}

.log-container {
  flex: 1;
  overflow-y: auto;
  background: #f8f9fa;
}

.log-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 8px;
}

.log-list-head {
  display: grid;
  grid-template-columns: 88px 92px 1fr;
  gap: 8px;
  padding: 0 12px;
  font-size: 12px;
  color: #909399;
}

.log-item {
  padding: 10px 12px;
  border: 1px solid #ebeef5;
  border-radius: 8px;
  background: #fff;
  transition: background-color 0.2s, border-color 0.2s;
  cursor: pointer;
}

.log-item:hover {
  background: #f4f6f8;
  border-color: #dcdfe6;
}

.log-item-expanded {
  border-color: #c6e2ff;
  background: #f8fbff;
}

.log-main-row {
  display: grid;
  grid-template-columns: 88px 92px 1fr;
  gap: 8px;
  align-items: center;
}

.log-col {
  min-width: 0;
}

.log-col-content {
  font-size: 13px;
  color: #303133;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.log-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
}

.log-time {
  font-size: 12px;
  color: #909399;
  font-family: monospace;
}

.log-device {
  font-size: 11px;
  color: #409EFF;
  background: #ecf5ff;
  padding: 1px 6px;
  border-radius: 4px;
  max-width: 120px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.log-detail {
  margin-top: 10px;
  padding-top: 10px;
  border-top: 1px dashed #e4e7ed;
}

.log-detail-line {
  font-size: 12px;
  color: #606266;
  line-height: 1.7;
  word-break: break-all;
}

.log-payload {
  margin: 6px 0 0;
  padding: 8px 10px;
  font-size: 12px;
  line-height: 1.45;
  color: #606266;
  background: #f8fafc;
  border: 1px solid #ebeef5;
  border-radius: 6px;
  white-space: pre-wrap;
  word-break: break-all;
  font-family: Consolas, Monaco, monospace;
}

.log-warning .log-msg {
  color: #e6a23c;
}
.log-error .log-msg {
  color: #f56c6c;
}
</style>
