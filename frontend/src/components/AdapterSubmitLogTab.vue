<script setup lang="ts">
import { computed, ref } from 'vue'
import { Search, Refresh } from '@element-plus/icons-vue'
import type { AdapterSubmitLogRecord } from '../types'
import { formatApiDateTime } from '../utils/datetime'

const props = defineProps<{
  logs: AdapterSubmitLogRecord[]
}>()

const emit = defineEmits<{
  (event: 'refresh'): void
}>()

const keyword = ref('')
const expandedLogId = ref('')

function formatDateTime(value?: string | null) {
  return formatApiDateTime(value)
}

function formatAnyPayload(payload?: unknown) {
  if (payload === null || payload === undefined || payload === '') return ''
  return typeof payload === 'string' ? payload : JSON.stringify(payload, null, 2)
}

function toggleExpand(logId: string) {
  expandedLogId.value = expandedLogId.value === logId ? '' : logId
}

function submitTypeTagType(submitType: string) {
  if (submitType === 'success') return 'success'
  if (submitType === 'failure') return 'danger'
  if (submitType === 'cancelled') return 'warning'
  return 'info'
}

function actionTagType(action: string) {
  if (action === 'fetch-task') return 'primary'
  if (action === 'upload-capture') return 'warning'
  if (action === 'submit-task') return 'success'
  return 'info'
}

function responseStatusTagType(status?: number | null, error?: string | null) {
  if (error) return 'danger'
  if (!status) return 'info'
  if (status === 204) return 'warning'
  if (status >= 400) return 'danger'
  if (status >= 200) return 'success'
  return 'info'
}

const filteredLogs = computed(() => {
  const search = keyword.value.trim()
  if (!search) return props.logs
  return props.logs.filter((item) =>
    [
      item.task_id,
      item.upstream_task_ref,
      item.source_code,
      item.device_id,
      item.action,
      item.submit_type,
      String(item.response_status ?? ''),
      formatAnyPayload(item.request_payload),
      formatAnyPayload(item.response_payload),
      item.error,
      item.endpoint,
    ].some((value) => String(value ?? '').includes(search)),
  )
})
</script>

<template>
  <el-card shadow="never" class="submit-log-page">
    <template #header>
      <div class="flex-between">
        <div class="submit-log-header">
          <span class="submit-log-title">适配器完整交互日志</span>
          <el-tag size="small" type="info">{{ props.logs.length }} 条</el-tag>
        </div>
        <div class="submit-log-actions">
          <el-input
            v-model="keyword"
            placeholder="筛选动作 / task_id / body / 响应"
            :prefix-icon="Search"
            style="width: 260px"
            clearable
          />
          <el-button type="primary" plain :icon="Refresh" @click="emit('refresh')">刷新</el-button>
        </div>
      </div>
    </template>

    <div class="submit-log-wrapper">
      <el-empty v-if="filteredLogs.length === 0" description="暂无适配器交互日志" :image-size="72" />
      <div v-else class="submit-log-list">
        <div
          v-for="item in filteredLogs"
          :key="item.id"
          class="submit-log-item"
          :class="{ 'submit-log-item-expanded': expandedLogId === item.id }"
          @click="toggleExpand(item.id)"
        >
          <div class="submit-log-summary">
            <div class="submit-log-summary-left">
              <el-tag :type="actionTagType(item.action)" size="small">{{ item.action }}</el-tag>
              <el-tag v-if="item.submit_type" :type="submitTypeTagType(item.submit_type)" size="small">{{ item.submit_type }}</el-tag>
              <span class="truncate submit-log-task-id" :title="item.task_id || '-'">{{ item.task_id || '-' }}</span>
            </div>
            <el-tag :type="responseStatusTagType(item.response_status, item.error)" size="small">
              {{ item.response_status ?? (item.error ? 'ERROR' : 'PENDING') }}
            </el-tag>
          </div>

          <div class="submit-log-meta">
            <span>时间：{{ formatDateTime(item.timestamp) }}</span>
            <span>方法：{{ item.request_method }}</span>
            <span>来源：{{ item.source_code || '-' }}</span>
            <span>设备：{{ item.device_id || '-' }}</span>
            <span>上游任务号：{{ item.upstream_task_ref || '-' }}</span>
          </div>

          <div v-if="expandedLogId === item.id" class="submit-log-detail">
            <div class="submit-log-detail-line"><strong>动作：</strong>{{ item.action }}</div>
            <div class="submit-log-detail-line"><strong>接口：</strong>{{ item.endpoint }}</div>
            <div class="submit-log-detail-line"><strong>错误：</strong>{{ item.error || '-' }}</div>
            <div class="submit-log-block">
              <div class="submit-log-block-title">POST Body</div>
              <pre class="submit-log-payload">{{ formatAnyPayload(item.request_payload) }}</pre>
            </div>
            <div class="submit-log-block">
              <div class="submit-log-block-title">Response</div>
              <pre class="submit-log-payload">{{ formatAnyPayload(item.response_payload) || '-' }}</pre>
            </div>
          </div>
        </div>
      </div>
    </div>
  </el-card>
</template>

<style scoped>
.submit-log-page :deep(.el-card__body) {
  padding: 0;
}

.submit-log-header {
  display: flex;
  align-items: center;
  gap: 10px;
}

.submit-log-title {
  font-size: 16px;
  font-weight: 500;
}

.submit-log-actions {
  display: flex;
  align-items: center;
  gap: 12px;
}

.submit-log-wrapper {
  min-height: calc(100vh - 240px);
  max-height: calc(100vh - 240px);
  overflow-y: auto;
  padding: 16px;
  background: #f8f9fa;
}

.submit-log-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.submit-log-item {
  border: 1px solid #ebeef5;
  border-radius: 10px;
  background: #fff;
  padding: 14px 16px;
  cursor: pointer;
  transition: background-color 0.2s, border-color 0.2s;
}

.submit-log-item:hover {
  background: #f4f6f8;
  border-color: #dcdfe6;
}

.submit-log-item-expanded {
  border-color: #c6e2ff;
  background: #f8fbff;
}

.submit-log-summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.submit-log-summary-left {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.submit-log-task-id {
  font-size: 13px;
  color: #303133;
}

.submit-log-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 12px 18px;
  margin-top: 8px;
  font-size: 12px;
  color: #909399;
}

.submit-log-detail {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px dashed #dcdfe6;
}

.submit-log-detail-line {
  font-size: 13px;
  color: #606266;
}

.submit-log-block {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.submit-log-block-title {
  font-size: 12px;
  font-weight: 600;
  color: #606266;
}

.submit-log-payload {
  margin: 0;
  padding: 12px;
  border-radius: 8px;
  background: #1f2937;
  color: #e5e7eb;
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
}

.truncate {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
