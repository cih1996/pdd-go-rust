<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { UpstreamConfigRecord } from '../types'

type UpstreamType = 'mock_upstream' | 'laoqian_worker' | 'custom_http'

const props = defineProps<{
  upstreams: UpstreamConfigRecord[]
  savingUpstreamId: string
}>()

const emit = defineEmits<{
  (event: 'create', payload: {
    name?: string | null
    upstream_type: UpstreamType
    enabled?: boolean
    priority?: number
    base_url: string
    token?: string | null
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
      token?: string | null
      notes?: string | null
    }
  }): void
  (event: 'toggle', payload: { upstreamId: string; enabled: boolean }): void
  (event: 'delete', upstreamId: string): void
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

const editingId = ref('')
const form = reactive({
  name: '',
  upstream_type: 'laoqian_worker' as UpstreamType,
  enabled: true,
  priority: 100,
  base_url: 'https://frontend.yqlaoqian111.com',
  token: '',
  notes: '',
})

const currentTypeOption = computed(() => {
  return typeOptions.find((item) => item.value === form.upstream_type) ?? typeOptions[0]!
})

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
  form.token = ''
  form.notes = ''
}

function buildPayload() {
  return {
    name: form.name.trim() || null,
    upstream_type: form.upstream_type,
    enabled: form.enabled,
    priority: Number(form.priority) || 0,
    base_url: form.base_url.trim(),
    token: form.token.trim() || null,
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
    return
  }
  emit('create', payload)
}

function startEdit(row: UpstreamConfigRecord) {
  editingId.value = row.id
  form.name = row.name ?? ''
  form.upstream_type = row.upstream_type
  form.enabled = row.enabled
  form.priority = row.priority
  form.base_url = row.base_url
  form.token = row.token ?? ''
  form.notes = row.notes ?? ''
}

async function confirmDelete(upstreamId: string) {
  await ElMessageBox.confirm('删除后该上游下的平台账号将无法继续取任务，是否继续？', '删除上游配置', {
    type: 'warning',
  })
  emit('delete', upstreamId)
}

function maskToken(token?: string | null) {
  if (!token) return '未配置'
  if (token.length <= 8) return '*'.repeat(token.length)
  return `${token.slice(0, 4)}***${token.slice(-4)}`
}
</script>

<template>
  <div class="stack">
    <el-card shadow="never" class="mb-4">
      <template #header>
        <div class="card-header">
          <span>{{ editingId ? '编辑上游配置' : '新增上游配置' }}</span>
          <el-button v-if="editingId" text @click="resetForm">取消编辑</el-button>
        </div>
      </template>

      <el-form label-position="top" class="upstream-form">
        <el-row :gutter="16">
          <el-col :md="8" :sm="12" :xs="24">
            <el-form-item label="上游类型">
              <el-select v-model="form.upstream_type">
                <el-option
                  v-for="item in typeOptions"
                  :key="item.value"
                  :label="item.label"
                  :value="item.value"
                />
              </el-select>
              <div class="field-tip">{{ currentTypeOption.description }}</div>
            </el-form-item>
          </el-col>
          <el-col :md="8" :sm="12" :xs="24">
            <el-form-item label="上游名称">
              <el-input v-model="form.name" placeholder="可留空，系统按类型自动命名" />
            </el-form-item>
          </el-col>
          <el-col :md="8" :sm="12" :xs="24">
            <el-form-item label="优先级">
              <el-input-number v-model="form.priority" :min="0" :max="9999" style="width: 100%;" />
            </el-form-item>
          </el-col>
        </el-row>

        <el-row :gutter="16">
          <el-col :md="12" :sm="24" :xs="24">
            <el-form-item label="基础地址">
              <el-input v-model="form.base_url" placeholder="例如：http://127.0.0.1:8102" />
            </el-form-item>
          </el-col>
          <el-col :md="12" :sm="24" :xs="24">
            <el-form-item label="默认密钥">
              <el-input
                v-model="form.token"
                type="textarea"
                :rows="2"
                placeholder="可选；custom_http 可填 Bearer Token，老钱模式一般由平台账号页维护账号密钥"
              />
            </el-form-item>
          </el-col>
        </el-row>

        <el-form-item label="备注">
          <el-input v-model="form.notes" type="textarea" :rows="2" placeholder="可选备注" />
        </el-form-item>

        <div class="form-actions">
          <el-switch v-model="form.enabled" inline-prompt active-text="启用" inactive-text="停用" />
          <div class="action-buttons">
            <el-button v-if="editingId" @click="resetForm">取消</el-button>
            <el-button type="primary" :loading="savingUpstreamId === (editingId || '__creating__')" @click="submitForm">
              {{ editingId ? '保存修改' : '创建上游' }}
            </el-button>
          </div>
        </div>
      </el-form>
    </el-card>

    <el-card shadow="never">
      <template #header>
        <span style="font-size: 16px; font-weight: 500;">上游列表</span>
      </template>

      <el-table :data="props.upstreams" border stripe>
        <el-table-column prop="name" label="名称" min-width="160" show-overflow-tooltip />
        <el-table-column prop="code" label="编码" min-width="170" show-overflow-tooltip />
        <el-table-column label="类型" min-width="130">
          <template #default="{ row }">
            {{ row.upstream_type }}
          </template>
        </el-table-column>
        <el-table-column prop="base_url" label="基础地址" min-width="220" show-overflow-tooltip />
        <el-table-column label="默认密钥" min-width="140" show-overflow-tooltip>
          <template #default="{ row }">
            {{ maskToken(row.token) }}
          </template>
        </el-table-column>
        <el-table-column label="统计" min-width="170">
          <template #default="{ row }">
            取 {{ row.stats.fetched_count }} / 成 {{ row.stats.reported_success_count }} / 败 {{ row.stats.reported_failure_count }}
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="row.enabled ? 'success' : 'info'">
              {{ row.enabled ? '启用中' : '已停用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="220" fixed="right">
          <template #default="{ row }">
            <el-button text :loading="savingUpstreamId === row.id" @click="startEdit(row)">编辑</el-button>
            <el-button
              text
              :loading="savingUpstreamId === row.id"
              @click="emit('toggle', { upstreamId: row.id, enabled: !row.enabled })"
            >
              {{ row.enabled ? '停用' : '启用' }}
            </el-button>
            <el-button text type="danger" :loading="savingUpstreamId === row.id" @click="confirmDelete(row.id)">
              删除
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>
  </div>
</template>

<style scoped>
.upstream-form {
  max-width: 960px;
}

.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  font-size: 16px;
  font-weight: 500;
}

.field-tip {
  margin-top: 6px;
  font-size: 12px;
  color: #909399;
  line-height: 1.5;
}

.form-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.action-buttons {
  display: flex;
  gap: 8px;
}
</style>
