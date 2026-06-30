<script setup lang="ts">
import { ref, computed } from 'vue'
import MatchPreview from './MatchPreview.vue'
import type { DeviceInfo, TemplateRecord, TemplateTestResult } from '../types'
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
  (event: 'test-unsaved-template', payload: FormData): void
  (event: 'clear-test-result'): void
  (event: 'export-templates'): void
  (event: 'import-templates', payload: { file: File; replaceExisting: boolean }): void
}>()

const form = ref({
  label: '',
  templateType: 'success_image',
  recognitionEngine: 'opencv',
  priority: '100',
  expectedText: '',
  requiresClick: false,
  matchOncePerTask: false,
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
const showEditModal = ref(false)
const editFileList = ref<any[]>([])
const editForm = ref({
  label: '',
  templateType: 'success_image',
  recognitionEngine: 'opencv',
  priority: '100',
  expectedText: '',
  requiresClick: false,
  matchOncePerTask: false,
  threshold: '0.8',
  method: 'ccoeff_normed',
  grayscale: true,
  cropX: '',
  cropY: '',
  cropWidth: '',
  cropHeight: '',
})

const searchTemplate = ref('')
const activeTypeTab = ref<'account_risk' | 'fail_release' | 'click_image' | 'success_image' |
  'goods_confirm' | 'condition_mismatch' | 'need_coupon' | 'coupon_detail'>('account_risk')
const showCreateModal = ref(false)

function openCreateModal() {
  showCreateModal.value = true
}

function ocrItemsForTestResult(result: TemplateTestResult) {
  return result.ocr_result?.results ?? []
}

function extractOCRPoints(item: { box?: Array<[number, number]> | number[][]; bounding_box?: Array<[number, number]> | number[][] }) {
  const raw = item.box?.length ? item.box : item.bounding_box
  if (!raw?.length) return []
  return raw
    .map((point) => {
      if (!Array.isArray(point) || point.length < 2) return null
      return [Number(point[0]), Number(point[1])] as [number, number]
    })
    .filter((point): point is [number, number] => Boolean(point))
}

function aggregateOCRPoints(items: Array<{ box?: Array<[number, number]> | number[][]; bounding_box?: Array<[number, number]> | number[][] }>) {
  const points = items.flatMap((item) => extractOCRPoints(item))
  if (!points.length) return null
  const xs = points.map((point) => point[0])
  const ys = points.map((point) => point[1])
  const minX = Math.min(...xs)
  const maxX = Math.max(...xs)
  const minY = Math.min(...ys)
  const maxY = Math.max(...ys)
  return {
    topLeft: [minX, minY] as [number, number],
    width: maxX - minX,
    height: maxY - minY,
  }
}

function findOCRTokenWindow(items: Array<{ text: string }>, token: string) {
  for (let start = 0; start < items.length; start += 1) {
    let joined = ''
    let joinedWithSpace = ''
    for (let end = start; end < items.length; end += 1) {
      const text = (items[end]?.text || '').trim()
      joined += text
      joinedWithSpace = joinedWithSpace ? `${joinedWithSpace} ${text}` : text
      if (joined.includes(token) || joinedWithSpace.includes(token)) {
        return { start, end }
      }
    }
  }
  return null
}

function resolveOCRTokenMatch(result: TemplateTestResult, token: string) {
  const trimmed = token.trim()
  if (!trimmed) return { matched: true, negated: false, target: '', sourceText: '', box: null }
  const negated = trimmed.startsWith('!')
  const target = negated ? trimmed.slice(1).trim() : trimmed
  if (!target) return { matched: true, negated, target: '', sourceText: '', box: null }
  const items = ocrItemsForTestResult(result)
  for (let index = 0; index < items.length; index += 1) {
    const item = items[index]
    if ((item.text || '').includes(target)) {
      return {
        matched: !negated,
        negated,
        target,
        sourceText: item.text || '',
        box: negated ? null : aggregateOCRPoints([item]),
      }
    }
  }
  const window = findOCRTokenWindow(items, target)
  if (window) {
    const sourceItems = items.slice(window.start, window.end + 1)
    return {
      matched: !negated,
      negated,
      target,
      sourceText: sourceItems.map((item) => item.text || '').join(' / '),
      box: negated ? null : aggregateOCRPoints(sourceItems),
    }
  }
  return {
    matched: negated,
    negated,
    target,
    sourceText: '',
    box: null,
  }
}

function ocrTokenMatchList(result: TemplateTestResult) {
  const tokens = result.ocr_result?.expected_tokens?.map((item) => item.trim()).filter(Boolean) ?? []
  return tokens.map((token) => {
    const resolved = resolveOCRTokenMatch(result, token)
    return {
      token,
      matched: resolved.matched,
      negated: resolved.negated,
      sourceText: resolved.sourceText,
      box: resolved.box,
    }
  })
}

function shouldShowOcrTokenSummary(result: TemplateTestResult) {
  return result.recognition_engine === 'ocr' && ocrTokenMatchList(result).length > 0
}

function testPreviewLabel(result: TemplateTestResult) {
  if (result.recognition_engine === 'ocr') {
    const tokenCount = ocrTokenMatchList(result).length
    if (tokenCount > 1) return '首个条件点击框'
    return 'OCR 命中框'
  }
  return result.template.label
}

function ocrPreviewBoxes(result: TemplateTestResult) {
  const matches = ocrTokenMatchList(result)
    .filter((item) => item.matched && !item.negated && item.box)
    .map((item, index) => ({
      topLeft: item.box!.topLeft,
      width: item.box!.width,
      height: item.box!.height,
      label: item.token,
      tone: index === 0 ? 'primary' as const : 'secondary' as const,
    }))
  return matches
}

function submitTestUnsaved() {
  if (form.value.recognitionEngine === 'opencv' && fileList.value.length === 0) {
    ElMessage.warning('OpenCV 模板必须上传模板图片才能测试')
    return
  }
  if (form.value.recognitionEngine === 'ocr' && !form.value.expectedText.trim()) {
    ElMessage.warning('OCR 模板必须填写期望文本')
    return
  }
  
  const payload = new FormData()
  if (testSourceMode.value === 'device') {
    if (!testDeviceId.value) {
      ElMessage.warning('请选择一个测试设备')
      return
    }
    payload.set('device_id', testDeviceId.value)
  } else {
    if (testFileList.value.length === 0) {
      ElMessage.warning('请上传一张测试大图')
      return
    }
    payload.set('source_image', testFileList.value[0].raw)
  }

  payload.set('label', form.value.label.trim() || '测试模板')
  payload.set('template_type', form.value.templateType)
  payload.set('recognition_engine', form.value.recognitionEngine)
  payload.set('priority', form.value.priority)
  payload.set('expected_text', form.value.expectedText)
  payload.set('requires_click', String(form.value.requiresClick && (form.value.templateType === 'fail_release' || form.value.templateType === 'need_coupon')))
  payload.set('match_once_per_task', String(form.value.matchOncePerTask))
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

  emit('test-unsaved-template', payload)
}

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
    goods_confirm: base.filter((item) => item.template_type === 'goods_confirm'),
    condition_mismatch: base.filter((item) => item.template_type === 'condition_mismatch'),
    need_coupon: base.filter((item) => item.template_type === 'need_coupon'),
    coupon_detail: base.filter((item) => item.template_type === 'coupon_detail'),
  }
})

