<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, DataLine, DocumentCopy } from '@element-plus/icons-vue'
import AdapterSubmitLogTab from './AdapterSubmitLogTab.vue'
import type { AdapterStatePayload, AdapterSubmitLogRecord, UpstreamConfigRecord } from '../types'

type UpstreamType = 'mock_upstream' | 'laoqian_worker' | 'custom_http'

const props = defineProps<{
  upstreams: UpstreamConfigRecord[]
  adapterState?: AdapterStatePayload | null
  adapterSubmitLogs?: AdapterSubmitLogRecord[]
  savingUpstreamId: string
  mockImporting?: boolean
}>()

const emit = defineEmits<{
  (event: 'create', payload: {
    name?: string | null
    upstream_type: UpstreamType
    enabled?: boolean
    priority?: number
    base_url: string
    proxy_url?: string | null
    notes?: string | null
  }): void
  (event: 'update', payload: {
    upstreamId: string
    data: {
      name?: string | null
      upstream_type: UpstreamType
      enabled?: boolean
      priority?: number
      base_url: string
      proxy_url?: string | null
      notes?: string | null
    }
  }): void
  (event: 'import-mock-data', payload: { lines: string; replace_existing: boolean }): void
  (event: 'toggle', payload: { upstreamId: string; enabled: boolean }): void
  (event: 'delete', upstreamId: string): void
  (event: 'refresh-state'): void
}>()

const typeOptions: Array<{ value: UpstreamType; label: string; defaultBaseUrl: string; description: string }> = [
  {
    value: 'laoqian_worker',
    label: '老钱真实上游',
    defaultBaseUrl: 'https://frontend.yqlaoqian111.com',
    description: '用于老钱真实上游任务流转',
  },
  {
    value: 'mock_upstream',
    label: '本地模拟上游',
    defaultBaseUrl: 'http://127.0.0.1:8102',
    description: '用于本地 pdd-data-server 调试',
  },
  {
    value: 'custom_http',
    label: '自定义 HTTP',
    defaultBaseUrl: '',
    description: '保留给自定义 HTTP 上游',
  },
]

const upstreamDrawerVisible = ref(false)
const mockDialogVisible = ref(false)

const editingId = ref('')
const form = reactive({
  name: '',
  upstream_type: 'laoqian_worker' as UpstreamType,
  enabled: true,
  priority: 100,
  base_url: 'https://frontend.yqlaoqian111.com',
  proxy_url: '',
  notes: '',
})

const mockImportForm = reactive({
  content: '',
  replace_existing: true,
})

const currentTypeOption = computed(() => {
  return typeOptions.find((item) => item.value === form.upstream_type) ?? typeOptions[0]!
})
const mockDataStatus = computed(() => props.adapterState?.mock_data_status ?? {
  imported_total: 0,
  remaining_total: 0,
  consumed_total: 0,
})
const adapterSnapshots = computed(() => props.adapterState?.recent_snapshots ?? [])

watch(
  () => form.upstream_type,
  (nextType, previousType) => {
    const nextDefault = typeOptions.find((item) => item.value === nextType)?.defaultBaseUrl ?? ''
    const previousDefault = typeOptions.find((item) => item.value === previousType)?.defaultBaseUrl ?? ''
    if (!form.base_url || form.base_url === previousDefault) {
      form.base_url = nextDefault
    }
  },
)

function resetForm() {
  editingId.value = ''
  form.name = ''
  form.upstream_type = 'laoqian_worker'
  form.enabled = true
  form.priority = 100
  form.base_url = 'https://frontend.yqlaoqian111.com'
  form.proxy_url = ''
  form.notes = ''
}

function handleAdd() {
  resetForm()
  upstreamDrawerVisible.value = true
}

function buildPayload() {
  return {
    name: form.name.trim() || null,
    upstream_type: form.upstream_type,
    enabled: form.enabled,
    priority: Number(form.priority) || 0,
    base_url: form.base_url.trim(),
    proxy_url: form.proxy_url.trim() || null,
    notes: form.notes.trim() || null,
  }
}

function submitForm() {
  if (!form.base_url.trim()) {
    ElMessage.warning('请先填写上游地址')
    return
  }
  const payload = buildPayload()
  if (editingId.value) {
    emit('update', { upstreamId: editingId.value, data: payload })
  } else {
    emit('create', payload)
  }
  upstreamDrawerVisible.value = false
}

function startEdit(row: UpstreamConfigRecord) {
  editingId.value = row.id
  form.name = row.name ?? ''
  form.upstream_type = row.upstream_type
  form.enabled = row.enabled
  form.priority = row.priority
  form.base_url = row.base_url
  form.proxy_url = row.proxy_url ?? ''
  form.notes = row.notes ?? ''
  upstreamDrawerVisible.value = true
}

