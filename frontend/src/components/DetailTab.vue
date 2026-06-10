<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage } from 'element-plus'
import type { DashboardSummary, DetailRecord } from '../types'
import { Search } from '@element-plus/icons-vue'
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
      ? [d.upstream_task_ref, d.url, d.recognition, d.task_id].some((item) => item?.includes(keyword))
      : true
    return matchDevice && matchTask
  })
})
</script>

<template>
  <div class="stack">
    <el-row :gutter="24" class="mb-4">
      <el-col :span="6">
        <el-card shadow="hover">
          <div class="statistic-card">
            <el-statistic title="总任务数" :value="summary.total" />
          </div>
        </el-card>
      </el-col>
      <el-col :span="6">
        <el-card shadow="hover">
          <div class="statistic-card">
            <el-statistic title="成功任务" :value="summary.success" value-style="color: #67C23A" />
          </div>
        </el-card>
      </el-col>
      <el-col :span="6">
        <el-card shadow="hover">
          <div class="statistic-card">
            <el-statistic title="失败任务" :value="summary.failure" value-style="color: #F56C6C" />
          </div>
        </el-card>
      </el-col>
      <el-col :span="6">
        <el-card shadow="hover">
          <div style="font-size: 14px; color: #909399; margin-bottom: 8px;">时间范围</div>
          <el-radio-group :model-value="rangeKey" @update:model-value="emit('update:rangeKey', $event)">
            <el-radio-button value="today">今日</el-radio-button>
            <el-radio-button value="yesterday">昨日</el-radio-button>
            <el-radio-button value="7d">近7日</el-radio-button>
          </el-radio-group>
        </el-card>
      </el-col>
    </el-row>

    <el-card shadow="never">
      <template #header>
        <div class="flex-between detail-header">
          <div class="detail-title-group">
            <span style="font-size: 16px; font-weight: 500;">执行明细</span>
            <el-button type="danger" :loading="clearing" @click="emit('clear-details')">清空全部</el-button>
          </div>
          <div class="flex-row" style="gap: 12px;">
            <el-input
              v-model="searchKeyword"
              placeholder="搜索上游编号/链接"
              :prefix-icon="Search"
              clearable
              style="width: 220px"
            />
            <el-input
              v-model="searchDevice"
              placeholder="搜索设备号"
              :prefix-icon="Search"
              clearable
              style="width: 200px"
            />
          </div>
        </div>
      </template>

      <el-table :data="filteredDetails" style="width: 100%" border stripe>
        <el-table-column label="执行时间" width="180">
          <template #default="{ row }">
            {{ formatApiDateTime(row.timestamp) }}
          </template>
        </el-table-column>
        <el-table-column label="上游任务号" width="180" show-overflow-tooltip>
          <template #default="{ row }">
            {{ row.upstream_task_ref || '待上游返回' }}
          </template>
        </el-table-column>
        <el-table-column prop="device_id" label="设备号" width="180" show-overflow-tooltip />
        <el-table-column label="执行结果" width="120">
          <template #default="{ row }">
            <el-tag :type="row.status === 'success' ? 'success' : row.status === 'failure' ? 'danger' : 'info'">
              {{ row.status === 'success' ? '成功' : row.status === 'failure' ? '失败' : row.status }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="任务URL" min-width="380">
          <template #default="{ row }">
            <div v-if="toTaskH5Url(row.url)" class="detail-url-cell">
              <el-link :href="toTaskH5Url(row.url)" target="_blank" type="primary" class="detail-url-text">
                {{ toTaskH5Url(row.url) }}
              </el-link>
              <div class="detail-url-actions">
                <el-button text size="small" @click="copyTaskUrl(row.url)">复制</el-button>
                <el-button text size="small" @click="openTaskUrl(row.url)">打开</el-button>
              </div>
            </div>
            <span v-else style="color: #909399;">暂无链接</span>
          </template>
        </el-table-column>
        <el-table-column prop="recognition" label="识别内容" min-width="150" show-overflow-tooltip />
        <el-table-column prop="message" label="结果说明" min-width="180" show-overflow-tooltip />
        <el-table-column label="任务截图" min-width="300">
          <template #default="{ row }">
            <div v-if="row.capture_urls?.length" style="display: flex; gap: 8px; flex-wrap: wrap;">
              <el-image
                v-for="(url, index) in row.capture_urls"
                :key="index"
                style="width: 60px; height: 80px; border-radius: 4px;"
                :src="url"
                :preview-src-list="row.capture_urls"
                :initial-index="index"
                fit="cover"
                preview-teleported
              />
            </div>
            <span v-else style="color: #909399;">暂无截图</span>
          </template>
        </el-table-column>
      </el-table>
    </el-card>
  </div>
</template>

<style scoped>
.statistic-card {
  padding: 8px 0;
}

.detail-url-cell {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.detail-url-text {
  display: inline-block;
  max-width: 100%;
  word-break: break-all;
  line-height: 1.5;
}

.detail-url-actions {
  display: flex;
  gap: 4px;
}

.detail-header {
  gap: 12px;
  flex-wrap: wrap;
}

.detail-title-group {
  display: flex;
  align-items: center;
  gap: 12px;
}
</style>
