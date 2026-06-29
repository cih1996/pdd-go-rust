<script setup lang="ts">
import { reactive, ref, watch, computed } from 'vue'
import { ElMessage } from 'element-plus'
import { Plus, DocumentAdd, Setting, Timer, Warning, Setting as SettingIcon } from '@element-plus/icons-vue'
import type { SystemConfig, UrlTemplateRecord } from '../types'

const props = defineProps<{
  config: SystemConfig
  saving: boolean
}>()

const emit = defineEmits<{
  (event: 'save', payload: SystemConfig): void
}>()

const form = reactive<SystemConfig>({
  open_url_delay_seconds: 2,
  click_image_delay_seconds: 1.2,
  max_task_sku_count: 0,
  external_api_enabled: false,
  use_url_templates: false,
  url_templates: [],
})

const batchImportDialogVisible = ref(false)
const batchImportText = ref('')
const batchImportReplace = ref(false)
const hasUnsavedChanges = ref(false)
let syncingFromProps = false
let lastSyncedSignature = ''

function createUrlTemplateRecord(template = '', name = ''): UrlTemplateRecord {
  return {
    id: `urltpl_${Date.now()}_${Math.random().toString(16).slice(2, 8)}`,
    name,
    template,
    trigger_count: 0,
    success_count: 0,
    risk_count: 0,
  }
}

function normalizedConfigSignature(value: SystemConfig) {
  return JSON.stringify({
    open_url_delay_seconds: Number(value.open_url_delay_seconds),
    click_image_delay_seconds: Number(value.click_image_delay_seconds),
    max_task_sku_count: Number(value.max_task_sku_count),
    external_api_enabled: Boolean(value.external_api_enabled),
    use_url_templates: Boolean(value.use_url_templates),
    url_templates: (value.url_templates ?? []).map((item) => ({
      id: item.id,
      name: item.name ?? '',
      template: item.template ?? '',
      trigger_count: Number(item.trigger_count ?? 0),
      success_count: Number(item.success_count ?? 0),
      risk_count: Number(item.risk_count ?? 0),
    })),
  })
}

function applyConfigToForm(value: SystemConfig) {
  syncingFromProps = true
  form.open_url_delay_seconds = value.open_url_delay_seconds
  form.click_image_delay_seconds = value.click_image_delay_seconds
  form.max_task_sku_count = value.max_task_sku_count
  form.external_api_enabled = value.external_api_enabled
  form.use_url_templates = value.use_url_templates
  form.url_templates = value.url_templates.map((item) => ({ ...item }))
  lastSyncedSignature = normalizedConfigSignature(value)
  hasUnsavedChanges.value = false
  syncingFromProps = false
}

watch(
  () => props.config,
  (value) => {
    if (hasUnsavedChanges.value && !props.saving) {
      return
    }
    applyConfigToForm(value)
  },
  { immediate: true, deep: true },
)

watch(
  form,
  () => {
    if (syncingFromProps) {
      return
    }
    hasUnsavedChanges.value = normalizedConfigSignature(form) !== lastSyncedSignature
  },
  { deep: true },
)

function addUrlTemplate() {
  form.url_templates.push(createUrlTemplateRecord())
}

function removeUrlTemplate(index: number) {
  form.url_templates.splice(index, 1)
}

function moveUrlTemplate(index: number, direction: 'up' | 'down') {
  const targetIndex = direction === 'up' ? index - 1 : index + 1
  if (targetIndex < 0 || targetIndex >= form.url_templates.length) return
  const [current] = form.url_templates.splice(index, 1)
  if (!current) return
  form.url_templates.splice(targetIndex, 0, current)
}

function submitBatchImport() {
  const lines = batchImportText.value.split('\n').map(line => line.trim()).filter(line => line)
  if (lines.length === 0) {
    ElMessage.warning('请输入要导入的 URL 模板')
    return
  }

  const newTemplates = lines.map(line => createUrlTemplateRecord(line))
  
  if (batchImportReplace.value) {
    form.url_templates = newTemplates
  } else {
    form.url_templates.push(...newTemplates)
  }
  
  ElMessage.success(`成功导入 ${newTemplates.length} 个 URL 模板`)
  batchImportDialogVisible.value = false
  batchImportText.value = ''
}