const activeTemplates = computed(() => {
  return [...templatesByType.value[activeTypeTab.value]].sort((a, b) => a.priority - b.priority)
})

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
  payload.set('requires_click', String(form.value.requiresClick && (form.value.templateType === 'fail_release' || form.value.templateType === 'need_coupon')))
  payload.set('match_once_per_task', String(form.value.matchOncePerTask))
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
  form.value.requiresClick = false
  form.value.matchOncePerTask = false
  showCreateModal.value = false
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
    requiresClick: Boolean(item.requires_click),
    matchOncePerTask: Boolean(item.match_once_per_task),
    threshold: String(item.threshold),
    method: item.method,
    grayscale: item.grayscale,
    cropX: item.crop?.x != null ? String(item.crop.x) : '',
    cropY: item.crop?.y != null ? String(item.crop.y) : '',
    cropWidth: item.crop?.width != null ? String(item.crop.width) : '',
    cropHeight: item.crop?.height != null ? String(item.crop.height) : '',
  }
  showEditModal.value = true
}

function submitTestEditUnsaved() {
  if (editForm.value.recognitionEngine === 'opencv' && editFileList.value.length === 0) {
    const current = props.templates.find((item) => item.id === editingId.value)
    if (!current?.image_url) {
      ElMessage.warning('OpenCV 模板必须保留或上传模板图片才能测试')
      return
    }
  }
  if (editForm.value.recognitionEngine === 'ocr' && !editForm.value.expectedText.trim()) {
    ElMessage.warning('OCR 模板必须填写期望文本')
    return
  }
  
  const payload = new FormData()
  if (testSourceMode.value === 'device') {
    if (!testDeviceId.value) {
      ElMessage.warning('请选择一个测试设备')
      return
    }
    payload.set('device_id', testDeviceId.value)
  } else {
    if (testFileList.value.length === 0) {
      ElMessage.warning('请上传一张测试大图')
      return
    }
    payload.set('source_image', testFileList.value[0].raw)
  }

  payload.set('label', editForm.value.label.trim() || '测试模板')
  payload.set('template_type', editForm.value.templateType)
  payload.set('recognition_engine', editForm.value.recognitionEngine)
  payload.set('priority', editForm.value.priority)
  payload.set('expected_text', editForm.value.expectedText)
  payload.set('threshold', editForm.value.threshold)
  payload.set('method', editForm.value.method)
  payload.set('grayscale', String(editForm.value.grayscale))
  payload.set('requires_click', String(editForm.value.requiresClick))
  payload.set('match_once_per_task', String(editForm.value.matchOncePerTask))
  
  if (editFileList.value.length > 0) {
    payload.set('image', editFileList.value[0].raw)
  } else {
    // We can't send existing image easily through this test unsaved API without backend changes,
    // so we will just call the standard test API if no new image is provided for an existing template
    if (editForm.value.recognitionEngine === 'opencv' && editingId.value) {
        // Fallback to standard test if no new image provided
        const testPayload = new FormData()
        if (testSourceMode.value === 'device') {
          testPayload.set('device_id', testDeviceId.value)
        } else {
          testPayload.set('source_image', testFileList.value[0].raw)
        }
        emit('test-template', editingId.value, testPayload)
        return
    }
  }
  
  if (editForm.value.cropX && editForm.value.cropY && editForm.value.cropWidth && editForm.value.cropHeight) {
    payload.set('crop_x', editForm.value.cropX)
    payload.set('crop_y', editForm.value.cropY)
    payload.set('crop_width', editForm.value.cropWidth)
    payload.set('crop_height', editForm.value.cropHeight)
  }

  emit('test-unsaved-template', payload)
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
  payload.set('requires_click', String(editForm.value.requiresClick && (editForm.value.templateType === 'fail_release' || editForm.value.templateType === 'need_coupon')))
  payload.set('match_once_per_task', String(editForm.value.matchOncePerTask))
  payload.set('threshold', editForm.value.threshold)
  payload.set('method', editForm.value.method)
  payload.set('grayscale', String(editForm.value.grayscale))
  payload.set('requires_click', String(editForm.value.requiresClick))
  payload.set('match_once_per_task', String(editForm.value.matchOncePerTask))
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

function cancelEdit() {
  editingId.value = ''
  editFileList.value = []
  showEditModal.value = false
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

function recognitionEngineLabel(engine: TemplateRecord['recognition_engine']) {
  return engine === 'ocr' ? 'OCR 文本' : 'OpenCV 匹配'
}

function canMove(item: TemplateRecord, direction: 'up' | 'down') {
  const list = props.templates.filter((template) => template.template_type === item.template_type).sort((a, b) => a.priority - b.priority)
  const index = list.findIndex((template) => template.id === item.id)
  if (index === -1) return false
  return direction === 'up' ? index > 0 : index < list.length - 1
}
</script>

<template>
  <div class="dashboard-layout full-width-layout">
    <!-- Main Content -->
    <main class="main-content">
      
      <!-- Top Action Bar -->
      <header class="content-header">
        <div class="header-left">
          <h1 class="page-title">模板库管理</h1>
          <el-input v-model="searchTemplate" placeholder="搜索模板名称..." clearable class="search-input" />
          
          <div class="global-test-bar">
            <span class="text-sm text-gray font-medium">测试环境:</span>
            <el-select v-model="testSourceMode" size="small" style="width: 100px;">
              <el-option label="在线设备" value="device" />
              <el-option label="上传大图" value="upload" />
            </el-select>
            <div v-if="testSourceMode === 'device'">
              <el-select v-model="testDeviceId" size="small" placeholder="请选择设备" style="width: 140px;">
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
                class="inline-block"
              >
                <button type="button" class="btn-outline btn-small">选择图片</button>
              </el-upload>
            </div>
          </div>
        </div>
        
        <div class="header-right">
          <div class="import-export-group">
            <button type="button" class="btn-primary" @click="openCreateModal">新建模板</button>
            <div class="divider"></div>
            <button type="button" class="btn-text-action" @click="emit('export-templates')">导出包</button>
            <el-upload
              action="#"
              :auto-upload="false"
              :show-file-list="false"
              :on-change="handleImportFileChange"
              :limit="1"
              accept=".zip"
              class="inline-block"
            >
              <button type="button" class="btn-text-action">选择包</button>
            </el-upload>
            <label class="checkbox-label">
              <input type="checkbox" v-model="importReplaceExisting" />
              <span>覆盖已有</span>
            </label>
            <button type="button" class="btn-outline btn-small" :class="{ 'is-loading': importingTemplatePack }" @click="submitImportTemplates">
              导入
            </button>
          </div>
          <div v-if="importFileList.length" class="selected-file-text">
            待导入文件: {{ importFileList[0].name }}
          </div>
        </div>
      </header>

      <!-- Tabs -->
      <div class="modern-tabs-container">
        <div class="modern-tabs">
          <div class="tab-item" :class="{ active: activeTypeTab === 'account_risk' }" @click="activeTypeTab = 'account_risk'">
            账号风控 <span class="badge">{{ templatesByType.account_risk.length }}</span>
          </div>
          <div class="tab-item" :class="{ active: activeTypeTab === 'fail_release' }" @click="activeTypeTab = 'fail_release'">
            失败释放 <span class="badge">{{ templatesByType.fail_release.length }}</span>
          </div>
          <div class="tab-item" :class="{ active: activeTypeTab === 'click_image' }" @click="activeTypeTab = 'click_image'">
            点击图 <span class="badge">{{ templatesByType.click_image.length }}</span>
          </div>
          <div class="tab-item" :class="{ active: activeTypeTab === 'success_image' }" @click="activeTypeTab = 'success_image'">
            成功图 <span class="badge">{{ templatesByType.success_image.length }}</span>
          </div>
          <div class="tab-item" :class="{ active: activeTypeTab === 'goods_confirm' }" @click="activeTypeTab = 'goods_confirm'">
            商品确认 <span class="badge">{{ templatesByType.goods_confirm.length }}</span>
          </div>
          <div class="tab-item" :class="{ active: activeTypeTab === 'condition_mismatch' }" @click="activeTypeTab = 'condition_mismatch'">
            条件不满足 <span class="badge">{{ templatesByType.condition_mismatch.length }}</span>
          </div>
          <div class="tab-item" :class="{ active: activeTypeTab === 'need_coupon' }" @click="activeTypeTab = 'need_coupon'">
            需要领券 <span class="badge">{{ templatesByType.need_coupon.length }}</span>
          </div>
          <div class="tab-item" :class="{ active: activeTypeTab === 'coupon_detail' }" @click="activeTypeTab = 'coupon_detail'">
            优惠券弹窗 <span class="badge">{{ templatesByType.coupon_detail.length }}</span>
          </div>
        </div>
      </div>

      <!-- Test Result Modal / Floating Panel (Global) -->
      <el-dialog :model-value="!!testResult" :title="'测试结果 - ' + (testResult?.template.label || '')" width="900px" top="5vh" @close="emit('clear-test-result')">
        <div v-if="testResult" class="global-test-result-body">
          <div class="result-left-panel">
            <div class="result-stats mb-4">
              <div class="stat-item">
                <span class="stat-label">识别引擎</span>
                <span class="stat-value">{{ recognitionEngineLabel(testResult.recognition_engine) }}</span>
              </div>
              <div class="stat-item">
                <span class="stat-label">是否命中</span>
                <span class="stat-value" :class="testResult.match.found ? 'text-success' : 'text-danger'">
                  {{ testResult.match.found ? '命中成功' : '未命中' }}
                </span>
              </div>
              <div class="stat-item">
                <span class="stat-label">置信度</span>
                <span class="stat-value">{{ testResult.match.confidence }} / {{ testResult.match.threshold }}</span>
              </div>
            </div>
            <div v-if="testResult.match.matched_text || testResult.match.full_text" class="result-text-box">
              <div v-if="testResult.match.matched_text" class="mb-2">
                <div class="text-label">命中文本：</div>
                <div class="text-highlight">{{ testResult.match.matched_text }}</div>
              </div>
              <div v-if="testResult.match.full_text">
                <div class="text-label">识别全文：</div>
                <div class="text-full">{{ testResult.match.full_text }}</div>
              </div>
            </div>
            <div v-if="shouldShowOcrTokenSummary(testResult)" class="result-text-box token-summary-box">
              <div class="text-label">条件命中明细：</div>
              <div class="token-chip-list">
                <span
                  v-for="item in ocrTokenMatchList(testResult)"
                  :key="item.token"
                  class="token-chip"
                  :class="item.matched ? 'token-chip-success' : 'token-chip-danger'"
                >
                  {{ item.token }} / {{ item.matched ? (item.negated ? '未出现' : '已命中') : (item.negated ? '已出现' : '未命中') }}
                </span>
              </div>
              <div class="token-source-list">
                <div v-for="item in ocrTokenMatchList(testResult)" :key="`${item.token}-source`" class="token-source-item">
                  <span class="token-source-name">{{ item.token }}</span>
                  <span class="token-source-value">
                    {{ item.sourceText || (item.negated ? '未在 OCR 结果中出现' : '未在 OCR 结果中找到对应片段') }}
                  </span>
                </div>
              </div>
              <div class="token-rule-tip">
                多条件判定按全部条件是否命中；预览图会绘制所有正向条件的命中框，绿色为首个条件点击框，橙色为其他条件。
              </div>
            </div>
          </div>
          <div class="result-right-panel">
            <div class="result-preview">
              <MatchPreview
                :image-url="testResult.capture_url"
                :boxes="testResult.recognition_engine === 'ocr' ? ocrPreviewBoxes(testResult) : undefined"
                :top-left="testResult.match.top_left"
                :width="testResult.match.width"
                :height="testResult.match.height"
                :label="testPreviewLabel(testResult)"
              />
            </div>
          </div>
        </div>
      </el-dialog>

      <!-- Template Grid -->
      <div class="template-scroll-area">
        <div v-if="activeTemplates.length === 0" class="empty-state">
          <div class="empty-text">当前分类下暂无模板数据</div>
        </div>
        
        <div v-else class="template-grid">
          <div v-for="item in activeTemplates" :key="item.id" class="template-card" :class="{ 'is-match-once': item.match_once_per_task }">
            
            <!-- Read Mode -->
            <template v-if="true">
              <div class="card-image-wrapper">
                <img v-if="item.image_url" :src="item.image_url" class="card-image" />
                <div v-else class="ocr-banner">
                  <div class="ocr-text">OCR</div>
                </div>
                <div class="card-badges">
                  <span class="badge-engine" :class="item.recognition_engine">{{ recognitionEngineLabel(item.recognition_engine) }}</span>
                </div>
                <!-- Highlight Ribbon for match_once_per_task -->
                <div v-if="item.match_once_per_task" class="ribbon-match-once">
                  <span>一次性</span>
                </div>
              </div>
              
              <div class="card-content">
                <h3 class="card-title-text" :title="item.label">{{ item.label }}</h3>
                <div class="card-meta">
                  <span class="meta-item">优先级: <strong>{{ item.priority }}</strong></span>
                  <span class="meta-item">阈值: <strong>{{ item.threshold }}</strong></span>
                </div>
                <div v-if="item.template_type === 'fail_release' && item.requires_click" class="card-desc text-warning">
                  前置条件：仅点击后参与匹配
                </div>
         
                <div class="card-desc" v-if="item.expected_text" :title="item.expected_text">
                  返回内容: {{ item.expected_text }}
                </div>
                <div class="card-desc" v-else>
                  无返回内容
                </div>
                
                <div class="card-actions">
                  <div class="action-group">
                    <button type="button" class="btn-text" :disabled="!canMove(item, 'up')" @click="emit('move-template', item.id, 'up')">前移</button>
                    <button type="button" class="btn-text" :disabled="!canMove(item, 'down')" @click="emit('move-template', item.id, 'down')">后移</button>
                  </div>
                  <div class="action-group">
                    <button type="button" class="btn-test" :class="{ 'is-loading': testingId === item.id }" @click="submitTest(item.id)">测试</button>
                    <button type="button" class="btn-edit" @click="openEdit(item)">编辑</button>
                    <button type="button" class="btn-delete" @click="removeTemplate(item.id)">删除</button>
                  </div>
                </div>
              </div>
            </template>
            
          </div>
        </div>
      </div>
    </main>

    <!-- Create Template Dialog -->
    <el-dialog v-model="showCreateModal" title="新建模板" width="700px" destroy-on-close>
      <div class="create-modal-body">
        <el-form label-position="top" size="default" class="modern-form">
          <el-form-item label="模板名称" required>
            <el-input v-model="form.label" placeholder="例如：无优惠券" />
          </el-form-item>
          
          <div class="form-row">
            <el-form-item label="模板类型" class="flex-1">
              <el-select v-model="form.templateType" class="w-full">
                <el-option label="账号风控" value="account_risk" />
                <el-option label="失败释放" value="fail_release" />
                <el-option label="点击图" value="click_image" />
                <el-option label="成功图" value="success_image" />
tt        <el-option label="商品确认" value="goods_confirm" />
tt        <el-option label="条件不满足" value="condition_mismatch" />
tt        <el-option label="需要领券" value="need_coupon" />
tt        <el-option label="优惠券弹窗" value="coupon_detail" />
              </el-select>
            </el-form-item>
            <el-form-item label="识别引擎" class="flex-1">
              <el-select v-model="form.recognitionEngine" class="w-full">
                <el-option label="OpenCV 匹配" value="opencv" />
                <el-option label="OCR 文本" value="ocr" />
              </el-select>
            </el-form-item>
          </div>

          <div class="form-row">
            <el-form-item label="识别顺序" class="flex-1">
              <el-input v-model="form.priority" placeholder="越小越优先" />
            </el-form-item>
            <el-form-item :label="form.recognitionEngine === 'ocr' ? 'OCR 阈值' : '匹配阈值'" class="flex-1">
              <el-input v-model="form.threshold" placeholder="默认 0.8" />
            </el-form-item>
          </div>

          <el-form-item :label="form.recognitionEngine === 'ocr' ? '期望文本' : '成功返回内容'">
            <el-input
              v-model="form.expectedText"
              :placeholder="form.recognitionEngine === 'ocr' ? '用 & 连接多条件' : '返回的文本内容'"
            />
            <div v-if="form.recognitionEngine === 'ocr'" class="text-xs text-gray mt-1">支持 & 多条件；`!文本` 表示该文本必须不存在，例如 `店铺优惠&!领取`。</div>
          </el-form-item>

          <el-form-item v-if="form.templateType === 'fail_release' || form.templateType === 'need_coupon'" label="失败释放前置条件">
            <label class="checkbox-label">
              <input v-model="form.requiresClick" type="checkbox" />
              <span>仅点击后参与匹配</span>
            </label>
          </el-form-item>

          <el-form-item label="匹配轮次控制">
            <label class="checkbox-label">
              <input v-model="form.matchOncePerTask" type="checkbox" />
              <span>本任务仅匹配一次</span>
            </label>
          </el-form-item>

          <div v-if="form.recognitionEngine === 'opencv'" class="form-row">
            <el-form-item label="匹配算法" class="flex-1">
              <el-select v-model="form.method" class="w-full">
                <el-option label="ccoeff_normed" value="ccoeff_normed" />
                <el-option label="ccorr_normed" value="ccorr_normed" />
                <el-option label="sqdiff_normed" value="sqdiff_normed" />
              </el-select>
            </el-form-item>
            <el-form-item label="灰度识别" class="flex-1">
              <el-switch v-model="form.grayscale" active-text="开启" inactive-text="关闭" />
            </el-form-item>
          </div>

          <el-form-item :label="form.recognitionEngine === 'ocr' ? '识别区域 (X, Y, 宽, 高)' : '裁剪区域 (X, Y, 宽, 高)'">
            <div class="grid-4">
              <el-input v-model="form.cropX" placeholder="X" />
              <el-input v-model="form.cropY" placeholder="Y" />
              <el-input v-model="form.cropWidth" placeholder="宽" />
              <el-input v-model="form.cropHeight" placeholder="高" />
            </div>
          </el-form-item>

          <el-form-item v-if="form.recognitionEngine === 'opencv'" label="模板截图" required>
            <el-upload
              class="w-full custom-dropzone"
              drag
              action="#"
              :auto-upload="false"
              :on-change="handleFileChange"
              :file-list="fileList"
              :limit="1"
              accept="image/*"
            >
              <div class="dropzone-content">
                <div class="dropzone-text">点击或拖拽图片到此处</div>
              </div>
            </el-upload>
          </el-form-item>
        </el-form>
      </div>
      
      <template #footer>
        <div class="create-modal-footer">
          <div class="test-hint text-sm text-gray flex items-center">
            提示：测试将使用页面顶部的“测试环境”配置
          </div>
          <div class="action-buttons">
            <button type="button" class="btn-test" :class="{ 'is-loading': testingId === 'unsaved' }" @click="submitTestUnsaved">
              <span v-if="testingId === 'unsaved'">测试中...</span>
              <span v-else>测试模板</span>
            </button>
            <button type="button" class="btn-primary" :class="{ 'is-loading': saving }" @click="submitCreate">
              <span v-if="saving">保存中...</span>
              <span v-else>保存模板</span>
            </button>
          </div>
        </div>
      </template>
    </el-dialog>

    <!-- Edit Template Dialog -->
    <el-dialog v-model="showEditModal" title="编辑模板" width="700px" destroy-on-close @close="cancelEdit">
      <div class="create-modal-body">
        <el-form label-position="top" size="default" class="modern-form">
          <el-form-item label="模板名称" required>
            <el-input v-model="editForm.label" placeholder="例如：无优惠券" />
          </el-form-item>
          
          <div class="form-row">
            <el-form-item label="模板类型" class="flex-1">
              <el-select v-model="editForm.templateType" class="w-full">
                <el-option label="账号风控" value="account_risk" />
                <el-option label="失败释放" value="fail_release" />
                <el-option label="点击图" value="click_image" />
                <el-option label="成功图" value="success_image" />
tt        <el-option label="商品确认" value="goods_confirm" />
tt        <el-option label="条件不满足" value="condition_mismatch" />
tt        <el-option label="需要领券" value="need_coupon" />
tt        <el-option label="优惠券弹窗" value="coupon_detail" />
              </el-select>
            </el-form-item>
            <el-form-item label="识别引擎" class="flex-1">
              <el-select v-model="editForm.recognitionEngine" class="w-full">
                <el-option label="OpenCV 匹配" value="opencv" />
                <el-option label="OCR 文本" value="ocr" />
              </el-select>
            </el-form-item>
          </div>

          <div class="form-row">
            <el-form-item label="识别顺序" class="flex-1">
              <el-input v-model="editForm.priority" placeholder="越小越优先" />
            </el-form-item>
            <el-form-item :label="editForm.recognitionEngine === 'ocr' ? 'OCR 阈值' : '匹配阈值'" class="flex-1">
              <el-input v-model="editForm.threshold" placeholder="默认 0.8" />
            </el-form-item>
          </div>

          <el-form-item :label="editForm.recognitionEngine === 'ocr' ? '期望文本' : '成功返回内容'">
            <el-input
              v-model="editForm.expectedText"
              :placeholder="editForm.recognitionEngine === 'ocr' ? '用 & 连接多条件' : '返回的文本内容'"
            />
            <div v-if="editForm.recognitionEngine === 'ocr'" class="text-xs text-gray mt-1">支持 & 多条件；`!文本` 表示该文本必须不存在，例如 `店铺优惠&!领取`。</div>
          </el-form-item>

          <el-form-item v-if="editForm.templateType === 'fail_release' || editForm.templateType === 'need_coupon'" label="失败释放前置条件">
            <label class="checkbox-label">
              <input v-model="editForm.requiresClick" type="checkbox" />
              <span>仅点击后参与匹配</span>
            </label>
          </el-form-item>

          <el-form-item label="匹配轮次控制">
            <label class="checkbox-label">
              <input v-model="editForm.matchOncePerTask" type="checkbox" />
              <span>本任务仅匹配一次</span>
            </label>
          </el-form-item>

          <div v-if="editForm.recognitionEngine === 'opencv'" class="form-row">
            <el-form-item label="匹配算法" class="flex-1">
              <el-select v-model="editForm.method" class="w-full">
                <el-option label="ccoeff_normed" value="ccoeff_normed" />
                <el-option label="ccorr_normed" value="ccorr_normed" />
                <el-option label="sqdiff_normed" value="sqdiff_normed" />
              </el-select>
            </el-form-item>
            <el-form-item label="灰度识别" class="flex-1">
              <el-switch v-model="editForm.grayscale" active-text="开启" inactive-text="关闭" />
            </el-form-item>
          </div>

          <el-form-item :label="editForm.recognitionEngine === 'ocr' ? '识别区域 (X, Y, 宽, 高)' : '裁剪区域 (X, Y, 宽, 高)'">
            <div class="grid-4">
              <el-input v-model="editForm.cropX" placeholder="X" />
              <el-input v-model="editForm.cropY" placeholder="Y" />
              <el-input v-model="editForm.cropWidth" placeholder="宽" />
              <el-input v-model="editForm.cropHeight" placeholder="高" />
            </div>
          </el-form-item>

          <el-form-item v-if="editForm.recognitionEngine === 'opencv'" label="模板截图 (留空则保留原图)">
            <el-upload
              class="w-full custom-dropzone"
              drag
              action="#"
              :auto-upload="false"
              :on-change="handleEditFileChange"
              :file-list="editFileList"
              :limit="1"
              accept="image/*"
            >
              <div class="dropzone-content">
                <div class="dropzone-text">点击或拖拽图片到此处以替换</div>
              </div>
            </el-upload>
          </el-form-item>
        </el-form>
      </div>
      
      <template #footer>
        <div class="create-modal-footer">
          <div class="test-hint text-sm text-gray flex items-center">
            提示：测试将使用页面顶部的“测试环境”配置
          </div>
          <div class="action-buttons">
            <button type="button" class="btn-test" :class="{ 'is-loading': testingId === editingId || testingId === 'unsaved' }" @click="submitTestEditUnsaved">
              <span v-if="testingId === editingId || testingId === 'unsaved'">测试中...</span>
              <span v-else>测试模板</span>
            </button>
            <button type="button" class="btn-primary" :class="{ 'is-loading': saving }" @click="submitEdit(editingId)">
              <span v-if="saving">保存中...</span>
              <span v-else>保存修改</span>
            </button>
          </div>
        </div>
      </template>
    </el-dialog>

  </div>
</template>

<style scoped>
/* Base Layout */
.dashboard-layout {
  display: flex;
  height: 100%;
  background: #f1f5f9;
  gap: 24px;
  padding: 24px;
  box-sizing: border-box;
  font-family: system-ui, -apple-system, sans-serif;
}

.full-width-layout {
  padding: 0; /* Let main-content take full available padding if needed, or adjust padding */
}

/* Main Content */
.main-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
  background: #ffffff;
  border-radius: 16px;
  padding: 24px;
  box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.05), 0 2px 4px -2px rgba(0, 0, 0, 0.05);
  border: 1px solid #e2e8f0;
}

