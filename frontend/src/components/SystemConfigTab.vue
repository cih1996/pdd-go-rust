<script setup lang="ts">
import { reactive, watch } from 'vue'
import { ElMessage } from 'element-plus'
import type { SystemConfig, UrlTemplateRecord } from '../types'

const props = defineProps<{
  config: SystemConfig
  saving: boolean
}>()

const emit = defineEmits<{
  (event: 'save', payload: SystemConfig): void
}>()

const goodsIdToken = '{{goods_id}}'
const skuIdToken = '{{sku_id}}'

const form = reactive<SystemConfig>({
  open_url_delay_seconds: 2,
  click_image_delay_seconds: 1.2,
  max_task_sku_count: 0,
  use_url_templates: false,
  url_templates: [],
})

function createUrlTemplateRecord(template = ''): UrlTemplateRecord {
  return {
    id: `urltpl_${Date.now()}_${Math.random().toString(16).slice(2, 8)}`,
    template,
    trigger_count: 0,
    success_count: 0,
    risk_count: 0,
  }
}

watch(
  () => props.config,
  (value) => {
    form.open_url_delay_seconds = value.open_url_delay_seconds
    form.click_image_delay_seconds = value.click_image_delay_seconds
    form.max_task_sku_count = value.max_task_sku_count
    form.use_url_templates = value.use_url_templates
    form.url_templates = value.url_templates.map((item) => ({ ...item }))
  },
  { immediate: true, deep: true },
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

function submit() {
  if (form.open_url_delay_seconds < 0 || form.click_image_delay_seconds < 0) {
    ElMessage.warning('延迟不能小于 0 秒')
    return
  }
  emit('save', {
    open_url_delay_seconds: Number(form.open_url_delay_seconds),
    click_image_delay_seconds: Number(form.click_image_delay_seconds),
    max_task_sku_count: Number(form.max_task_sku_count),
    use_url_templates: form.use_url_templates,
    url_templates: form.url_templates
      .map((item) => ({
        ...item,
        template: item.template.trim(),
      }))
      .filter((item) => item.template.length > 0),
  })
}
</script>

<template>
  <div class="stack">
    <el-card shadow="never" class="mb-4">
      <template #header>
        <span style="font-size: 16px; font-weight: 500;">系统配置</span>
      </template>



      <el-form label-position="top" class="system-config-form">
        <el-form-item label="跳转 URL 后延迟（秒）">
          <el-input-number
            v-model="form.open_url_delay_seconds"
            :min="0"
            :step="0.1"
            :precision="1"
            controls-position="right"
          />
     
        </el-form-item>

        <el-form-item label="触发点击图后延迟（秒）">
          <el-input-number
            v-model="form.click_image_delay_seconds"
            :min="0"
            :step="0.1"
            :precision="1"
            controls-position="right"
          />
        </el-form-item>

        <el-form-item label="候选任务 SKU 数量上限">
          <el-input-number
            v-model="form.max_task_sku_count"
            :min="0"
            :step="1"
            :precision="0"
            controls-position="right"
          />
          <div class="config-tip">
            0 表示不限制。大于 0 时，并发领取到的任务如果 SKU 数量超过这个值，会立即回传取消，不进入候选区。
          </div>
        </el-form-item>

        <el-divider content-position="left">跳转链接模板</el-divider>

        <el-form-item label="启用自定义 URL 模板">
          <el-switch
            v-model="form.use_url_templates"
            active-text="启用"
            inactive-text="关闭"
          />
       
        </el-form-item>

        <el-form-item label="URL 模板列表">
          <div class="url-template-list">
            <div
              v-for="(item, index) in form.url_templates"
              :key="item.id"
              class="url-template-item"
            >
              <div class="url-template-head">
                <span>模板 {{ index + 1 }}</span>
                <div class="url-template-actions">
                  <el-button text size="small" @click="moveUrlTemplate(index, 'up')" :disabled="index === 0">上移</el-button>
                  <el-button text size="small" @click="moveUrlTemplate(index, 'down')" :disabled="index === form.url_templates.length - 1">下移</el-button>
                  <el-button text size="small" type="danger" @click="removeUrlTemplate(index)">删除</el-button>
                </div>
              </div>
              <el-input
                v-model="item.template"
                type="textarea"
                :rows="3"
                placeholder="例如：https://mobile.yangkeduo.com/order_checkout.html?goods_id={{goods_id}}&sku_id={{sku_id}}"
              />
              <div class="url-template-stats">
                <el-tag size="small" type="info">触发 {{ item.trigger_count }}</el-tag>
                <el-tag size="small" type="success">成功 {{ item.success_count }}</el-tag>
                <el-tag size="small" type="warning">风控 {{ item.risk_count }}</el-tag>
              </div>
            </div>
            <el-button plain @click="addUrlTemplate">新增模板 URL</el-button>
          </div>
          <div class="config-tip">
            支持变量 {{ goodsIdToken }} / {{ skuIdToken }}，也支持 {goods_id} / {sku_id}。如果模板本身已带 query 参数，系统会自动覆盖 goods_id 和 sku_id。触发次数表示实际用了该模板跳转，成功次数包含失败释放和成功图，只有命中账号风控才计入风控次数。
          </div>
        </el-form-item>

        <el-button type="primary" :loading="saving" @click="submit">保存配置</el-button>
      </el-form>
    </el-card>
  </div>
</template>

<style scoped>
.system-config-form {
  max-width: 560px;
}

.config-tip {
  margin-top: 8px;
  color: #909399;
  font-size: 12px;
  line-height: 1.6;
}

.url-template-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
  width: 100%;
}

.url-template-item {
  border: 1px solid #ebeef5;
  border-radius: 8px;
  padding: 12px;
  background: #fff;
}

.url-template-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  margin-bottom: 8px;
  font-size: 13px;
  color: #303133;
}

.url-template-actions {
  display: flex;
  align-items: center;
  gap: 4px;
}

.url-template-stats {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  margin-top: 8px;
}
</style>
