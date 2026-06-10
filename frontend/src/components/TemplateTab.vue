<script setup lang="ts">
import { ref, computed } from 'vue'
import MatchPreview from './MatchPreview.vue'
import type { DeviceInfo, TemplateRecord, TemplateTestResult } from '../types'
import { Plus, Edit, Delete, VideoPlay, Upload, Monitor } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'

const props = defineProps<{
  templates: TemplateRecord[]
  devices: DeviceInfo[]
  saving: boolean
  testingId: string
  testResult: TemplateTestResult | null
  importingTemplatePack: boolean
}>()

const emit = defineEmits<{
  (event: 'create-template', payload: FormData): void
  (event: 'update-template', templateId: string, payload: FormData): void
  (event: 'delete-template', templateId: string): void
  (event: 'move-template', templateId: string, direction: 'up' | 'down'): void
  (event: 'test-template', templateId: string, payload: FormData): void
  (event: 'export-templates'): void
  (event: 'import-templates', payload: { file: File; replaceExisting: boolean }): void
}>()

const form = ref({
  label: '',
  templateType: 'success_image',
  recognitionEngine: 'opencv',
  priority: '100',
  expectedText: '',
  threshold: '0.8',
  method: 'ccoeff_normed',
  grayscale: true,
  cropX: '',
  cropY: '',
  cropWidth: '',
  cropHeight: '',
})

const fileList = ref<any[]>([])

const testSourceMode = ref<'device' | 'upload'>('device')
const testDeviceId = ref('')
const testFileList = ref<any[]>([])
const importReplaceExisting = ref(false)
const importFileList = ref<any[]>([])

const editingId = ref('')
const editFileList = ref<any[]>([])
const editForm = ref({
  label: '',
  templateType: 'success_image',
  recognitionEngine: 'opencv',
  priority: '100',
  expectedText: '',
  threshold: '0.8',
  method: 'ccoeff_normed',
  grayscale: true,
  cropX: '',
  cropY: '',
  cropWidth: '',
  cropHeight: '',
})

const searchTemplate = ref('')
const activeTypeTab = ref<'account_risk' | 'fail_release' | 'click_image' | 'success_image'>('account_risk')

const templatesByType = computed(() => {
  const base = props.templates.filter((template) => {
    if (!searchTemplate.value) return true
    return template.label.includes(searchTemplate.value)
  })
  return {
    account_risk: base.filter((item) => item.template_type === 'account_risk'),
    fail_release: base.filter((item) => item.template_type === 'fail_release'),
    click_image: base.filter((item) => item.template_type === 'click_image'),
    success_image: base.filter((item) => item.template_type === 'success_image'),
  }
})

const activeTemplates = computed(() => templatesByType.value[activeTypeTab.value])

const handleFileChange = (file: any) => {
  fileList.value = [file]
}

const handleTestFileChange = (file: any) => {
  testFileList.value = [file]
}

const handleEditFileChange = (file: any) => {
  editFileList.value = [file]
}

const handleImportFileChange = (file: any) => {
  importFileList.value = [file]
}

function submitCreate() {
  if (!form.value.label.trim()) {
    ElMessage.warning('请填写模板名称')
    return
  }
  if (form.value.recognitionEngine === 'opencv' && fileList.value.length === 0) {
    ElMessage.warning('OpenCV 模板必须上传模板图片')
    return
  }
  if (form.value.recognitionEngine === 'ocr' && !form.value.expectedText.trim()) {
    ElMessage.warning('OCR 模板必须填写期望文本')
    return
  }
  const payload = new FormData()
  payload.set('label', form.value.label.trim())
  payload.set('template_type', form.value.templateType)
  payload.set('recognition_engine', form.value.recognitionEngine)
  payload.set('priority', form.value.priority)
  payload.set('expected_text', form.value.expectedText)
  payload.set('threshold', form.value.threshold)
  payload.set('method', form.value.method)
  payload.set('grayscale', String(form.value.grayscale))
  if (fileList.value.length > 0) {
    payload.set('image', fileList.value[0].raw)
  }
  if (form.value.cropX && form.value.cropY && form.value.cropWidth && form.value.cropHeight) {
    payload.set('crop_x', form.value.cropX)
    payload.set('crop_y', form.value.cropY)
    payload.set('crop_width', form.value.cropWidth)
    payload.set('crop_height', form.value.cropHeight)
  }
  emit('create-template', payload)
  fileList.value = []
  form.value.label = ''
  form.value.recognitionEngine = 'opencv'
  form.value.priority = '100'
  form.value.expectedText = ''
}