.global-test-bar {
  display: flex;
  align-items: center;
  gap: 12px;
  background: #f8fafc;
  padding: 6px 12px;
  border-radius: 8px;
  border: 1px solid #e2e8f0;
}

.create-modal-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding-top: 16px;
  border-top: 1px solid #e2e8f0;
}

.create-modal-body {
  max-height: 60vh;
  overflow-y: auto;
  padding-right: 8px;
}

.flex { display: flex; }
.items-center { align-items: center; }
.text-sm { font-size: 14px; }
.font-medium { font-weight: 500; }

/* Header Elements */
.content-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 24px;
}

.header-left {
  display: flex;
  align-items: center;
  gap: 24px;
}

.page-title {
  margin: 0;
  font-size: 24px;
  font-weight: 800;
  color: #0f172a;
  letter-spacing: -0.5px;
}

.search-input {
  width: 280px;
}

.header-right {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 8px;
}

.import-export-group {
  display: flex;
  align-items: center;
  gap: 16px;
  background: #f8fafc;
  padding: 6px 12px;
  border-radius: 12px;
  border: 1px solid #e2e8f0;
}

.divider {
  width: 1px;
  height: 20px;
  background: #cbd5e1;
}

.checkbox-label {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 14px;
  color: #475569;
  cursor: pointer;
  user-select: none;
  font-weight: 500;
}

