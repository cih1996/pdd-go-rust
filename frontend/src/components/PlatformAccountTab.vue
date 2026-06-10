<script setup lang="ts">
import { computed, reactive, watch, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { PlatformAccountRecord, UpstreamOption } from '../types'

const props = defineProps<{
  accounts: PlatformAccountRecord[]
  upstreamOptions: UpstreamOption[]
  importing: boolean
  savingAccountId: string
  batchProcessing?: boolean
}>()

const emit = defineEmits<{
  (event: 'import', payload: { upstream_code: string; lines: string }): void
  (event: 'toggle', payload: { accountId: string; enabled: boolean }): void
  (event: 'delete', accountId: string): void
  (event: 'batch-toggle', payload: { accountIds: string[]; enabled: boolean }): void
  (event: 'batch-delete', accountIds: string[]): void
}>()

const form = reactive({
  upstream_type: '',
  upstream_code: '',
  lines: '',
})

const selectedAccounts = ref<PlatformAccountRecord[]>([])

function handleSelectionChange(selection: PlatformAccountRecord[]) {
  selectedAccounts.value = selection
}

async function handleBatchEnable() {
  if (selectedAccounts.value.length === 0) return
  emit('batch-toggle', {
    accountIds: selectedAccounts.value.map(a => a.id),
    enabled: true
  })
}

async function handleBatchDisable() {
  if (selectedAccounts.value.length === 0) return
  emit('batch-toggle', {
    accountIds: selectedAccounts.value.map(a => a.id),
    enabled: false
  })
}

async function handleBatchDelete() {
  if (selectedAccounts.value.length === 0) return
  await ElMessageBox.confirm(`确认批量删除选中的 ${selectedAccounts.value.length} 个账号吗？`, '批量删除', {
    type: 'warning',
  })
  emit('batch-delete', selectedAccounts.value.map(a => a.id))
}

const enabledUpstreams = computed(() => props.upstreamOptions.filter((item) => item.enabled))
const upstreamTypeOptions = computed(() => {
  const seen = new Set<string>()
  return enabledUpstreams.value.filter((item) => {
    if (seen.has(item.upstream_type)) return false
    seen.add(item.upstream_type)
    return true
  })
})
const filteredUpstreams = computed(() => {
  if (!form.upstream_type) return enabledUpstreams.value
  return enabledUpstreams.value.filter((item) => item.upstream_type === form.upstream_type)
})
const selectedUpstream = computed(() => enabledUpstreams.value.find((item) => item.code === form.upstream_code) ?? null)
const isMockUpstreamSelected = computed(() => selectedUpstream.value?.upstream_type === 'mock_upstream')

watch(
  upstreamTypeOptions,
  (value) => {
    const first = value[0]
    if (!form.upstream_type && first) {
      form.upstream_type = first.upstream_type
    }
  },
  { immediate: true },
)

watch(
  enabledUpstreams,
  (value) => {
    const first = value.find((item) => item.upstream_type === form.upstream_type) ?? value[0]
    if (!form.upstream_code && first) {
      form.upstream_code = first.code
    }
  },
  { immediate: true },
)

watch(
  () => form.upstream_type,
  (value) => {
    const current = enabledUpstreams.value.find((item) => item.code === form.upstream_code)
    if (current?.upstream_type === value) {
      return
    }
    const first = enabledUpstreams.value.find((item) => item.upstream_type === value)
    form.upstream_code = first?.code ?? ''
  },
)

function submitImport() {
  if (!form.upstream_code) {
    ElMessage.warning('请先选择上游类型')
    return
  }
  if (!form.lines.trim()) {
    ElMessage.warning('请先输入平台账号')
    return
  }
  emit('import', {
    upstream_code: form.upstream_code,
    lines: form.lines.trim(),
  })
}

async function confirmDelete(accountId: string) {
  await ElMessageBox.confirm('删除后该账号将不再参与账号池抢单，是否继续？', '删除平台账号', {
    type: 'warning',
  })
  emit('delete', accountId)
}
</script>

<template>
  <div class="platform-account-container">
    <el-row :gutter="24">
      <el-col :span="8">
        <el-card shadow="never" class="modern-card mb-4">
          <template #header>
            <div class="card-title">平台账号导入</div>
          </template>

          <el-alert
            title="这里管理的是账号池。启用状态的账号会由后端自动轮询抢单，再把任务派发给空闲设备执行。"
            type="info"
            :closable="false"
            class="mb-3"
          />
          <el-alert
            title="如果选择的是本地模拟上游，账号内容只用于按正式流程传递，不做账号真实性校验。"
            type="warning"
            :closable="false"
            class="mb-4"
          />

          <el-form label-position="top" class="account-form">
            <el-form-item label="上游类型">
              <el-select v-model="form.upstream_type" placeholder="请选择上游类型" size="large" class="w-full">
                <el-option
                  v-for="item in upstreamTypeOptions"
                  :key="item.upstream_type"
                  :label="item.upstream_type"
                  :value="item.upstream_type"
                />
              </el-select>
            </el-form-item>
            <el-form-item label="上游实例">
              <el-select v-model="form.upstream_code" placeholder="请选择上游类型" size="large" class="w-full">
                <el-option
                  v-for="item in filteredUpstreams"
                  :key="item.code"
                  :label="`${item.name}（${item.upstream_type}）`"
                  :value="item.code"
                />
              </el-select>
            </el-form-item>
            <el-alert
              v-if="isMockUpstreamSelected"
              title="当前选中的是 mock_upstream：账号会像正式上游一样参与抢单与任务派发，只是不做真实性校验。"
              type="success"
              :closable="false"
              class="mb-4"
            />
            <el-form-item label="批量添加账号">
              <el-input
                v-model="form.lines"
                type="textarea"
                :rows="8"
                placeholder="每行一个账号，格式：账号备注,account|secret_key&#10;如果不写备注，也可以直接写：account|secret_key&#10;mock_upstream 下也沿用同样格式，但不会校验账号是否真实"
              />
            </el-form-item>
            <el-button type="primary" :loading="importing" @click="submitImport" class="w-full run-btn" size="large">
              批量导入
            </el-button>
          </el-form>
        </el-card>
      </el-col>

      <el-col :span="16">
        <el-card shadow="never" class="modern-card h-full">
          <template #header>
            <div class="flex-between">
              <div class="card-title">账号列表</div>
              <div class="action-bar-right" style="display: flex; gap: 8px; align-items: center;">
                <el-button 
                  type="success" 
                  plain 
                  size="small" 
                  :disabled="selectedAccounts.length === 0" 
                  :loading="batchProcessing"
                  @click="handleBatchEnable"
                >批量启用</el-button>
                <el-button 
                  type="warning" 
                  plain 
                  size="small" 
                  :disabled="selectedAccounts.length === 0" 
                  :loading="batchProcessing"
                  @click="handleBatchDisable"
                >批量停用</el-button>
                <el-button 
                  type="danger" 
                  plain 
                  size="small" 
                  :disabled="selectedAccounts.length === 0" 
                  :loading="batchProcessing"
                  @click="handleBatchDelete"
                >批量删除</el-button>
                <el-tag type="info" effect="light" round size="large" class="ml-2">共 {{ accounts.length }} 个账号</el-tag>
              </div>
            </div>
          </template>

          <el-table :data="accounts" border stripe class="modern-table" @selection-change="handleSelectionChange">
            <el-table-column type="selection" width="45" align="center" />
            <el-table-column prop="name" label="账号备注" min-width="160" show-overflow-tooltip>
              <template #default="{ row }">
                <span class="font-medium">{{ row.name || '-' }}</span>
              </template>
            </el-table-column>
            <el-table-column label="上游类型" min-width="180" show-overflow-tooltip>
              <template #default="{ row }">
                <el-tag size="small" type="info">{{ row.upstream_type }}</el-tag>
                <span class="ml-2 text-xs text-gray">{{ row.upstream_code }}</span>
              </template>
            </el-table-column>
            <el-table-column label="领取次数" width="100" align="center">
              <template #default="{ row }">
                <span class="highlight-text">{{ row.stats.fetched_count }}</span>
              </template>
            </el-table-column>
            <el-table-column label="成功提交" width="100" align="center">
              <template #default="{ row }">
                <span style="color: #10b981; font-weight: 500;">{{ row.stats.reported_success_count }}</span>
              </template>
            </el-table-column>
            <el-table-column label="失败提交" width="100" align="center">
              <template #default="{ row }">
                <span style="color: #ef4444; font-weight: 500;">{{ row.stats.reported_failure_count }}</span>
              </template>
            </el-table-column>
            <el-table-column label="状态" width="100" align="center">
              <template #default="{ row }">
                <el-tag :type="row.enabled ? 'success' : 'info'" effect="dark" size="small">
                  {{ row.enabled ? '自动抢单' : '已停用' }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column label="操作" width="140" fixed="right" align="center">
              <template #default="{ row }">
                <el-button
                  link
                  type="primary"
                  :loading="savingAccountId === row.id"
                  @click="emit('toggle', { accountId: row.id, enabled: !row.enabled })"
                >
                  {{ row.enabled ? '停用' : '启用' }}
                </el-button>
                <el-button link type="danger" :loading="savingAccountId === row.id" @click="confirmDelete(row.id)">
                  删除
                </el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<style scoped>
.platform-account-container {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.card-title {
  font-size: 16px;
  font-weight: 600;
  color: #1f2937;
}

.modern-card {
  border-radius: 12px;
  border: 1px solid #f3f4f6;
  box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.05), 0 2px 4px -1px rgba(0, 0, 0, 0.03);
  transition: box-shadow 0.3s ease;
}

.modern-card:hover {
  box-shadow: 0 10px 15px -3px rgba(0, 0, 0, 0.05), 0 4px 6px -2px rgba(0, 0, 0, 0.03);
}

.h-full {
  height: 100%;
}

.flex-between {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.w-full {
  width: 100%;
}

.ml-2 {
  margin-left: 8px;
}

.mb-3 {
  margin-bottom: 12px;
}

.mb-4 {
  margin-bottom: 16px;
}

.text-gray {
  color: #6b7280;
}

.text-xs {
  font-size: 12px;
}

.font-medium {
  font-weight: 500;
}

.highlight-text {
  color: #0ea5e9;
  font-weight: 500;
}

.run-btn {
  height: 44px;
  font-size: 16px;
  font-weight: 500;
  letter-spacing: 1px;
}

.modern-table {
  border-radius: 8px;
  overflow: hidden;
}

.modern-table :deep(th.el-table__cell) {
  background-color: #f8fafc;
  color: #475569;
  font-weight: 600;
}
</style>
