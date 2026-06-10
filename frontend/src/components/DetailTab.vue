<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage } from 'element-plus'
import type { DashboardSummary, DetailRecord } from '../types'
import { Search, Delete, CopyDocument, TopRight, View } from '@element-plus/icons-vue'
import { formatApiDateTime } from '../utils/datetime'

const props = defineProps<{
  summary: DashboardSummary
  details: DetailRecord[]
  rangeKey: string
  clearing: boolean
}>()

const emit = defineEmits<{
  (event: 'update:rangeKey', value: string): void
  (event: 'clear-details'): void
}>()

const searchDevice = ref('')
const searchKeyword = ref('')

function toTaskH5Url(url?: string | null): string {
  const raw = url?.trim()
  if (!raw) return ''
  if (/^https?:\/\//i.test(raw)) return raw

  const normalized = raw.replace(/^pinduoduo:\/\/com\.xunmeng\.pinduoduo\/?/i, '').replace(/^\/+/, '')
  if (!/\.html(?:\?|$)/i.test(normalized)) return ''
  return `https://mobile.yangkeduo.com/${normalized}`
}

async function copyTaskUrl(url?: string | null) {
  const target = toTaskH5Url(url)
  if (!target) {
    ElMessage.warning('当前任务没有可复制的链接')
    return
  }
  try {
    await navigator.clipboard.writeText(target)
    ElMessage.success('任务链接已复制')
  } catch {
    ElMessage.error('复制失败，请手动复制')
  }
}

function openTaskUrl(url?: string | null) {
  const target = toTaskH5Url(url)
  if (!target) {
    ElMessage.warning('当前任务没有可打开的链接')
    return
  }
  window.open(target, '_blank', 'noopener')
}

const filteredDetails = computed(() => {
  const keyword = searchKeyword.value.trim()
  return props.details.filter((d) => {
    const matchDevice = searchDevice.value ? d.device_id.includes(searchDevice.value) : true
    const matchTask = keyword
      ? [d.upstream_task_ref, d.url, d.recognition, d.task_id, d.goods_id, d.sku_id].some((item) => item?.includes(keyword))
      : true
    return matchDevice && matchTask
  })
})
</script>

<template>
  <div class="detail-page">
    <!-- Top Statistics Dashboard -->
    <div class="dashboard-stats mb-6">
      <el-row :gutter="24">
        <el-col :span="6">
          <div class="stat-card modern-card primary">
            <div class="stat-icon">📊</div>
            <div class="stat-content">
              <div class="stat-label">总任务数</div>
              <div class="stat-value">{{ summary.total }}</div>
            </div>
          </div>
        </el-col>
        <el-col :span="6">
          <div class="stat-card modern-card success">
            <div class="stat-icon">✅</div>
            <div class="stat-content">
              <div class="stat-label">成功任务</div>
              <div class="stat-value text-green">{{ summary.success }}</div>
            </div>
          </div>
        </el-col>
        <el-col :span="6">
          <div class="stat-card modern-card danger">
            <div class="stat-icon">❌</div>
            <div class="stat-content">
              <div class="stat-label">失败任务</div>
              <div class="stat-value text-red">{{ summary.failure }}</div>
            </div>
          </div>
        </el-col>
        <el-col :span="6">
          <div class="stat-card modern-card filter-card">
            <div class="stat-label mb-3">时间范围</div>
            <el-radio-group :model-value="rangeKey" @update:model-value="emit('update:rangeKey', $event)" size="large" class="w-full">
              <el-radio-button value="today" class="flex-1">今日</el-radio-button>
              <el-radio-button value="yesterday" class="flex-1">昨日</el-radio-button>
              <el-radio-button value="7d" class="flex-1">近7日</el-radio-button>
            </el-radio-group>
          </div>
        </el-col>
      </el-row>
    </div>

    <!-- Main Detail Table -->
    <el-card shadow="never" class="modern-card">
      <template #header>
        <div class="flex-between detail-header">
          <div class="header-left">
            <span class="card-title">执行明细与日志</span>
            <el-tag type="info" effect="light" round class="ml-3">当前显示 {{ filteredDetails.length }} 条</el-tag>
          </div>
          
          <div class="header-right">
            <el-input
              v-model="searchKeyword"
              placeholder="搜索上游编号 / 链接 / goods_id"
              :prefix-icon="Search"
              clearable
              class="search-input"
            />
            <el-input
              v-model="searchDevice"
              placeholder="筛选设备号"
              :prefix-icon="Search"
              clearable
              class="device-input"
            />
            <el-button type="danger" plain :icon="Delete" :loading="clearing" @click="emit('clear-details')">
              清空记录
            </el-button>
          </div>
        </div>
      </template>

      <el-table :data="filteredDetails" style="width: 100%" class="modern-table">
        <el-table-column label="执行时间" width="180">
          <template #default="{ row }">
            <div class="time-cell">
              <span class="date">{{ formatApiDateTime(row.timestamp).split(' ')[0] }}</span>
              <span class="time">{{ formatApiDateTime(row.timestamp).split(' ')[1] }}</span>
            </div>
          </template>
        </el-table-column>
        
        <el-table-column label="任务标识" width="220" show-overflow-tooltip>
          <template #default="{ row }">
            <div class="task-ids">
              <div class="id-row">
                <span class="id-label">上游:</span>
                <span class="id-value">{{ row.upstream_task_ref || '待上游返回' }}</span>
              </div>
              <div class="id-row">
                <span class="id-label">设备:</span>
                <span class="id-value device">{{ row.device_id }}</span>
              </div>
            </div>
          </template>
        </el-table-column>
        
        <el-table-column label="商品信息" width="180" show-overflow-tooltip>
          <template #default="{ row }">
            <div class="task-ids">
              <div class="id-row">
                <span class="id-label">GID:</span>
                <span class="id-value mono-text">{{ row.goods_id || '-' }}</span>
              </div>
              <div class="id-row">
                <span class="id-label">SKU:</span>
                <span class="id-value mono-text">{{ row.sku_id || '-' }}</span>
              </div>
            </div>
          </template>
        </el-table-column>
        
        <el-table-column label="执行结果" width="120" align="center">
          <template #default="{ row }">
            <el-tag :type="row.status === 'success' ? 'success' : row.status === 'failure' ? 'danger' : 'info'" effect="dark">
              {{ row.status === 'success' ? '成功' : row.status === 'failure' ? '失败' : row.status }}
            </el-tag>
          </template>
        </el-table-column>
        
        <el-table-column label="任务URL" min-width="340">
          <template #default="{ row }">
            <div v-if="toTaskH5Url(row.url)" class="url-cell">
              <el-tooltip :content="toTaskH5Url(row.url)" placement="top" :show-after="500">
                <div class="url-text">{{ toTaskH5Url(row.url) }}</div>
              </el-tooltip>
              <div class="url-actions">
                <el-button link type="primary" :icon="CopyDocument" @click="copyTaskUrl(row.url)">复制</el-button>
                <el-button link type="primary" :icon="TopRight" @click="openTaskUrl(row.url)">打开</el-button>
              </div>
            </div>
            <span v-else class="text-gray text-xs">暂无链接</span>
          </template>
        </el-table-column>
        
        <el-table-column label="详情信息" min-width="240">
          <template #default="{ row }">
            <div class="detail-info-cell">
              <div v-if="row.recognition" class="recognition-text" title="识别内容">
                <el-icon><View /></el-icon> {{ row.recognition }}
              </div>
              <div class="message-text" :class="{'is-error': row.status === 'failure'}" :title="row.message">
                {{ row.message || '-' }}
              </div>
            </div>
          </template>
        </el-table-column>
        
        <el-table-column label="任务截图" width="220" align="center">
          <template #default="{ row }">
            <div v-if="row.capture_urls?.length" class="capture-gallery">
              <el-image
                v-for="(url, index) in row.capture_urls.slice(0, 3)"
                :key="index"
                class="gallery-img"
                :src="url"
                :preview-src-list="row.capture_urls"
                :initial-index="index"
                fit="cover"
                preview-teleported
              />
              <div v-if="row.capture_urls.length > 3" class="gallery-more">
                +{{ row.capture_urls.length - 3 }}
              </div>
            </div>
            <span v-else class="text-gray text-xs">暂无截图</span>
          </template>
        </el-table-column>
      </el-table>
    </el-card>
  </div>
</template>

<style scoped>
.detail-page {
  display: flex;
  flex-direction: column;
}

.mb-6 { margin-bottom: 24px; }
.mb-3 { margin-bottom: 12px; }
.ml-3 { margin-left: 12px; }
.w-full { width: 100%; }

.modern-card {
  border-radius: 12px;
  border: 1px solid #f3f4f6;
  box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.05), 0 2px 4px -1px rgba(0, 0, 0, 0.03);
  transition: transform 0.2s ease, box-shadow 0.2s ease;
}