.selected-file-text {
  font-size: 12px;
  color: #64748b;
  font-weight: 500;
}

/* Utilities */
.w-full { width: 100%; }
.mb-2 { margin-bottom: 8px; }
.mb-4 { margin-bottom: 16px; }
.mt-4 { margin-top: 16px; }
.inline-block { display: inline-block; }
.flex-1 { flex: 1; }
.grid-2 { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.grid-4 { display: grid; grid-template-columns: repeat(4, 1fr); gap: 8px; }
.form-row { display: flex; gap: 16px; margin-bottom: 16px; }
.form-row .el-form-item { margin-bottom: 0; }

/* Radio Group Modern */
.radio-group-modern {
  display: flex;
  background: #f1f5f9;
  border-radius: 8px;
  padding: 4px;
  gap: 4px;
}

.radio-item {
  flex: 1;
  text-align: center;
  padding: 8px 0;
  font-size: 14px;
  font-weight: 600;
  color: #64748b;
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.2s;
}

.radio-item.active {
  background: #ffffff;
  color: #0f172a;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
}

/* Tabs */
.modern-tabs-container {
  margin-bottom: 24px;
  border-bottom: 1px solid #e2e8f0;
}

.modern-tabs {
  display: flex;
  gap: 32px;
}

.tab-item {
  padding: 12px 0;
  font-size: 15px;
  font-weight: 600;
  color: #64748b;
  cursor: pointer;
  position: relative;
  transition: color 0.2s;
  display: flex;
  align-items: center;
  gap: 8px;
}

.tab-item:hover {
  color: #0f172a;
}

.tab-item.active {
  color: #2563eb;
}

.tab-item.active::after {
  content: '';
  position: absolute;
  bottom: -1px;
  left: 0;
  right: 0;
  height: 3px;
  background: #2563eb;
  border-radius: 3px 3px 0 0;
}

.tab-item .badge {
  background: #f1f5f9;
  color: #475569;
  padding: 2px 8px;
  border-radius: 12px;
  font-size: 12px;
  font-weight: 600;
}

.tab-item.active .badge {
  background: #eff6ff;
  color: #2563eb;
}

/* Forms */
.modern-form :deep(.el-form-item__label) {
  font-weight: 600;
  color: #334155;
  padding-bottom: 6px;
}

.custom-dropzone :deep(.el-upload-dragger) {
  background: #f8fafc;
  border: 2px dashed #cbd5e1;
  border-radius: 12px;
  transition: all 0.2s;
  padding: 32px 20px;
}

.custom-dropzone :deep(.el-upload-dragger:hover) {
  border-color: #3b82f6;
  background: #eff6ff;
}

.dropzone-content {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
}

.dropzone-text {
  font-size: 14px;
  color: #64748b;
  font-weight: 600;
}

/* Template Grid */
.template-scroll-area {
  flex: 1;
  overflow-y: auto;
  padding-right: 8px;
}

.empty-state {
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
}

.empty-text {
  font-size: 16px;
  color: #94a3b8;
  font-weight: 600;
}

.template-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 20px;
  padding-bottom: 24px;
}