function submitTest(templateId: string) {
  const payload = new FormData()

  if (testSourceMode.value === 'device') {
    if (!testDeviceId.value) {
      ElMessage.warning('请选择一个设备后再测试')
      return
    }
    payload.set('device_id', testDeviceId.value)
  } else {
    if (testFileList.value.length === 0) {
      ElMessage.warning('请上传一张大图后再测试')
      return
    }
    payload.set('source_image', testFileList.value[0].raw)
  }

  emit('test-template', templateId, payload)
}

function submitImportTemplates() {
  if (importFileList.value.length === 0) {
    ElMessage.warning('请先选择模板包')
    return
  }
  emit('import-templates', {
    file: importFileList.value[0].raw as File,
    replaceExisting: importReplaceExisting.value,
  })
  importFileList.value = []
}

function openEdit(item: TemplateRecord) {
  editingId.value = item.id
  editFileList.value = []
  editForm.value = {
    label: item.label,
    templateType: item.template_type,
    recognitionEngine: item.recognition_engine,
    priority: String(item.priority),
    expectedText: item.expected_text || '',
    threshold: String(item.threshold),
    method: item.method,
    grayscale: item.grayscale,
    cropX: item.crop?.x != null ? String(item.crop.x) : '',
    cropY: item.crop?.y != null ? String(item.crop.y) : '',
    cropWidth: item.crop?.width != null ? String(item.crop.width) : '',
    cropHeight: item.crop?.height != null ? String(item.crop.height) : '',
  }
}

function cancelEdit() {
  editingId.value = ''
  editFileList.value = []
}

function submitEdit(templateId: string) {
  if (editForm.value.recognitionEngine === 'opencv' && editFileList.value.length === 0) {
    const current = props.templates.find((item) => item.id === templateId)
    if (!current?.image_url) {
      ElMessage.warning('OpenCV 模板必须保留或上传模板图片')
      return
    }
  }
  if (editForm.value.recognitionEngine === 'ocr' && !editForm.value.expectedText.trim()) {
    ElMessage.warning('OCR 模板必须填写期望文本')
    return
  }
  const payload = new FormData()
  payload.set('label', editForm.value.label.trim())
  payload.set('template_type', editForm.value.templateType)
  payload.set('recognition_engine', editForm.value.recognitionEngine)
  payload.set('priority', editForm.value.priority)
  payload.set('expected_text', editForm.value.expectedText)
  payload.set('threshold', editForm.value.threshold)
  payload.set('method', editForm.value.method)
  payload.set('grayscale', String(editForm.value.grayscale))
  if (editForm.value.cropX && editForm.value.cropY && editForm.value.cropWidth && editForm.value.cropHeight) {
    payload.set('crop_x', editForm.value.cropX)
    payload.set('crop_y', editForm.value.cropY)
    payload.set('crop_width', editForm.value.cropWidth)
    payload.set('crop_height', editForm.value.cropHeight)
  }
  if (editFileList.value.length > 0) {
    payload.set('image', editFileList.value[0].raw)
  }
  emit('update-template', templateId, payload)
  cancelEdit()
}

function removeTemplate(templateId: string) {
  ElMessageBox.confirm('确定要删除该模板吗？此操作不可恢复', '警告', {
    confirmButtonText: '确定',
    cancelButtonText: '取消',
    type: 'warning',
  }).then(() => {
    emit('delete-template', templateId)
    if (editingId.value === templateId) {
      cancelEdit()
    }
  }).catch(() => {})
}