async function confirmDelete(upstreamId: string) {
  await ElMessageBox.confirm('删除后该上游下的平台账号将无法继续取任务，是否继续？', '删除上游配置', {
    type: 'warning',
  })
  emit('delete', upstreamId)
}

async function importMockDataFromFile(file: File) {
  const text = await file.text()
  mockImportForm.content = text
}

async function handleMockFileChange(event: Event) {
  const file = (event.target as HTMLInputElement | null)?.files?.[0]
  if (!file) return
  await importMockDataFromFile(file)
}

function submitMockData() {
  const raw = mockImportForm.content.trim()
  if (!raw) {
    ElMessage.warning('请先粘贴或导入多行链接')
    return
  }
  emit('import-mock-data', {
    lines: raw,
    replace_existing: mockImportForm.replace_existing,
  })
  mockDialogVisible.value = false
  mockImportForm.content = ''
}
</script>

<template>
  <div class="upstream-config-page">
    <!-- Header Section -->
    <div class="page-header mb-6">
      <div class="header-info">
        <h2 class="page-title">上游服务与路由</h2>
        <p class="page-desc">集中管理任务分发的上游节点、本地模拟数据及监控完整调用链路</p>
      </div>
      <div class="header-actions">
        <el-badge :value="mockDataStatus.remaining_total" :max="999" class="badge-item" type="primary">
          <el-button size="large" plain :icon="DataLine" @click="mockDialogVisible = true" class="mock-btn">
            本地模拟数据
          </el-button>
        </el-badge>
        <el-button size="large" type="primary" :icon="Plus" @click="handleAdd" class="add-btn ml-4">
          添加上游配置
        </el-button>
      </div>
    </div>

    <!-- Main Content -->
    <div class="main-content">
      <el-card shadow="never" class="modern-card mb-6">
        <template #header>
          <div class="flex-between">
            <span class="card-title">上游节点列表</span>
            <el-tag type="info" effect="light" round>共 {{ props.upstreams.length }} 个上游</el-tag>
          </div>
        </template>

        <el-table :data="props.upstreams" border stripe class="modern-table">
          <el-table-column prop="name" label="名称" min-width="160" show-overflow-tooltip>
            <template #default="{ row }">
              <span class="font-medium">{{ row.name || '-' }}</span>
            </template>
          </el-table-column>
          <el-table-column prop="code" label="编码" min-width="170" show-overflow-tooltip>
            <template #default="{ row }">
              <span class="text-xs text-gray mono-text">{{ row.code }}</span>
            </template>
          </el-table-column>
          <el-table-column label="类型" min-width="130">
            <template #default="{ row }">
              <el-tag size="small" type="info">{{ row.upstream_type }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="base_url" label="基础地址" min-width="220" show-overflow-tooltip />
          <el-table-column label="代理" min-width="220" show-overflow-tooltip>
            <template #default="{ row }">
              <span class="text-xs text-gray mono-text">{{ row.proxy_url || '直连' }}</span>
            </template>
          </el-table-column>
          <el-table-column label="运行统计" min-width="170" align="center">
            <template #default="{ row }">
              <div class="stats-group">
                <span class="stat-item text-blue" title="取单">{{ row.stats.fetched_count }}</span>
                <span class="stat-divider">/</span>
                <span class="stat-item text-green" title="成功">{{ row.stats.reported_success_count }}</span>
                <span class="stat-divider">/</span>
                <span class="stat-item text-red" title="失败">{{ row.stats.reported_failure_count }}</span>
              </div>
            </template>
          </el-table-column>
          <el-table-column label="状态" width="100" align="center">
            <template #default="{ row }">
              <el-tag :type="row.enabled ? 'success' : 'info'" effect="dark" size="small">
                {{ row.enabled ? '启用中' : '已停用' }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="180" fixed="right" align="center">
            <template #default="{ row }">
              <el-button link type="primary" :loading="savingUpstreamId === row.id" @click="startEdit(row)">配置</el-button>
              <el-button
                link
                :type="row.enabled ? 'warning' : 'success'"
                :loading="savingUpstreamId === row.id"
                @click="emit('toggle', { upstreamId: row.id, enabled: !row.enabled })"
              >
                {{ row.enabled ? '停用' : '启用' }}
              </el-button>
              <el-button link type="danger" :loading="savingUpstreamId === row.id" @click="confirmDelete(row.id)">
                删除
              </el-button>
            </template>
          </el-table-column>
          <template #empty>
            <el-empty description="暂无上游配置，请点击右上角添加" :image-size="100">
              <el-button type="primary" :icon="Plus" @click="handleAdd">立即添加</el-button>
            </el-empty>
          </template>
        </el-table>
      </el-card>

      <el-card shadow="never" class="modern-card mb-6">
        <template #header>
          <div class="flex-between">
            <span class="card-title">适配器链路快照</span>
            <el-button link type="primary" :icon="DocumentCopy" @click="emit('refresh-state')">刷新状态</el-button>
          </div>
        </template>
        <el-empty v-if="!adapterSnapshots.length" description="适配器还没有链路快照" :image-size="80" />
        <el-table v-else :data="adapterSnapshots.slice(0, 20)" border stripe class="modern-table">
          <el-table-column prop="timestamp" label="时间" min-width="170" />
          <el-table-column prop="action" label="动作" width="130">
            <template #default="{ row }">
              <el-tag size="small" effect="plain">{{ row.action }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="status" label="结果" width="110">
            <template #default="{ row }">
              <el-tag size="small" :type="row.status === 'success' ? 'success' : row.status === 'error' ? 'danger' : 'info'">{{ row.status }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="source_code" label="上游" min-width="130" show-overflow-tooltip />
          <el-table-column prop="task_id" label="任务ID" min-width="180" show-overflow-tooltip>
            <template #default="{ row }">
              <span class="mono-text">{{ row.task_id || '-' }}</span>
            </template>
          </el-table-column>
          <el-table-column prop="upstream_task_ref" label="上游任务" min-width="150" show-overflow-tooltip />
          <el-table-column prop="message" label="说明" min-width="220" show-overflow-tooltip />
        </el-table>
      </el-card>

      <AdapterSubmitLogTab
        class="modern-card"
        :logs="props.adapterSubmitLogs ?? []"
        @refresh="emit('refresh-state')"
      />
    </div>

    <!-- Upstream Form Drawer -->
    <el-drawer
      v-model="upstreamDrawerVisible"
      :title="editingId ? '编辑上游配置' : '新增上游配置'"
      size="500px"
      destroy-on-close
      class="custom-drawer"
    >
      <el-form label-position="top" class="drawer-form" size="large">
        <el-form-item label="上游类型">
          <el-select v-model="form.upstream_type" class="w-full">
            <el-option
              v-for="item in typeOptions"
              :key="item.value"
              :label="item.label"
              :value="item.value"
            />
          </el-select>
          <div class="field-tip">{{ currentTypeOption.description }}</div>
        </el-form-item>
        
        <el-form-item label="上游名称">
          <el-input v-model="form.name" placeholder="可留空，系统按类型自动命名" />
        </el-form-item>
        
        <el-form-item label="优先级">
          <el-input-number v-model="form.priority" :min="0" :max="9999" class="w-full" controls-position="right" />
          <div class="field-tip">数字越小，优先级越高</div>
        </el-form-item>

        <el-form-item label="基础地址">
          <el-input v-model="form.base_url" placeholder="例如：http://127.0.0.1:8102" />
        </el-form-item>

        <el-form-item label="代理地址">
          <el-input
            v-model="form.proxy_url"
            placeholder="可选，例如 http://127.0.0.1:7890 或 socks5://127.0.0.1:1080"
          />
          <div class="field-tip">留空则直连上游；填写后适配器会按该代理访问当前上游</div>
        </el-form-item>

        <el-form-item label="备注">
          <el-input v-model="form.notes" type="textarea" :rows="3" placeholder="可选备注" />
        </el-form-item>

        <el-form-item label="状态">
          <el-switch v-model="form.enabled" inline-prompt active-text="启用" inactive-text="停用" />
        </el-form-item>

        <el-alert
          title="提示：上游配置不再保存默认密钥。调用适配器时只会使用平台账号中实时携带的凭证。"
          type="info"
          :closable="false"
          class="mt-4"
        />
      </el-form>
      <template #footer>
        <div class="drawer-footer">
          <el-button @click="upstreamDrawerVisible = false" size="large">取消</el-button>
          <el-button type="primary" :loading="savingUpstreamId === (editingId || '__creating__')" @click="submitForm" size="large" class="px-8">
            {{ editingId ? '保存修改' : '确认创建' }}
          </el-button>
        </div>
      </template>
    </el-drawer>

    <!-- Mock Data Import Dialog -->
    <el-dialog
      v-model="mockDialogVisible"
      title="本地模拟数据池"
      width="640px"
      destroy-on-close
      class="modern-dialog"
    >
      <el-alert
        title="导入的临时任务数据仅用于 mock_upstream，不替代正式配置。系统自动提取 goods_id 和 sku_id。"
        type="warning"
        :closable="false"
        class="mb-4"
      />
      
      <div class="mock-usage-grid mb-6">
        <div class="stat-card">
          <div class="usage-label">累计导入</div>
          <div class="usage-value text-blue">{{ mockDataStatus.imported_total }}</div>
        </div>
        <div class="stat-card highlight-card">
          <div class="usage-label">队列剩余</div>
          <div class="usage-value text-orange">{{ mockDataStatus.remaining_total }}</div>
        </div>
        <div class="stat-card">
          <div class="usage-label">已消费</div>
          <div class="usage-value text-green">{{ mockDataStatus.consumed_total }}</div>
        </div>
      </div>

      <el-form label-position="top">
        <el-form-item label="导入配置">
          <div class="mock-import-actions">
            <input
              type="file"
              accept=".txt,.json,text/plain,application/json"
              @change="handleMockFileChange"
              class="file-input"
            />
            <el-radio-group v-model="mockImportForm.replace_existing" size="small">
              <el-radio-button :value="false">追加</el-radio-button>
              <el-radio-button :value="true">覆盖</el-radio-button>
            </el-radio-group>
          </div>
        </el-form-item>
        <el-form-item label="粘贴模拟链接">
          <el-input
            v-model="mockImportForm.content"
            type="textarea"
            :rows="6"
            placeholder="每行一个链接，例如：https://...goods_id=...&sku_id=..."
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="mockDialogVisible = false" size="large">关闭</el-button>
        <el-button type="primary" :loading="props.mockImporting" @click="submitMockData" size="large" class="px-6">
          开始导入
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.upstream-config-page {
  display: flex;
  flex-direction: column;
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  background: #ffffff;
  padding: 24px;
  border-radius: 12px;
  box-shadow: 0 1px 3px 0 rgba(0, 0, 0, 0.1), 0 1px 2px 0 rgba(0, 0, 0, 0.06);
}

.page-title {
  margin: 0 0 8px 0;
  font-size: 20px;
  font-weight: 600;
  color: #1e293b;
}

.page-desc {
  margin: 0;
  font-size: 14px;
  color: #64748b;
}

.header-actions {
  display: flex;
  align-items: center;
}

.badge-item :deep(.el-badge__content) {
  transform: translateY(-50%) translateX(100%);
  z-index: 10;
}

.mock-btn {
  font-weight: 500;
}

.add-btn {
  font-weight: 500;
  letter-spacing: 0.5px;
}

.ml-4 {
  margin-left: 16px;
}

.mb-4 {
  margin-bottom: 16px;
}

.mb-6 {
  margin-bottom: 24px;
}

.mt-4 {
  margin-top: 16px;
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

.flex-between {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.w-full {
  width: 100%;
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

.font-medium {
  font-weight: 500;
}

.text-xs {
  font-size: 12px;
}

.text-gray {
  color: #6b7280;
}

.mono-text {
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, Courier, monospace;
}

.stats-group {
  display: inline-flex;
  align-items: center;
  background: #f8fafc;
  padding: 4px 12px;
  border-radius: 999px;
  border: 1px solid #e2e8f0;
}

.stat-item {
  font-weight: 600;
  font-size: 13px;
  min-width: 20px;
  text-align: center;
}

.stat-divider {
  margin: 0 6px;
  color: #cbd5e1;
  font-size: 12px;
}

.text-blue { color: #0ea5e9; }
.text-orange { color: #f59e0b; }
.text-green { color: #10b981; }
.text-red { color: #ef4444; }

/* Drawer Styles */
.custom-drawer :deep(.el-drawer__header) {
  margin-bottom: 0;
  padding: 20px 24px;
  border-bottom: 1px solid #f1f5f9;
  font-weight: 600;
  color: #0f172a;
}

.drawer-form {
  padding: 8px 4px;
}

.field-tip {
  margin-top: 6px;
  font-size: 12px;
  color: #64748b;
  line-height: 1.5;
}

.drawer-footer {
  display: flex;
  justify-content: flex-end;
  gap: 12px;
}

.px-8 {
  padding-left: 32px;
  padding-right: 32px;
}

.px-6 {
  padding-left: 24px;
  padding-right: 24px;
}

/* Dialog Styles */
.modern-dialog :deep(.el-dialog__header) {
  font-weight: 600;
  border-bottom: 1px solid #f1f5f9;
  margin-right: 0;
  padding-bottom: 16px;
}

.mock-usage-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 16px;
}

.stat-card {
  background: #f8fafc;
  border-radius: 12px;
  padding: 20px 16px;
  text-align: center;
  border: 1px solid #e2e8f0;
  transition: transform 0.2s ease;
}

.stat-card:hover {
  transform: translateY(-2px);
}

.highlight-card {
  background: #fffbeb;
  border-color: #fde68a;
}

.usage-label {
  font-size: 13px;
  color: #64748b;
  margin-bottom: 12px;
}

.usage-value {
  font-size: 28px;
  font-weight: 700;
  line-height: 1;
}

.mock-import-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: #f8fafc;
  padding: 12px 16px;
  border-radius: 8px;
  border: 1px solid #e2e8f0;
}

.file-input {
  font-size: 13px;
  color: #475569;
}
</style>