/* Stats Cards */
.stat-card {
  display: flex;
  align-items: center;
  padding: 24px;
  height: 110px;
  background: #ffffff;
}
.stat-card:hover {
  transform: translateY(-2px);
  box-shadow: 0 10px 15px -3px rgba(0, 0, 0, 0.05), 0 4px 6px -2px rgba(0, 0, 0, 0.03);
}

.stat-icon {
  font-size: 42px;
  margin-right: 20px;
  opacity: 0.9;
}

.stat-content {
  display: flex;
  flex-direction: column;
}

.stat-label {
  font-size: 14px;
  color: #64748b;
  font-weight: 500;
  margin-bottom: 8px;
}

.stat-value {
  font-size: 32px;
  font-weight: 700;
  line-height: 1;
  color: #1e293b;
}

.filter-card {
  flex-direction: column;
  align-items: flex-start;
  justify-content: center;
}
.filter-card :deep(.el-radio-group) {
  display: flex;
}
.filter-card :deep(.el-radio-button__inner) {
  width: 100%;
}

.text-green { color: #10b981; }
.text-red { color: #ef4444; }

/* Table Header */
.detail-header {
  flex-wrap: wrap;
  gap: 16px;
}

.flex-between {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.header-left {
  display: flex;
  align-items: center;
}

.header-right {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.card-title {
  font-size: 18px;
  font-weight: 600;
  color: #1f2937;
}

.search-input { width: 280px; }
.device-input { width: 180px; }

/* Table Styles */
.modern-table {
  border-radius: 8px;
  overflow: hidden;
}
.modern-table :deep(th.el-table__cell) {
  background-color: #f8fafc;
  color: #475569;
  font-weight: 600;
  height: 48px;
}

.time-cell {
  display: flex;
  flex-direction: column;
}
.time-cell .date {
  font-size: 13px;
  color: #64748b;
}
.time-cell .time {
  font-size: 14px;
  font-weight: 500;
  color: #334155;
}

.task-ids {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.id-row {
  display: flex;
  align-items: baseline;
  font-size: 13px;
}
.id-label {
  color: #94a3b8;
  width: 40px;
  flex-shrink: 0;
}
.id-value {
  color: #334155;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.id-value.device {
  color: #0ea5e9;
  font-weight: 500;
}
.mono-text {
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, Courier, monospace;
}

.url-cell {
  display: flex;
  flex-direction: column;
  gap: 6px;
  background: #f8fafc;
  padding: 8px 12px;
  border-radius: 6px;
  border: 1px solid #e2e8f0;
}
.url-text {
  font-size: 12px;
  color: #475569;
  font-family: 'SFMono-Regular', Consolas, monospace;
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  word-break: break-all;
}
.url-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

.detail-info-cell {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.recognition-text {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 13px;
  font-weight: 500;
  color: #0369a1;
  background: #e0f2fe;
  padding: 2px 8px;
  border-radius: 4px;
  width: fit-content;
}
.message-text {
  font-size: 13px;
  color: #64748b;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.message-text.is-error {
  color: #ef4444;
}

.capture-gallery {
  display: flex;
  justify-content: center;
  align-items: center;
  gap: 6px;
}
.gallery-img {
  width: 48px;
  height: 64px;
  border-radius: 6px;
  border: 1px solid #e2e8f0;
  cursor: pointer;
  transition: transform 0.2s;
}
.gallery-img:hover {
  transform: scale(1.05);
  border-color: #94a3b8;
}
.gallery-more {
  width: 48px;
  height: 64px;
  border-radius: 6px;
  background: #f1f5f9;
  color: #64748b;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 13px;
  font-weight: 600;
  border: 1px dashed #cbd5e1;
}

.text-gray { color: #94a3b8; }
.text-xs { font-size: 12px; }
</style>