function submit() {
  if (form.open_url_delay_seconds < 0 || form.click_image_delay_seconds < 0) {
    ElMessage.warning('延迟不能小于 0 秒')
    return
  }
  emit('save', {
    open_url_delay_seconds: Number(form.open_url_delay_seconds),
    click_image_delay_seconds: Number(form.click_image_delay_seconds),
    max_task_sku_count: Number(form.max_task_sku_count),
    external_api_enabled: form.external_api_enabled,
    use_url_templates: form.use_url_templates,
    url_templates: form.url_templates
      .map((item) => ({
        ...item,
        name: item.name?.trim() || '',
        template: item.template.trim(),
      }))
      .filter((item) => item.template.length > 0),
  })
}

const totalTriggerCount = computed(() => form.url_templates.reduce((sum, item) => sum + item.trigger_count, 0))
const totalSuccessCount = computed(() => form.url_templates.reduce((sum, item) => sum + item.success_count, 0))
const totalRiskCount = computed(() => form.url_templates.reduce((sum, item) => sum + item.risk_count, 0))
</script>

<template>
  <div class="system-config-page">
    <div class="page-header mb-6">
      <div class="header-info">
        <h2 class="page-title">系统核心配置</h2>
        <p class="page-desc">全局任务流转延迟、风控及跳转 URL 路由规则设置</p>
      </div>
      <div class="header-actions">
        <el-tag v-if="hasUnsavedChanges" type="warning" effect="light" round class="mr-3">
          编辑中，自动刷新不会覆盖当前内容
        </el-tag>
        <el-button type="primary" size="large" :icon="Setting" :loading="saving" @click="submit" class="save-btn">
          保存所有配置
        </el-button>
      </div>
    </div>

    <div class="config-grid">
      <!-- Left Panel: Core Timing & Limits -->
      <div class="left-panel">
        <el-card shadow="never" class="modern-card mb-6">
          <template #header>
            <div class="card-title"><el-icon class="mr-2"><Timer /></el-icon>时序与限制</div>
          </template>

          <el-form label-position="top" size="large">
            <el-form-item label="跳转 URL 后延迟 (秒)">
              <el-input-number
                v-model="form.open_url_delay_seconds"
                :min="0"
                :step="0.1"
                :precision="1"
                controls-position="right"
                class="w-full"
              />
              <div class="field-tip">建议预留足够的页面加载时间，避免太快导致截图白屏</div>
            </el-form-item>

            <el-form-item label="触发点击图后延迟 (秒)">
              <el-input-number
                v-model="form.click_image_delay_seconds"
                :min="0"
                :step="0.1"
                :precision="1"
                controls-position="right"
                class="w-full"
              />
              <div class="field-tip">点击目标区域后，等待跳转到下一个界面的过渡时间</div>
            </el-form-item>

            <el-divider border-style="dashed" class="my-6" />

            <el-form-item label="候选任务 SKU 数量上限">
              <el-input-number
                v-model="form.max_task_sku_count"
                :min="0"
                :step="1"
                :precision="0"
                controls-position="right"
                class="w-full"
              />
              <div class="field-tip text-warning">
                <el-icon><Warning /></el-icon> 0 表示不限制。大于 0 时，并发领取到的任务如果 SKU 数量超过此值，会立即回传取消，不进入候选区。
              </div>
            </el-form-item>

            <el-form-item label="外部任务 API">
              <div class="switch-row">
                <el-switch
                  v-model="form.external_api_enabled"
                  inline-prompt
                  active-text="开启"
                  inactive-text="关闭"
                />
                <a href="/external-api.html" target="_blank" rel="noopener" class="doc-link">查看接口说明</a>
              </div>
              <div class="field-tip">开启后可通过业务端的外部接口直接领取任务和提交任务结果，不依赖 ADB 开始按钮。</div>
            </el-form-item>
          </el-form>
        </el-card>
      </div>

      <!-- Right Panel: URL Templates -->
      <div class="right-panel">
        <el-card shadow="never" class="modern-card">
          <template #header>
            <div class="flex-between">
              <div class="card-title"><el-icon class="mr-2"><SettingIcon /></el-icon>跳转链接模板路由</div>
              <el-switch
                v-model="form.use_url_templates"
                inline-prompt
                active-text="启用"
                inactive-text="关闭"
              />
            </div>
          </template>

          <el-alert
            v-if="!form.use_url_templates"
            title="当前已关闭自定义 URL 模板。任务流转将默认使用上游直接下发的链接。"
            type="info"
            show-icon
            :closable="false"
            class="mb-4"
          />

          <div v-else class="template-workspace">
            <div class="flex-between mb-4">
              <div class="stats-summary">
                <span class="stat-badge total">共 {{ form.url_templates.length }} 个</span>
                <span class="stat-badge blue">触发 {{ totalTriggerCount }}</span>
                <span class="stat-badge green">成功 {{ totalSuccessCount }}</span>
                <span class="stat-badge orange">风控 {{ totalRiskCount }}</span>
              </div>
              <div class="action-group">
                <el-button plain @click="batchImportDialogVisible = true" :icon="DocumentAdd">批量导入</el-button>
                <el-button type="primary" plain @click="addUrlTemplate" :icon="Plus">新增一行</el-button>
              </div>
            </div>

            <div v-if="form.url_templates.length === 0" class="empty-state">
              <el-empty description="暂无 URL 模板，请添加或批量导入" :image-size="80">
                <el-button type="primary" @click="batchImportDialogVisible = true">立即导入</el-button>
              </el-empty>
            </div>

            <div v-else class="template-list">
              <div
                v-for="(item, index) in form.url_templates"
                :key="item.id"
                class="template-item-card"
              >
                <div class="item-header">
                  <div class="item-index">
                    <span class="index-num">{{ index + 1 }}</span>
                    <span class="item-id">ID: {{ item.id.split('_').pop() }}</span>
                  </div>
                  <div class="item-actions">
                    <el-button link type="info" @click="moveUrlTemplate(index, 'up')" :disabled="index === 0">上移</el-button>
                    <el-button link type="info" @click="moveUrlTemplate(index, 'down')" :disabled="index === form.url_templates.length - 1">下移</el-button>
                    <el-button link type="danger" @click="removeUrlTemplate(index)">删除</el-button>
                  </div>
                </div>
                
                <el-input
                  v-model="item.name"
                  placeholder="模板名称，例如：鞋类登录链路 / 靴子专用 / 默认模板"
                  class="mb-4"
                />

                <el-input
                  v-model="item.template"
                  type="textarea"
                  :rows="2"
                  placeholder="填写一个真实示例 URL，系统会自动把其中的 goods_id 和 sku_id 替换为实际任务值"
                  class="url-input"
                />
                
                <div class="item-footer">
                  <div class="stats-group">
                    <span class="stat-item text-blue" title="触发"><i class="stat-icon">🎯</i> {{ item.trigger_count }}</span>
                    <span class="stat-divider">/</span>
                    <span class="stat-item text-green" title="成功"><i class="stat-icon">✅</i> {{ item.success_count }}</span>
                    <span class="stat-divider">/</span>
                    <span class="stat-item text-orange" title="风控"><i class="stat-icon">⚠️</i> {{ item.risk_count }}</span>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </el-card>
      </div>
    </div>

    <!-- Batch Import Dialog -->
    <el-dialog
      v-model="batchImportDialogVisible"
      title="批量导入 URL 模板"
      width="600px"
      destroy-on-close
      class="modern-dialog"
    >
      <el-alert
        title="每行填写一个完整的跳转链接，系统会自动清洗并将其中的参数替换为模板变量。"
        type="info"
        :closable="false"
        class="mb-4"
      />
      
      <el-form label-position="top">
        <el-form-item label="导入模式">
          <el-radio-group v-model="batchImportReplace">
            <el-radio-button :value="false">追加到现有列表底部</el-radio-button>
            <el-radio-button :value="true">清空现有列表并覆盖</el-radio-button>
          </el-radio-group>
        </el-form-item>
        
        <el-form-item label="链接列表 (每行一个)">
          <el-input
            v-model="batchImportText"
            type="textarea"
            :rows="12"
            placeholder="pinduoduo://com.xunmeng.pinduoduo/goods.html?goods_id=xxx&sku_id=xxx
