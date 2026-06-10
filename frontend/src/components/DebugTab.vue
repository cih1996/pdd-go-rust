<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import MatchPreview from './MatchPreview.vue'
import SelectionCanvas from './SelectionCanvas.vue'
import type { CropRegion, DebugCaptureResult, DebugResult, DebugSelectionTestResult, DeviceInfo } from '../types'
import { Camera, VideoPlay, Scissor, Aim } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'

const props = defineProps<{
  devices: DeviceInfo[]
  result: DebugResult | null
  running: boolean
  capture: DebugCaptureResult | null
  capturing: boolean
  selectionTesting: boolean
  selectionResult: DebugSelectionTestResult | null
  selectedDeviceId?: string
}>()

const emit = defineEmits<{
  (event: 'run', payload: { device_id: string; mode: 'url' | 'current'; url?: string }): void
  (event: 'capture-screen', deviceId: string): void
  (event: 'run-selection-test', payload: FormData): void
  (event: 'run-ocr-selection-test', payload: FormData): void
}>()

const mode = ref<'url' | 'current'>('url')
const deviceId = ref('')
const url = ref('')
const selectionEngine = ref<'opencv' | 'ocr'>('opencv')
const activeSelectionMode = ref<'template' | 'search'>('template')
const templateRect = ref<CropRegion | null>(null)
const searchRect = ref<CropRegion | null>(null)
const ocrRect = ref<CropRegion | null>(null)
const threshold = ref('0.8')
const method = ref<'ccoeff_normed' | 'ccorr_normed' | 'sqdiff_normed'>('ccoeff_normed')
const grayscale = ref(true)
const ocrExpectedText = ref('')
const ocrThreshold = ref('0.8')
const templatePreviewUrl = ref('')
const templatePreviewBlob = ref<Blob | null>(null)
let previewObjectUrl = ''
let captureObjectUrl = ''

const finalMatchedResult = computed(() => {
  if (!props.result) return null
  const matchedItems = props.result.opencv_results.filter((item) => item.match.found)
  return matchedItems.length ? matchedItems[matchedItems.length - 1] : null
})

const finalCaptureUrl = computed(() => {
  if (!props.result) return ''
  const captures = props.result.detail.capture_urls ?? []
  if (captures.length) return captures[captures.length - 1]
  return props.result.detail.capture_url ?? ''
})

function formatStageName(stageName: string) {
  if (stageName === '账号风控') return '账号风控'
  if (stageName === '失败释放') return '失败释放'
  if (stageName === '点击图') return '点击图'
  if (stageName === '成功图') return '成功图'
  return stageName
}

function formatTemplateType(templateType: string) {
  if (templateType === 'account_risk') return '账号风控'
  if (templateType === 'fail_release') return '失败释放'
  if (templateType === 'click_image') return '点击图'
  return '成功图'
}

function formatRecognitionEngine(engine: string) {
  return engine === 'ocr' ? 'OCR' : 'OpenCV'
}

function revokePreview() {
  if (previewObjectUrl) {
    URL.revokeObjectURL(previewObjectUrl)
    previewObjectUrl = ''
  }
  templatePreviewUrl.value = ''
  templatePreviewBlob.value = null
}

function revokeCaptureObjectUrl() {
  if (captureObjectUrl) {
    URL.revokeObjectURL(captureObjectUrl)
    captureObjectUrl = ''
  }
}

async function getLocalCaptureUrl(imageUrl: string): Promise<string> {
  revokeCaptureObjectUrl()
  const response = await fetch(imageUrl, { mode: 'cors' })
  if (!response.ok) throw new Error('调试截图读取失败')
  const blob = await response.blob()
  captureObjectUrl = URL.createObjectURL(blob)
  return captureObjectUrl
}

function submit() {
  if (!deviceId.value) {
    ElMessage.warning('请选择设备')
    return
  }
  if (mode.value === 'url' && !url.value) {
    ElMessage.warning('请输入跳转链接')
    return
  }
  emit('run', {
    device_id: deviceId.value,
    mode: mode.value,
    url: mode.value === 'url' ? url.value : undefined,
  })
}

function captureScreen() {
  if (!deviceId.value) {
    ElMessage.warning('请选择设备')
    return
  }
  emit('capture-screen', deviceId.value)
}

