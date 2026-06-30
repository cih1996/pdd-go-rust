<script setup lang="ts">
import { computed, ref, watch } from 'vue'
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
const currentPage = ref(1)
const pageSize = ref(25)

watch(keyword, () => {
  currentPage.value = 1
})

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

const paginatedLogs = computed(() => {
  const start = (currentPage.value - 1) * pageSize.value
  return filteredLogs.value.slice(start, start + pageSize.value)
})
</script>

<template>
  <div class="submit-log-page">
    <div class="flex-between mb-4">
      <div class="submit-log-header">
        <span class="submit-log-title">适配器完整交互日志</span>
        <el-tag size="small" type="info" effect="light" round>{{ props.logs.length }} 条记录</el-tag>
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

    <div class="submit-log-wrapper">
      <el-empty v-if="filteredLogs.length === 0" description="暂无适配器交互日志" :image-size="80" />
      <div v-else class="submit-log-list">
        <div
          v-for="item in paginatedLogs"
          :key="item.id"
          class="submit-log-item"
          :class="{ 'submit-log-item-expanded': expandedLogId === item.id }"
          @click="toggleExpand(item.id)"
        >
          <div class="submit-log-summary">
            <div class="submit-log-summary-left">
              <el-tag :type="actionTagType(item.action)" size="small" effect="light">{{ item.action }}</el-tag>
              <el-tag v-if="item.submit_type" :type="submitTypeTagType(item.submit_type)" size="small" effect="plain">{{ item.submit_type }}</el-tag>
              <span class="truncate submit-log-task-id" :title="item.task_id || '-'">{{ item.task_id || '-' }}</span>
            </div>
            <el-tag :type="responseStatusTagType(item.response_status, item.error)" size="small" effect="dark">
              {{ item.response_status ?? (item.error ? 'ERROR' : 'PENDING') }}
            </el-tag>
          </div>

          <div class="submit-log-meta">
            <span><i class="meta-icon">🕒</i> {{ formatDateTime(item.timestamp) }}</span>
            <span><i class="meta-icon">⚡</i> {{ item.request_method }}</span>
            <span><i class="meta-icon">🌐</i> {{ item.source_code || '-' }}</span>
            <span><i class="meta-icon">📱</i> {{ item.device_id || '-' }}</span>
            <span><i class="meta-icon">🔖</i> {{ item.upstream_task_ref || '-' }}</span>
          </div>

          <div v-if="expandedLogId === item.id" class="submit-log-detail" @click.stop>
            <div class="detail-grid">
              <div class="detail-grid-item">
                <span class="detail-label">动作：</span>
                <span class="detail-value">{{ item.action }}</span>
              </div>
              <div class="detail-grid-item">
                <span class="detail-label">接口：</span>
                <span class="detail-value">{{ item.endpoint }}</span>
              </div>
              <div class="detail-grid-item" v-if="item.error">
                <span class="detail-label">错误：</span>
                <span class="detail-value text-red">{{ item.error }}</span>
              </div>
            </div>

            <el-row :gutter="16" class="mt-4">
              <el-col :span="12">
                <div class="submit-log-block">
                  <div class="submit-log-block-title">Request Payload</div>
                  <pre class="submit-log-payload">{{ formatAnyPayload(item.request_payload) }}</pre>
                </div>
              </el-col>
              <el-col :span="12">
                <div class="submit-log-block">
                  <div class="submit-log-block-title">Response Data</div>
                  <pre class="submit-log-payload">{{ formatAnyPayload(item.response_payload) || '-' }}</pre>
                </div>
              </el-col>
            </el-row>
          </div>
        </div>
      </div>
      <div class="submit-log-footer" v-if="filteredLogs.length > 0">
        <el-pagination
          v-model:current-page="currentPage"
          v-model:page-size="pageSize"
          :total="filteredLogs.length"
          :page-sizes="[15, 25, 50, 100]"
          layout="total, sizes, prev, pager, next, jumper"
          background
          small
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
.submit-log-page {
  display: flex;
  flex-direction: column;
}

.flex-between {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.mb-4 {
  margin-bottom: 16px;
}

.mt-4 {
  margin-top: 16px;
}

.submit-log-header {
  display: flex;
  align-items: center;
  gap: 12px;
}

.submit-log-title {
  font-size: 16px;
  font-weight: 600;
  color: #1f2937;
}

.submit-log-actions {
  display: flex;
  align-items: center;
  gap: 12px;
}

.submit-log-wrapper {
  max-height: 600px;
  overflow-y: auto;
  padding: 16px;
  background: #f8fafc;
  border-radius: 8px;
  border: 1px solid #e2e8f0;
}

.submit-log-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.submit-log-item {
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #ffffff;
  padding: 14px 16px;
  cursor: pointer;
  transition: all 0.2s ease;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.02);
}

.submit-log-item:hover {
  border-color: #cbd5e1;
  box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.05);
}

.submit-log-item-expanded {
  border-color: #93c5fd;
  box-shadow: 0 4px 12px -2px rgba(0, 0, 0, 0.08);
  cursor: default;
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
  color: #334155;
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, Courier, monospace;
}

.submit-log-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 12px 20px;
  margin-top: 10px;
  font-size: 12px;
  color: #64748b;
}

.meta-icon {
  font-style: normal;
  opacity: 0.7;
  margin-right: 2px;
}

.submit-log-detail {
  display: flex;
  flex-direction: column;
  margin-top: 16px;
  padding-top: 16px;
  border-top: 1px dashed #e2e8f0;
}

.detail-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 12px;
  background: #f8fafc;
  padding: 12px;
  border-radius: 6px;
}

.detail-grid-item {
  display: flex;
  align-items: flex-start;
  font-size: 13px;
}

.detail-label {
  color: #64748b;
  min-width: 48px;
}

.detail-value {
  color: #334155;
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, Courier, monospace;
  word-break: break-all;
}

.text-red {
  color: #ef4444;
}

.submit-log-block {
  display: flex;
  flex-direction: column;
  gap: 8px;
  height: 100%;
}

.submit-log-block-title {
  font-size: 13px;
  font-weight: 600;
  color: #475569;
}

.submit-log-payload {
  margin: 0;
  padding: 12px;
  border-radius: 6px;
  background: #1e293b;
  color: #f8fafc;
  font-size: 12px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 300px;
  overflow-y: auto;
  flex: 1;
}

/* Custom Scrollbar for payload */
.submit-log-payload::-webkit-scrollbar {
  width: 6px;
  height: 6px;
}
.submit-log-payload::-webkit-scrollbar-track {
  background: #0f172a;
  border-radius: 3px;
}
.submit-log-payload::-webkit-scrollbar-thumb {
  background: #475569;
  border-radius: 3px;
}
.submit-log-payload::-webkit-scrollbar-thumb:hover {
  background: #64748b;
}

.truncate {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.submit-log-footer {
  display: flex;
  justify-content: center;
  padding: 16px 0 0;
  border-top: 1px solid #e2e8f0;
  margin-top: 16px;
}
</style>
