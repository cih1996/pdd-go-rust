<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { PlatformAccountRecord, UpstreamOption } from '../types'

const props = defineProps<{
  accounts: PlatformAccountRecord[]
  upstreamOptions: UpstreamOption[]
  importing: boolean
  savingAccountId: string
}>()

const emit = defineEmits<{
  (event: 'import', payload: { upstream_code: string; lines: string }): void
  (event: 'toggle', payload: { accountId: string; enabled: boolean }): void
  (event: 'delete', accountId: string): void
}>()

const form = reactive({
  upstream_code: '',
  lines: '',
})

const enabledUpstreams = computed(() => props.upstreamOptions.filter((item) => item.enabled))

watch(
  enabledUpstreams,
  (value) => {
    const first = value[0]
    if (!form.upstream_code && first) {
      form.upstream_code = first.code
    }
  },
  { immediate: true },
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
  <div class="stack">
    <el-card shadow="never" class="mb-4">
      <template #header>
        <span style="font-size: 16px; font-weight: 500;">平台账号</span>
      </template>

      <el-alert
        title="这里管理的是账号池。启用状态的账号会由后端自动轮询抢单，再把任务派发给空闲设备执行。"
        type="info"
        :closable="false"
        class="mb-4"
      />

      <el-form label-position="top" class="account-form">
        <el-form-item label="上游类型">
          <el-select v-model="form.upstream_code" placeholder="请选择上游类型">
            <el-option
              v-for="item in enabledUpstreams"
              :key="item.code"
              :label="`${item.name}（${item.upstream_type}）`"
              :value="item.code"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="批量添加账号">
          <el-input
            v-model="form.lines"
            type="textarea"
            :rows="8"
            placeholder="每行一个账号，格式：账号备注,account|secret_key&#10;如果不写备注，也可以直接写：account|secret_key"
          />
        </el-form-item>
        <el-button type="primary" :loading="importing" @click="submitImport">批量导入</el-button>
      </el-form>
    </el-card>

    <el-card shadow="never">
      <template #header>
        <span style="font-size: 16px; font-weight: 500;">账号列表</span>
      </template>

      <el-table :data="accounts" border stripe>
        <el-table-column prop="name" label="账号备注" min-width="160" show-overflow-tooltip />
        <el-table-column label="上游类型" min-width="180" show-overflow-tooltip>
          <template #default="{ row }">
            {{ row.upstream_type }} / {{ row.upstream_code }}
          </template>
        </el-table-column>
        <el-table-column label="账号池状态" min-width="180" show-overflow-tooltip>
          <template #default="{ row }">
            {{ row.enabled ? '参与自动抢单' : '已从账号池停用' }}
          </template>
        </el-table-column>
        <el-table-column label="领取次数" width="100">
          <template #default="{ row }">
            {{ row.stats.fetched_count }}
          </template>
        </el-table-column>
        <el-table-column label="成功提交" width="100">
          <template #default="{ row }">
            <span style="color: #67c23a;">{{ row.stats.reported_success_count }}</span>
          </template>
        </el-table-column>
        <el-table-column label="失败提交" width="100">
          <template #default="{ row }">
            <span style="color: #f56c6c;">{{ row.stats.reported_failure_count }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="row.enabled ? 'success' : 'info'">
              {{ row.enabled ? '启用中' : '已停用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="180" fixed="right">
          <template #default="{ row }">
            <el-button
              text
              :loading="savingAccountId === row.id"
              @click="emit('toggle', { accountId: row.id, enabled: !row.enabled })"
            >
              {{ row.enabled ? '停用' : '启用' }}
            </el-button>
            <el-button text type="danger" :loading="savingAccountId === row.id" @click="confirmDelete(row.id)">
              删除
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>
  </div>
</template>

<style scoped>
.account-form {
  max-width: 720px;
}
</style>