function clearRect(type: 'template' | 'search' | 'ocr') {
  if (type === 'template') {
    templateRect.value = null
    revokePreview()
  } else if (type === 'search') {
    searchRect.value = null
  } else {
    ocrRect.value = null
  }
}

function updateRect(type: 'template' | 'search' | 'ocr', key: keyof CropRegion, value: string) {
  const target = type === 'template' ? templateRect : type === 'search' ? searchRect : ocrRect
  const next = Number(value)
  if (!target.value) {
    target.value = { x: 0, y: 0, width: 0, height: 0 }
  }
  target.value = {
    ...target.value!,
    [key]: Number.isFinite(next) ? Math.max(0, Math.round(next)) : 0,
  }
}

function loadImage(urlValue: string): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const image = new Image()
    image.crossOrigin = 'anonymous'
    image.onload = () => resolve(image)
    image.onerror = () => reject(new Error('截图加载失败'))
    image.src = urlValue
  })
}

async function cropImageBlob(imageUrl: string, rect: CropRegion): Promise<Blob> {
  const image = await loadImage(imageUrl)
  const canvas = document.createElement('canvas')
  canvas.width = rect.width || 1
  canvas.height = rect.height || 1
  const context = canvas.getContext('2d')
  if (!context) throw new Error('浏览器不支持 Canvas')
  context.drawImage(
    image,
    rect.x,
    rect.y,
    rect.width || 1,
    rect.height || 1,
    0,
    0,
    rect.width || 1,
    rect.height || 1,
  )
  return new Promise((resolve, reject) => {
    canvas.toBlob((blob) => {
      if (!blob) {
        reject(new Error('生成小图失败'))
        return
      }
      resolve(blob)
    }, 'image/png')
  })
}

watch(
  () => props.selectedDeviceId,
  (value) => {
    if (value) deviceId.value = value
  },
  { immediate: true },
)

watch(
  () => props.capture?.capture_url,
  () => {
    templateRect.value = null
    searchRect.value = null
    ocrRect.value = null
    revokePreview()
  },
)

watch(
  [() => props.capture?.capture_url, templateRect],
  async ([captureUrl, rect]) => {
    revokePreview()
    if (!captureUrl || !rect?.width || !rect.height) return
    try {
      const localCaptureUrl = await getLocalCaptureUrl(captureUrl)
      const blob = await cropImageBlob(localCaptureUrl, rect)
      templatePreviewBlob.value = blob
      previewObjectUrl = URL.createObjectURL(blob)
      templatePreviewUrl.value = previewObjectUrl
    } catch {
      templatePreviewUrl.value = ''
      templatePreviewBlob.value = null
    }
  },
  { deep: true },
)

const canDownloadTemplate = computed(() => Boolean(templatePreviewBlob.value && templateRect.value?.width && templateRect.value?.height))

function downloadTemplateImage() {
  if (!templatePreviewBlob.value) {
    ElMessage.warning('请先框选小图区域')
    return
  }
  const downloadUrl = URL.createObjectURL(templatePreviewBlob.value)
  const link = document.createElement('a')
  const timestamp = new Date().toISOString().replace(/[:.]/g, '-')
  link.href = downloadUrl
  link.download = `template-crop-${timestamp}.png`
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
  URL.revokeObjectURL(downloadUrl)
}

async function submitSelectionTest() {
  try {
    if (!props.capture?.capture_url || !templateRect.value?.width || !templateRect.value.height) {
      ElMessage.warning('请先框选小图区域')
      return
    }
    const sourceResponse = await fetch(props.capture.capture_url, { mode: 'cors' })
    if (!sourceResponse.ok) throw new Error('调试截图读取失败')
    const sourceBlob = await sourceResponse.blob()
    const templateBlob = templatePreviewBlob.value
      ? templatePreviewBlob.value
      : await cropImageBlob(await getLocalCaptureUrl(props.capture.capture_url), templateRect.value)
    const payload = new FormData()
    payload.set('source_image', sourceBlob, 'debug-source.png')
    payload.set('template_image', templateBlob, 'selected-template.png')
    payload.set('threshold', threshold.value)
    payload.set('method', method.value)
    payload.set('grayscale', String(grayscale.value))
    if (searchRect.value?.width && searchRect.value.height) {
      payload.set('crop_x', String(searchRect.value.x))
      payload.set('crop_y', String(searchRect.value.y))
      payload.set('crop_width', String(searchRect.value.width))
      payload.set('crop_height', String(searchRect.value.height))
    }
    emit('run-selection-test', payload)
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : '生成调试小图失败')
  }
}