.template-card {
  background: #ffffff;
  border-radius: 12px;
  border: 1px solid #e2e8f0;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  transition: transform 0.2s, box-shadow 0.2s;
  height: 280px;
}

.template-card:hover {
  transform: translateY(-2px);
  box-shadow: 0 10px 15px -3px rgba(0, 0, 0, 0.1), 0 4px 6px -2px rgba(0, 0, 0, 0.05);
  border-color: #cbd5e1;
}

/* Match Once Per Task Styling */
.template-card.is-match-once {
  border: 2px solid #8b5cf6;
  box-shadow: 0 4px 6px -1px rgba(139, 92, 246, 0.15);
}

.template-card.is-match-once:hover {
  box-shadow: 0 10px 15px -3px rgba(139, 92, 246, 0.25), 0 4px 6px -2px rgba(139, 92, 246, 0.1);
  border-color: #7c3aed;
}

.ribbon-match-once {
  position: absolute;
  top: 12px;
  left: -28px;
  background: #8b5cf6;
  color: white;
  padding: 4px 32px;
  font-size: 12px;
  font-weight: 700;
  transform: rotate(-45deg);
  box-shadow: 0 2px 4px rgba(0,0,0,0.1);
  z-index: 10;
  pointer-events: none;
  letter-spacing: 1px;
}