https://mobile.yangkeduo.com/goods.html?goods_id=yyy"
            class="batch-input"
          />
        </el-form-item>
      </el-form>
      
      <template #footer>
        <el-button @click="batchImportDialogVisible = false" size="large">取消</el-button>
        <el-button type="primary" @click="submitBatchImport" size="large" class="px-6">
          确认导入
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.system-config-page {
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

.save-btn {
  font-weight: 500;
  letter-spacing: 0.5px;
  padding-left: 32px;
  padding-right: 32px;
}

.config-grid {
  display: grid;
  grid-template-columns: 360px 1fr;
  gap: 24px;
  align-items: start;
}

@media (max-width: 1200px) {
  .config-grid {
    grid-template-columns: 1fr;
  }
}

.left-panel {
  display: flex;
  flex-direction: column;
}

.right-panel {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.card-title {
  display: flex;
  align-items: center;
  font-size: 16px;
.switch-row {
  display: flex;
  align-items: center;
  gap: 12px;
}
.doc-link {
  color: #2563eb;
  text-decoration: none;
  font-size: 13px;
}
.doc-link:hover {
  text-decoration: underline;
}
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

.mr-2 { margin-right: 8px; }
.mb-4 { margin-bottom: 16px; }
.mb-6 { margin-bottom: 24px; }
.my-6 { margin-top: 24px; margin-bottom: 24px; }

.field-tip {
  margin-top: 6px;
  font-size: 12px;
  color: #64748b;
  line-height: 1.5;
}

.text-warning {
  color: #d97706;
  display: flex;
  align-items: flex-start;
  gap: 4px;
}
.text-warning .el-icon {
  margin-top: 2px;
}

/* URL Templates Area */
.template-workspace {
  background: #f8fafc;
  border-radius: 8px;
  padding: 16px;
  border: 1px solid #e2e8f0;
}

.stats-summary {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.stat-badge {
  padding: 4px 10px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 600;
}
.stat-badge.total { background: #e2e8f0; color: #475569; }
.stat-badge.blue { background: #e0f2fe; color: #0369a1; }
.stat-badge.green { background: #dcfce7; color: #047857; }
.stat-badge.orange { background: #fef3c7; color: #b45309; }

.action-group {
  display: flex;
  gap: 12px;
}

.template-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
  max-height: 600px;
  overflow-y: auto;
  padding-right: 4px;
}

.template-item-card {
  background: #ffffff;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  padding: 12px;
  transition: all 0.2s ease;
}
.template-item-card:hover {
  border-color: #cbd5e1;
  box-shadow: 0 2px 4px rgba(0, 0, 0, 0.02);
}

.item-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
}

.item-index {
  display: flex;
  align-items: center;
  gap: 8px;
}

.index-num {
  background: #f1f5f9;
  color: #475569;
  width: 24px;
  height: 24px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  font-size: 12px;
  font-weight: 600;
}

.item-id {
  font-size: 12px;
  color: #94a3b8;
  font-family: 'SFMono-Regular', Consolas, monospace;
}

.item-actions {
  display: flex;
  gap: 4px;
}

.url-input :deep(.el-textarea__inner) {
  font-family: 'SFMono-Regular', Consolas, monospace;
  font-size: 13px;
  background-color: #f8fafc;
  color: #334155;
}

.item-footer {
  display: flex;
  justify-content: flex-end;
  margin-top: 8px;
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
  font-size: 12px;
  display: flex;
  align-items: center;
  gap: 4px;
}

.stat-icon {
  font-style: normal;
}

.stat-divider {
  margin: 0 8px;
  color: #cbd5e1;
  font-size: 12px;
}

.text-blue { color: #0ea5e9; }
.text-green { color: #10b981; }
.text-orange { color: #f59e0b; }

.px-6 {
  padding-left: 24px;
  padding-right: 24px;
}

.batch-input :deep(.el-textarea__inner) {
  font-family: 'SFMono-Regular', Consolas, monospace;
  font-size: 13px;
  line-height: 1.6;
  white-space: pre;
}

.modern-dialog :deep(.el-dialog__header) {
  font-weight: 600;
  border-bottom: 1px solid #f1f5f9;
  margin-right: 0;
  padding-bottom: 16px;
}
</style>