async function submitOcrSelectionTest() {
  try {
    if (!props.capture?.capture_url) {
      ElMessage.warning('请先抓取当前设备截图')
      return
    }
    if (!ocrExpectedText.value.trim()) {
      ElMessage.warning('请填写 OCR 期望文本')
      return
    }
    const sourceResponse = await fetch(props.capture.capture_url, { mode: 'cors' })
    if (!sourceResponse.ok) throw new Error('调试截图读取失败')
    const sourceBlob = await sourceResponse.blob()
    const payload = new FormData()
    payload.set('source_image', sourceBlob, 'debug-source.png')
    payload.set('expected_text', ocrExpectedText.value.trim())
    payload.set('threshold', ocrThreshold.value)
    if (ocrRect.value?.width && ocrRect.value.height) {
      payload.set('crop_x', String(ocrRect.value.x))
      payload.set('crop_y', String(ocrRect.value.y))
      payload.set('crop_width', String(ocrRect.value.width))
      payload.set('crop_height', String(ocrRect.value.height))
    }
    emit('run-ocr-selection-test', payload)
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : 'OCR 调试测试失败')
  }
}

onBeforeUnmount(() => {
  revokePreview()
  revokeCaptureObjectUrl()
})
</script>

<template>
  <div class="stack">
    <el-row :gutter="24">
      <el-col :span="12">
        <el-card shadow="never" class="mb-4">
          <template #header>
            <span style="font-size: 16px; font-weight: 500;">单次调试执行</span>
          </template>
          
          <el-form label-position="top">
            <el-row :gutter="12">
              <el-col :span="16">
                <el-form-item label="选择设备">
                  <el-select v-model="deviceId" placeholder="请选择设备" class="w-full">
                    <el-option v-for="device in devices" :key="device.serial" :label="device.serial" :value="device.serial" />
                  </el-select>
                </el-form-item>
              </el-col>
              <el-col :span="8">
                <el-form-item label="快捷操作">
                  <el-button class="w-full" type="primary" plain :icon="Camera" :loading="capturing" @click="captureScreen">
                    抓取屏幕
                  </el-button>
                </el-form-item>
              </el-col>
            </el-row>

            <el-form-item label="调试模式">
              <el-radio-group v-model="mode">
                <el-radio value="url">给定 URL</el-radio>
                <el-radio value="current">当前界面</el-radio>
              </el-radio-group>
            </el-form-item>

            <el-form-item label="拼多多 URL" v-if="mode === 'url'">
              <el-input v-model="url" placeholder="输入完整 pinduoduo:// 跳转链接" clearable />
            </el-form-item>

            <el-button type="primary" :icon="VideoPlay" class="w-full" :loading="running" @click="submit">
              执行单次调试
            </el-button>
          </el-form>
        </el-card>
      </el-col>

      <el-col :span="12">
        <el-card shadow="never" class="mb-4" style="height: 100%;">
          <template #header>
            <span style="font-size: 16px; font-weight: 500;">调试执行结果</span>
          </template>
          
          <div v-if="result">
            <el-descriptions :column="2" border size="small" class="mb-4">
              <el-descriptions-item label="任务ID" :span="2">{{ result.task_id }}</el-descriptions-item>
              <el-descriptions-item label="是否匹配">
                <el-tag :type="result.matched ? 'success' : 'danger'" size="small">
                  {{ result.matched ? '是' : '否' }}
                </el-tag>
              </el-descriptions-item>
              <el-descriptions-item label="状态">
                <el-tag type="info" size="small">{{ result.detail.status }}</el-tag>
              </el-descriptions-item>
              <el-descriptions-item label="总耗时">
                {{ result.timing?.total_elapsed_ms?.toFixed(2) ?? '-' }} ms
              </el-descriptions-item>
              <el-descriptions-item label="打开链接耗时">
                {{ result.timing?.open_url_elapsed_ms != null ? `${result.timing.open_url_elapsed_ms.toFixed(2)} ms` : '-' }}
              </el-descriptions-item>
              <el-descriptions-item label="识别结果" :span="2">{{ result.detail.recognition }}</el-descriptions-item>
              <el-descriptions-item label="说明" :span="2">{{ result.detail.message || '-' }}</el-descriptions-item>
            </el-descriptions>

            <div v-if="result.timing?.capture_steps?.length" class="mb-4">
              <div style="font-size: 14px; font-weight: 500; margin-bottom: 8px;">每轮截图耗时</div>
              <div class="timing-chip-list">
                <el-tag v-for="step in result.timing.capture_steps" :key="step.loop_count" effect="plain">
                  第 {{ step.loop_count }} 轮: {{ step.elapsed_ms.toFixed(2) }} ms
                </el-tag>
              </div>
            </div>

            <div v-if="finalCaptureUrl" class="mb-4">
              <div style="font-size: 14px; font-weight: 500; margin-bottom: 8px;">最终匹配截图</div>
              <div class="final-preview-wrap">
                <MatchPreview
                  :image-url="finalCaptureUrl"
                  :top-left="finalMatchedResult?.match.top_left"
                  :width="finalMatchedResult?.match.width"
                  :height="finalMatchedResult?.match.height"
                  :label="result.detail.recognition"
                />
              </div>
            </div>
            
            <div v-if="result.opencv_results.length > 0">
              <div style="font-size: 14px; font-weight: 500; margin-bottom: 8px;">识别详情</div>
              <el-collapse>
                <el-collapse-item
                  v-for="(item, index) in result.opencv_results"
                  :key="`${item.template_id}-${item.loop_count}-${item.stage_name}-${index}`"
                  :title="`第 ${item.loop_count} 轮 / ${formatStageName(item.stage_name)} / ${item.template_label}${item.match.found ? ' (命中)' : ' (未命中)'}`"
                  :name="`${item.template_id}-${item.loop_count}-${item.stage_name}-${index}`"
                >
                  <p style="margin: 0; font-size: 13px;">
                    <strong>类型:</strong>
                    {{ formatTemplateType(item.template_type) }}
                  </p>
                  <p style="margin: 4px 0 0; font-size: 13px;"><strong>引擎:</strong> {{ formatRecognitionEngine(item.recognition_engine) }}</p>
                  <p style="margin: 4px 0 0; font-size: 13px;"><strong>轮次:</strong> 第 {{ item.loop_count }} 轮 / <strong>阶段:</strong> {{ formatStageName(item.stage_name) }}</p>
                  <p style="margin: 4px 0 0; font-size: 13px;"><strong>置信度:</strong> {{ item.match.confidence }} / <strong>阈值:</strong> {{ item.match.threshold }}</p>
                  <p style="margin: 4px 0 0; font-size: 13px;">
                    <strong>模板识别耗时:</strong> {{ item.request_elapsed_ms.toFixed(2) }} ms
                    / <strong>{{ item.recognition_engine === 'ocr' ? 'OCR耗时' : 'OpenCV耗时' }}:</strong> {{ item.match.elapsed_ms }} ms
                  </p>
                  <p v-if="item.match.matched_text" style="margin: 4px 0 0; font-size: 13px;"><strong>命中文本:</strong> {{ item.match.matched_text }}</p>
                  <p v-if="item.match.full_text" style="margin: 4px 0 0; font-size: 13px; white-space: pre-wrap;"><strong>识别全文:</strong> {{ item.match.full_text }}</p>
                </el-collapse-item>
              </el-collapse>
            </div>
          </div>
          <el-empty v-else description="尚未执行调试" />
        </el-card>
      </el-col>
    </el-row>

    <el-card shadow="never">
      <template #header>
        <div class="flex-between">
          <span style="font-size: 16px; font-weight: 500;">截图框选工作台</span>
          <el-tag v-if="capture" type="info">当前设备: {{ capture.device_id }}</el-tag>
        </div>
      </template>

      <div v-if="capture">
        <el-row :gutter="24">
          <el-col :span="13">
            <div class="mb-4">
              <div class="selection-toolbar">
                <el-radio-group v-model="selectionEngine" size="small">
                  <el-radio-button value="opencv">OpenCV 找图调试</el-radio-button>
                  <el-radio-button value="ocr">OCR 文本调试</el-radio-button>
                </el-radio-group>
                <el-radio-group v-if="selectionEngine === 'opencv'" v-model="activeSelectionMode" size="small">
                  <el-radio-button value="template"><el-icon><Scissor /></el-icon> 框选小图区域</el-radio-button>
                  <el-radio-button value="search"><el-icon><Aim /></el-icon> 框选查找区域</el-radio-button>
                </el-radio-group>
                <el-tag v-else type="warning" effect="plain">当前拖拽框选的是 OCR 识别区域</el-tag>
              </div>
              <div class="selection-tip">
                预览图已缩小显示，框选坐标仍按原始截图像素换算，不影响模板区域和 OCR 区域精度。
              </div>
            </div>

            <SelectionCanvas
              :image-url="capture.capture_url"
              :active-mode="selectionEngine === 'ocr' ? 'ocr' : activeSelectionMode"
              :template-rect="templateRect"
              :search-rect="searchRect"
              :ocr-rect="ocrRect"
              @update:template-rect="templateRect = $event"
              @update:search-rect="searchRect = $event"
              @update:ocr-rect="ocrRect = $event"
            />
          </el-col>

          <el-col :span="11">
            <el-card shadow="hover" class="mb-4">
              <template #header>
                <div class="flex-between">
                  <span>OpenCV 小图区域 (目标)</span>
                  <el-button type="primary" link @click="clearRect('template')">清空</el-button>
                </div>
              </template>
              <el-row :gutter="8" class="mb-4">
                <el-col :span="6"><el-input :value="templateRect?.x ?? 0" size="small" placeholder="X" @input="updateRect('template', 'x', $event)" /></el-col>
                <el-col :span="6"><el-input :value="templateRect?.y ?? 0" size="small" placeholder="Y" @input="updateRect('template', 'y', $event)" /></el-col>
                <el-col :span="6"><el-input :value="templateRect?.width ?? 0" size="small" placeholder="宽" @input="updateRect('template', 'width', $event)" /></el-col>
                <el-col :span="6"><el-input :value="templateRect?.height ?? 0" size="small" placeholder="高" @input="updateRect('template', 'height', $event)" /></el-col>
              </el-row>
              <div v-if="templatePreviewUrl" style="display: flex; justify-content: center; background: #f5f7fa; padding: 12px; border-radius: 4px; border: 1px dashed #dcdfe6;">
                <div style="display: flex; flex-direction: column; gap: 12px; align-items: center; width: 100%;">
                  <img :src="templatePreviewUrl" alt="框选小图预览" style="max-width: 100%; max-height: 120px;" />
                  <el-button type="primary" plain size="small" :disabled="!canDownloadTemplate" @click="downloadTemplateImage">
                    下载小图
                  </el-button>
                </div>
              </div>
              <div v-else style="color: #909399; font-size: 12px; text-align: center; padding: 20px;">
                请在左侧截图上拖动框选小图
              </div>
            </el-card>

            <el-card shadow="hover" class="mb-4">
              <template #header>
                <div class="flex-between">
                  <span>OpenCV 查找区域 (限制范围)</span>
                  <el-button type="primary" link @click="clearRect('search')">清空</el-button>
                </div>
              </template>
              <el-row :gutter="8">
                <el-col :span="6"><el-input :value="searchRect?.x ?? 0" size="small" placeholder="X" @input="updateRect('search', 'x', $event)" /></el-col>
                <el-col :span="6"><el-input :value="searchRect?.y ?? 0" size="small" placeholder="Y" @input="updateRect('search', 'y', $event)" /></el-col>
                <el-col :span="6"><el-input :value="searchRect?.width ?? 0" size="small" placeholder="宽" @input="updateRect('search', 'width', $event)" /></el-col>
                <el-col :span="6"><el-input :value="searchRect?.height ?? 0" size="small" placeholder="高" @input="updateRect('search', 'height', $event)" /></el-col>
              </el-row>
              <div style="font-size: 12px; color: #909399; margin-top: 8px;">不填时默认全屏查找</div>
            </el-card>

            <el-card shadow="hover" class="mb-4">
              <template #header>
                <span>OpenCV 参数与测试</span>
              </template>
              <el-form size="small" label-width="80px">
                <el-form-item label="算法">
                  <el-select v-model="method" class="w-full">
                    <el-option label="ccoeff_normed" value="ccoeff_normed" />
                    <el-option label="ccorr_normed" value="ccorr_normed" />
                    <el-option label="sqdiff_normed" value="sqdiff_normed" />
                  </el-select>
                </el-form-item>
                <el-form-item label="阈值">
                  <el-input v-model="threshold" placeholder="默认 0.8" />
                </el-form-item>
                <el-form-item label="灰度识别">
                  <el-switch v-model="grayscale" />
                </el-form-item>
                <el-button type="primary" class="w-full" :loading="selectionTesting" :disabled="!templateRect?.width" @click="submitSelectionTest">
                  测试找图
                </el-button>
              </el-form>
            </el-card>

            <el-card shadow="hover">
              <template #header>
                <div class="flex-between">
                  <span>OCR 参数与测试</span>
                  <el-button type="primary" link @click="clearRect('ocr')">清空 OCR 区域</el-button>
                </div>
              </template>
              <el-row :gutter="8" class="mb-4">
                <el-col :span="6"><el-input :value="ocrRect?.x ?? 0" size="small" placeholder="X" @input="updateRect('ocr', 'x', $event)" /></el-col>
                <el-col :span="6"><el-input :value="ocrRect?.y ?? 0" size="small" placeholder="Y" @input="updateRect('ocr', 'y', $event)" /></el-col>
                <el-col :span="6"><el-input :value="ocrRect?.width ?? 0" size="small" placeholder="宽" @input="updateRect('ocr', 'width', $event)" /></el-col>
                <el-col :span="6"><el-input :value="ocrRect?.height ?? 0" size="small" placeholder="高" @input="updateRect('ocr', 'height', $event)" /></el-col>
              </el-row>
              <el-form size="small" label-width="88px">
                <el-form-item label="期望文本">
                  <el-input v-model="ocrExpectedText" placeholder="例如：店铺优惠&立即支付，多个条件用 & 连接" />
                </el-form-item>
                <el-form-item label="OCR 阈值">
                  <el-input v-model="ocrThreshold" placeholder="默认 0.8，越高越严格" />
                </el-form-item>
                <div style="font-size: 12px; color: #909399; margin-bottom: 12px;">
                  不框选 OCR 区域时默认全屏识别；使用 & 时，区域内多个文本条件需同时满足才算命中。
                </div>
                <el-button type="warning" class="w-full" :loading="selectionTesting" @click="submitOcrSelectionTest">
                  测试 OCR
                </el-button>
              </el-form>
            </el-card>

            <div v-if="selectionResult" style="margin-top: 16px;">
              <el-alert
                :title="selectionResult.match.found ? '命中成功' : '未命中'"
                :type="selectionResult.match.found ? 'success' : 'error'"
                :description="`${selectionResult.recognition_engine === 'ocr' ? 'OCR' : 'OpenCV'} / 置信度: ${selectionResult.match.confidence} / 阈值: ${selectionResult.match.threshold}`"
                show-icon
                :closable="false"
              />
              <div v-if="selectionResult.match.matched_text || selectionResult.match.full_text" class="selection-result-text">
                <p v-if="selectionResult.match.matched_text"><strong>命中文本：</strong>{{ selectionResult.match.matched_text }}</p>
                <p v-if="selectionResult.match.full_text"><strong>识别全文：</strong>{{ selectionResult.match.full_text }}</p>
              </div>
              <div class="mt-4" v-if="selectionResult.match.found">
                <MatchPreview
                  :image-url="capture.capture_url"
                  :top-left="selectionResult.match.top_left"
                  :width="selectionResult.match.width"
                  :height="selectionResult.match.height"
                  label="框选找图结果"
                />
              </div>
            </div>
          </el-col>
        </el-row>
      </div>
      <el-empty v-else description="请先在上方抓取设备屏幕" />
    </el-card>
  </div>
</template>

<style scoped>
.selection-toolbar {
  display: flex;
  gap: 12px;
  align-items: center;
  flex-wrap: wrap;
}

.selection-tip {
  margin-top: 10px;
  font-size: 12px;
  color: #909399;
}

.selection-result-text {
  margin-top: 12px;
  padding: 12px;
  background: #f8fafc;
  border-radius: 8px;
  font-size: 13px;
  color: #606266;
}

.selection-result-text p {
  margin: 0 0 8px;
  white-space: pre-wrap;
}

.selection-result-text p:last-child {
  margin-bottom: 0;
}

.timing-chip-list {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.final-preview-wrap :deep(.match-preview),
.final-preview-wrap :deep(.match-canvas) {
  max-height: 400px;
}

.final-preview-wrap :deep(img) {
  width: auto;
  max-width: 100%;
  max-height: 400px;
  object-fit: contain;
}
</style>