.card-image-wrapper {
  height: 130px;
  background: #f8fafc;
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  border-bottom: 1px solid #f1f5f9;
  overflow: hidden;
}

.card-image {
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
}

.ocr-banner {
  background: linear-gradient(135deg, #e0e7ff 0%, #c7d2fe 100%);
  width: 100%;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
}

.ocr-text {
  font-size: 24px;
  font-weight: 800;
  color: #4f46e5;
  letter-spacing: 2px;
}

.card-badges {
  position: absolute;
  top: 10px;
  right: 10px;
}

.badge-engine {
  padding: 4px 10px;
  border-radius: 6px;
  font-size: 12px;
  font-weight: 700;
  background: rgba(255, 255, 255, 0.9);
  backdrop-filter: blur(4px);
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.05);
}

.badge-engine.ocr { color: #4f46e5; }
.badge-engine.opencv { color: #059669; }

.card-content {
  padding: 16px;
  flex: 1;
  display: flex;
  flex-direction: column;
}

.card-title-text {
  margin: 0 0 8px 0;
  font-size: 16px;
  font-weight: 700;
  color: #0f172a;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.card-meta {
  display: flex;
  gap: 12px;
  font-size: 13px;
  color: #64748b;
  margin-bottom: 8px;
}

.card-meta strong {
  color: #0f172a;
  font-weight: 700;
}

.card-desc {
  font-size: 13px;
  color: #475569;
  background: #f8fafc;
  padding: 8px 12px;
  border-radius: 6px;
  margin-bottom: 16px;
  flex: 1;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  border: 1px solid #f1f5f9;
}

.card-actions {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-top: auto;
}

.action-group {
  display: flex;
  gap: 8px;
}

/* Edit Mode */
.card-edit-mode {
  padding: 16px;
  display: flex;
  flex-direction: column;
  height: 100%;
  background: #f8fafc;
}

.edit-header {
  font-weight: 700;
  font-size: 15px;
  margin-bottom: 12px;
  color: #0f172a;
}

.edit-body {
  flex: 1;
  overflow-y: auto;
}

.edit-footer {
  display: flex;
  gap: 8px;
  margin-top: 12px;
}

/* Buttons */
.btn-primary {
  background: #2563eb;
  color: white;
  border: none;
  padding: 8px 16px;
  border-radius: 8px;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.2s;
}

.btn-primary:hover {
  background: #1d4ed8;
}

.btn-large {
  padding: 12px 16px;
  font-size: 16px;
}

.btn-small {
  padding: 6px 12px;
  font-size: 13px;
}

.btn-outline {
  background: #ffffff;
  color: #334155;
  border: 1px solid #cbd5e1;
  padding: 8px 16px;
  border-radius: 8px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.2s;
}

.btn-outline:hover {
  background: #f8fafc;
  border-color: #94a3b8;
  color: #0f172a;
}

.btn-text-action {
  background: transparent;
  color: #475569;
  border: none;
  padding: 8px 12px;
  font-weight: 600;
  cursor: pointer;
  border-radius: 8px;
  transition: all 0.2s;
}

.btn-text-action:hover {
  background: #f1f5f9;
  color: #0f172a;
}

.btn-text {
  background: transparent;
  color: #64748b;
  border: none;
  padding: 4px 8px;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  border-radius: 6px;
  transition: all 0.2s;
}

.btn-text:hover {
  background: #f1f5f9;
  color: #0f172a;
}

.btn-text:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.btn-test {
  background: #ecfdf5;
  color: #059669;
  border: 1px solid #a7f3d0;
  padding: 6px 12px;
  border-radius: 6px;
  font-size: 13px;
  font-weight: 700;
  cursor: pointer;
  transition: all 0.2s;
}

.btn-test:hover {
  background: #d1fae5;
  border-color: #6ee7b7;
}

.btn-edit {
  background: #eff6ff;
  color: #2563eb;
  border: 1px solid #bfdbfe;
  padding: 6px 12px;
  border-radius: 6px;
  font-size: 13px;
  font-weight: 700;
  cursor: pointer;
  transition: all 0.2s;
}

.btn-edit:hover {
  background: #dbeafe;
  border-color: #93c5fd;
}

.btn-delete {
  background: #fef2f2;
  color: #dc2626;
  border: 1px solid #fecaca;
  padding: 6px 12px;
  border-radius: 6px;
  font-size: 13px;
  font-weight: 700;
  cursor: pointer;
  transition: all 0.2s;
}

.btn-delete:hover {
  background: #fee2e2;
  border-color: #fca5a5;
}

.is-loading {
  opacity: 0.7;
  cursor: wait !important;
}

/* Test Result Styles */
.global-test-result-body {
  display: flex;
  gap: 24px;
  align-items: stretch;
}

.result-left-panel {
  width: 280px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
}

.result-right-panel {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.result-stats {
  display: grid;
  grid-template-columns: 1fr;
  gap: 16px;
  margin-bottom: 20px;
}

.stat-item {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.stat-label {
  font-size: 12px;
  color: #64748b;
  font-weight: 600;
}

.stat-value {
  font-size: 14px;
  color: #0f172a;
  font-weight: 700;
}

.text-success { color: #059669; }
.text-danger { color: #dc2626; }
.text-warning { color: #b45309; }

.result-text-box {
  background: #f8fafc;
  padding: 16px;
  border-radius: 12px;
  margin-bottom: 20px;
  border: 1px solid #e2e8f0;
}

.token-summary-box {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.token-chip-list {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.token-source-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.token-source-item {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 8px 10px;
  background: #ffffff;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
}

.token-source-name {
  font-size: 12px;
  font-weight: 600;
  color: #334155;
}

.token-source-value {
  font-size: 12px;
  color: #475569;
  word-break: break-all;
}

.token-chip {
  display: inline-flex;
  align-items: center;
  padding: 4px 10px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 600;
}

.token-chip-success {
  background: #dcfce7;
  color: #166534;
}

.token-chip-danger {
  background: #fee2e2;
  color: #b91c1c;
}

.token-rule-tip {
  font-size: 12px;
  color: #64748b;
  line-height: 1.5;
}

.text-label {
  font-size: 13px;
  color: #64748b;
  font-weight: 600;
  margin-bottom: 4px;
}

.text-highlight {
  font-weight: 700;
  color: #2563eb;
  font-size: 14px;
}

.text-full {
  font-size: 13px;
  color: #334155;
  white-space: pre-wrap;
  word-break: break-all;
  line-height: 1.5;
}

.result-preview {
  border-radius: 12px;
  overflow: hidden;
  border: 1px solid #e2e8f0;
  background: #f8fafc;
  display: flex;
  justify-content: center;
  align-items: center;
  padding: 8px;
  height: 100%;
  min-height: 400px;
}

.result-preview :deep(.match-preview),
.result-preview :deep(.match-canvas) {
  max-width: 100%;
  max-height: 60vh;
  object-fit: contain;
  border-radius: 8px;
}
</style>