function templateTypeLabel(type: TemplateRecord['template_type']) {
  if (type === 'account_risk') return '账号风控'
  if (type === 'fail_release') return '失败释放'
  if (type === 'click_image') return '点击图'
  return '成功图'
}

function recognitionEngineLabel(engine: TemplateRecord['recognition_engine']) {
  return engine === 'ocr' ? 'OCR' : 'OpenCV'
}

function recognitionEngineTag(engine: TemplateRecord['recognition_engine']) {
  return engine === 'ocr' ? 'success' : 'info'
}

function templateTypeTag(type: TemplateRecord['template_type']) {
  if (type === 'account_risk') return 'warning'
  if (type === 'fail_release') return 'danger'
  if (type === 'click_image') return 'warning'
  return 'success'
}

function canMove(item: TemplateRecord, direction: 'up' | 'down') {
  const list = props.templates.filter((template) => template.template_type === item.template_type)
  const index = list.findIndex((template) => template.id === item.id)
  if (index === -1) return false
  return direction === 'up' ? index > 0 : index < list.length - 1
}
</script>

<template>
  <el-row :gutter="24">
    <el-col :span="8">
      <el-card shadow="never" class="mb-4">
        <template #header>
          <div class="flex-between" style="gap: 12px;">
            <span style="font-size: 16px; font-weight: 500;">创建模板</span>
            <div style="display: flex; gap: 8px; flex-wrap: wrap; justify-content: flex-end;">
              <el-button plain @click="emit('export-templates')">导出模板包</el-button>
              <el-upload
                action="#"
                :auto-upload="false"
                :show-file-list="false"
                :on-change="handleImportFileChange"
                :limit="1"
                accept=".zip"
              >
                <el-button plain>选择模板包</el-button>
              </el-upload>
              <el-checkbox v-model="importReplaceExisting">覆盖现有模板</el-checkbox>
              <el-button type="primary" plain :loading="importingTemplatePack" @click="submitImportTemplates">
                导入模板包
              </el-button>
            </div>
          </div>
        </template>

        <div v-if="importFileList.length" class="mb-4" style="font-size: 12px; color: #606266;">
          当前已选择：{{ importFileList[0].name }}
        </div>
        
        <el-form label-position="top" size="default">
          <el-form-item label="模板名称" required>
            <el-input v-model="form.label" placeholder="例如：无优惠券" />
          </el-form-item>
          
          <el-row :gutter="12">
            <el-col :span="12">
              <el-form-item label="模板类型">
                <el-select v-model="form.templateType" class="w-full">
                  <el-option label="账号风控" value="account_risk" />
                  <el-option label="失败释放" value="fail_release" />
                  <el-option label="点击图" value="click_image" />
                  <el-option label="成功图" value="success_image" />
                </el-select>
              </el-form-item>
            </el-col>
            <el-col :span="12">
              <el-form-item label="识别引擎">
                <el-select v-model="form.recognitionEngine" class="w-full">
                  <el-option label="OpenCV 图像匹配" value="opencv" />
                  <el-option label="OCR 文本识别" value="ocr" />
                </el-select>
              </el-form-item>
            </el-col>
          </el-row>

          <el-row :gutter="12">
            <el-col :span="12">
              <el-form-item label="识别顺序">
                <el-input v-model="form.priority" placeholder="同类型越小越优先" />
              </el-form-item>
            </el-col>
            <el-col :span="12">
              <el-form-item :label="form.recognitionEngine === 'ocr' ? 'OCR 置信度阈值' : '匹配阈值'">
                <el-input v-model="form.threshold" :placeholder="form.recognitionEngine === 'ocr' ? '默认 0.8，越高越严格' : '默认 0.8'" />
              </el-form-item>
            </el-col>
          </el-row>

          <el-form-item :label="form.recognitionEngine === 'ocr' ? '期望文本' : '识别成功返回内容'">
            <el-input
              v-model="form.expectedText"
              :placeholder="form.recognitionEngine === 'ocr' ? 'OCR 文本命中规则，多个条件可用 & 连接，例如：店铺优惠&立即支付' : '返回的文本内容'"
            />
          </el-form-item>
          <div v-if="form.recognitionEngine === 'ocr'" class="mb-4" style="font-size: 12px; color: #909399;">
            OCR 模板支持使用 & 连接多个文本条件，只有区域内多个条件同时命中才算识别成功。
          </div>

          <el-row v-if="form.recognitionEngine === 'opencv'" :gutter="12">
            <el-col :span="12">
              <el-form-item label="匹配算法">
                <el-select v-model="form.method" class="w-full">
                  <el-option label="ccoeff_normed" value="ccoeff_normed" />
                  <el-option label="ccorr_normed" value="ccorr_normed" />
                  <el-option label="sqdiff_normed" value="sqdiff_normed" />
                </el-select>
              </el-form-item>
            </el-col>
            <el-col :span="12">
              <el-form-item label="灰度识别">
                <el-switch v-model="form.grayscale" active-text="开启" inactive-text="关闭" />
              </el-form-item>
            </el-col>
          </el-row>

          <el-form-item :label="form.recognitionEngine === 'ocr' ? 'OCR 识别区域 (可选，X, Y, 宽, 高)' : '裁剪区域 (可选，X, Y, 宽, 高)'">
            <el-row :gutter="8">
              <el-col :span="6"><el-input v-model="form.cropX" placeholder="X" /></el-col>
              <el-col :span="6"><el-input v-model="form.cropY" placeholder="Y" /></el-col>
              <el-col :span="6"><el-input v-model="form.cropWidth" placeholder="宽" /></el-col>
              <el-col :span="6"><el-input v-model="form.cropHeight" placeholder="高" /></el-col>
            </el-row>
          </el-form-item>

          <el-form-item v-if="form.recognitionEngine === 'opencv'" label="模板截图" required>
            <el-upload
              class="w-full"
              drag
              action="#"
              :auto-upload="false"
              :on-change="handleFileChange"
              :file-list="fileList"
              :limit="1"
              accept="image/*"
            >
              <el-icon class="el-icon--upload"><upload /></el-icon>
              <div class="el-upload__text">
                将图片拖到此处，或 <em>点击上传</em>
              </div>
            </el-upload>
          </el-form-item>
          <div v-else class="mb-4" style="font-size: 12px; color: #909399;">
            OCR 模板不需要上传小图，建议配合区域坐标限定识别范围。
          </div>

          <el-button type="primary" :icon="Plus" class="w-full" :loading="saving" @click="submitCreate">
            保存模板
          </el-button>
        </el-form>
      </el-card>

      <el-card shadow="never" v-if="testResult">
        <template #header>
          <span style="font-size: 16px; font-weight: 500;">测试结果</span>
        </template>
        <div class="mb-4">
          <p style="margin: 0 0 8px;"><strong>模板：</strong>{{ testResult.template.label }}</p>
          <p style="margin: 0 0 8px;"><strong>引擎：</strong>{{ recognitionEngineLabel(testResult.recognition_engine) }}</p>
          <p style="margin: 0 0 8px;">
            <strong>命中：</strong>
            <el-tag :type="testResult.match.found ? 'success' : 'danger'" size="small">
              {{ testResult.match.found ? '是' : '否' }}
            </el-tag>
          </p>
          <p style="margin: 0 0 8px;"><strong>置信度：</strong>{{ testResult.match.confidence }} / 阈值：{{ testResult.match.threshold }}</p>
          <p v-if="testResult.match.matched_text" style="margin: 0 0 8px;"><strong>命中文本：</strong>{{ testResult.match.matched_text }}</p>
          <p v-if="testResult.match.full_text" style="margin: 0;"><strong>识别全文：</strong>{{ testResult.match.full_text }}</p>
        </div>
        <MatchPreview
          :image-url="testResult.capture_url"
          :top-left="testResult.match.top_left"
          :width="testResult.match.width"
          :height="testResult.match.height"
          :label="testResult.template.label"
        />
      </el-card>
    </el-col>

    <el-col :span="16">
      <el-card shadow="never" style="height: calc(100vh - 112px); display: flex; flex-direction: column;">
        <template #header>
          <div class="flex-between">
            <span style="font-size: 16px; font-weight: 500;">模板列表与测试</span>
            <el-input v-model="searchTemplate" placeholder="搜索模板名称..." clearable style="width: 200px" />
          </div>
        </template>
        
        <div class="mb-4 p-4" style="background: #f8f9fa; border-radius: 8px; padding: 16px;">
          <div style="font-size: 14px; font-weight: 500; margin-bottom: 12px;">全局测试环境配置</div>
          <el-radio-group v-model="testSourceMode" class="mb-4">
            <el-radio-button value="device"><el-icon class="mr-2"><Monitor /></el-icon>当前设备截图</el-radio-button>
            <el-radio-button value="upload"><el-icon class="mr-2"><Upload /></el-icon>上传大图测试</el-radio-button>
          </el-radio-group>
          
          <div v-if="testSourceMode === 'device'" style="max-width: 300px;">
            <el-select v-model="testDeviceId" placeholder="选择设备" class="w-full">
              <el-option v-for="device in devices" :key="device.serial" :label="device.serial" :value="device.serial" />
            </el-select>
          </div>
          <div v-else>
            <el-upload
              action="#"
              :auto-upload="false"
              :on-change="handleTestFileChange"
              :file-list="testFileList"
              :limit="1"
              accept="image/*"
            >
              <el-button type="primary" plain>选择测试大图</el-button>
            </el-upload>
          </div>
        </div>

        <div style="flex: 1; overflow-y: auto;">
          <el-tabs v-model="activeTypeTab" stretch>
            <el-tab-pane name="account_risk">
              <template #label>账号风控 ({{ templatesByType.account_risk.length }})</template>
            </el-tab-pane>
            <el-tab-pane name="fail_release">
              <template #label>失败释放 ({{ templatesByType.fail_release.length }})</template>
            </el-tab-pane>
            <el-tab-pane name="click_image">
              <template #label>点击图 ({{ templatesByType.click_image.length }})</template>
            </el-tab-pane>
            <el-tab-pane name="success_image">
              <template #label>成功图 ({{ templatesByType.success_image.length }})</template>
            </el-tab-pane>
          </el-tabs>

          <div style="margin-bottom: 12px; font-size: 12px; color: #909399;">
            当前仅显示 {{ templateTypeLabel(activeTypeTab) }} 模板，卡片内可直接前移/后移调整同类型识别顺序。
          </div>

          <el-empty v-if="activeTemplates.length === 0" description="当前类型暂无模板" />
          <el-row :gutter="16" v-else>
            <el-col :span="12" v-for="item in activeTemplates" :key="item.id" class="mb-4">
              <el-card shadow="hover" :body-style="{ padding: '0px' }">
                <div style="display: flex; height: 188px;">
                  <div style="width: 140px; border-right: 1px solid #ebeef5; display: flex; align-items: center; justify-content: center; background: #fafafa; padding: 10px;">
                    <el-image v-if="item.image_url" :src="item.image_url" fit="contain" style="max-width: 100%; max-height: 100%; border-radius: 4px;" />
                    <div v-else style="font-size: 13px; color: #909399; text-align: center; line-height: 1.6;">
                      OCR 模板
                      <br />
                      区域识别
                    </div>
                  </div>
                  <div style="flex: 1; padding: 12px; display: flex; flex-direction: column; justify-content: space-between;">
                    <div v-if="editingId !== item.id">
                      <div style="font-weight: 500; font-size: 15px; margin-bottom: 6px; color: #303133;">{{ item.label }}</div>
                      <div style="font-size: 12px; color: #909399; margin-bottom: 4px;">
                        <el-tag size="small" :type="templateTypeTag(item.template_type)">
                          {{ templateTypeLabel(item.template_type) }}
                        </el-tag>
                        <el-tag size="small" :type="recognitionEngineTag(item.recognition_engine)" style="margin-left: 8px;">
                          {{ recognitionEngineLabel(item.recognition_engine) }}
                        </el-tag>
                        <span style="margin-left: 8px;">顺序: {{ item.priority }}</span>
                        <span style="margin-left: 8px;">阈值: {{ item.threshold }}</span>
                      </div>
                      <div style="font-size: 12px; color: #606266; margin-bottom: 8px; min-height: 36px; overflow: hidden; text-overflow: ellipsis;">
                        返回: {{ item.expected_text || '无' }}
                      </div>
                    </div>

                    <div v-if="editingId === item.id" style="flex: 1; overflow-y: auto;">
                      <el-input v-model="editForm.label" size="small" placeholder="名称" class="mb-2" />
                      <div class="flex-row mb-2" style="gap: 8px;">
                        <el-select v-model="editForm.templateType" size="small" style="flex: 1;">
                          <el-option label="账号风控" value="account_risk" />
                          <el-option label="失败释放" value="fail_release" />
                          <el-option label="点击图" value="click_image" />
                          <el-option label="成功图" value="success_image" />
                        </el-select>
                        <el-select v-model="editForm.recognitionEngine" size="small" style="width: 110px;">
                          <el-option label="OpenCV" value="opencv" />
                          <el-option label="OCR" value="ocr" />
                        </el-select>
                        <el-input v-model="editForm.priority" size="small" style="width: 70px;" />
                        <el-input v-model="editForm.threshold" size="small" style="width: 70px;" />
                      </div>
                      <el-input
                        v-model="editForm.expectedText"
                        size="small"
                        class="mb-2"
                        :placeholder="editForm.recognitionEngine === 'ocr' ? '期望文本，支持用 & 连接多个条件' : '识别成功返回内容'"
                      />
                      <div class="flex-row mb-2" style="gap: 6px;">
                        <el-input v-model="editForm.cropX" size="small" placeholder="X" />
                        <el-input v-model="editForm.cropY" size="small" placeholder="Y" />
                        <el-input v-model="editForm.cropWidth" size="small" placeholder="宽" />
                        <el-input v-model="editForm.cropHeight" size="small" placeholder="高" />
                      </div>
                      <el-upload
                        v-if="editForm.recognitionEngine === 'opencv'"
                        action="#"
                        :auto-upload="false"
                        :on-change="handleEditFileChange"
                        :file-list="editFileList"
                        :limit="1"
                        accept="image/*"
                      >
                        <el-button size="small" plain>替换图片</el-button>
                      </el-upload>
                    </div>

                    <div style="display: flex; gap: 8px; justify-content: space-between; align-items: center; flex-wrap: wrap;">
                      <div style="display: flex; gap: 6px;">
                        <el-button size="small" plain :disabled="!canMove(item, 'up')" @click="emit('move-template', item.id, 'up')">
                          向前
                        </el-button>
                        <el-button size="small" plain :disabled="!canMove(item, 'down')" @click="emit('move-template', item.id, 'down')">
                          向后
                        </el-button>
                      </div>
                      <div style="display: flex; gap: 8px; justify-content: flex-end;">
                        <template v-if="editingId !== item.id">
                          <el-button type="success" size="small" plain :icon="VideoPlay" :loading="testingId === item.id" @click="submitTest(item.id)">
                            测试
                          </el-button>
                          <el-button type="primary" size="small" plain :icon="Edit" @click="openEdit(item)">
                            编辑
                          </el-button>
                          <el-button type="danger" size="small" plain :icon="Delete" @click="removeTemplate(item.id)" />
                        </template>
                        <template v-else>
                          <el-button type="primary" size="small" @click="submitEdit(item.id)">保存</el-button>
                          <el-button size="small" @click="cancelEdit">取消</el-button>
                        </template>
                      </div>
                    </div>
                  </div>
                </div>
              </el-card>
            </el-col>
          </el-row>
        </div>
      </el-card>
    </el-col>
  </el-row>
</template>

<style scoped>
:deep(.el-card__body) {
  flex: 1;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}
</style>
