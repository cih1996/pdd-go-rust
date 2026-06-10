<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import MatchPreview from './MatchPreview.vue'
import SelectionCanvas from './SelectionCanvas.vue'
import type { CropRegion, DebugCaptureResult, DebugResult, DebugRunStreamEvent, DebugSelectionTestResult, DeviceInfo } from '../types'
import { Camera, VideoPlay, Scissor, Aim, Cellphone, Monitor, Picture } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'

const props = defineProps<{
  devices: DeviceInfo[]
  result: DebugResult | null
  streamEvents?: Array<{ type: string; data: DebugRunStreamEvent }>
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

const activeTab = ref('execution')
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

function formatOcrExecution(item?: DebugRunStreamEvent | DebugResult['opencv_results'][number] | null) {
  if (!item || item.recognition_engine !== 'ocr') return ''
  const match = 'match' in item ? item.match : item
  if (match.ocr_used_cache) return 'OCR 复用本轮缓存'
  if (match.ocr_executed) return `OCR 执行 ${Number(match.ocr_exec_elapsed_ms ?? 0).toFixed(2)} ms`
  return 'OCR 未执行'
}

function formatStreamTitle(item: { type: string; data: DebugRunStreamEvent }) {
  const loopPrefix = item.data.loop_count ? `第 ${item.data.loop_count} 轮 / ` : ''
  if (item.type === 'debug_run_started') return '调试开始'
  if (item.type === 'debug_run_url_opened') return `打开链接完成 / ${Number(item.data.elapsed_ms ?? 0).toFixed(2)} ms`
  if (item.type === 'debug_run_loop_started') return `${loopPrefix}开始循环`
  if (item.type === 'debug_run_capture') return `${loopPrefix}截图完成 / ${Number(item.data.elapsed_ms ?? 0).toFixed(2)} ms`
  if (item.type === 'debug_run_stage_started') return `${loopPrefix}${item.data.stage_name} / 共 ${item.data.template_count ?? 0} 张图`
  if (item.type === 'debug_run_template_result') {
    const timing = `${Number(item.data.elapsed_ms ?? item.data.request_elapsed_ms ?? 0).toFixed(2)} ms`
    const ocrText = formatOcrExecution(item.data)
    return `${loopPrefix}${item.data.stage_name} / ${item.data.template_label ?? '-'} / ${item.data.found ? '命中' : '未命中'} / ${timing}${ocrText ? ` / ${ocrText}` : ''}`
  }
  if (item.type === 'debug_run_click_performed') return `${loopPrefix}执行点击 / ${Number(item.data.elapsed_ms ?? 0).toFixed(2)} ms`
  if (item.type === 'debug_run_finished') return '调试完成'
  if (item.type === 'debug_run_error') return item.data.message ?? '调试失败'
  if (item.type === 'debug_run_cancelled') return '调试已取消'
  return item.type
}

function getTimelineItemType(type: string) {
  if (type === 'debug_run_error') return 'danger'
  if (type === 'debug_run_finished') return 'success'
  if (type === 'debug_run_started') return 'primary'
  if (type === 'debug_run_click_performed') return 'warning'
  return 'info'
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
  <div class="debug-container">
    <!-- Global Toolbar -->
    <div class="global-toolbar">
      <div class="toolbar-left">
        <el-select v-model="deviceId" placeholder="请选择目标设备" class="device-select" size="large">
          <template #prefix>
            <el-icon><Cellphone /></el-icon>
          </template>
          <el-option v-for="device in devices" :key="device.serial" :label="device.serial" :value="device.serial" />
        </el-select>
        <el-button type="primary" :icon="Camera" :loading="capturing" @click="captureScreen" size="large" class="ml-4 capture-btn">
          抓取屏幕
        </el-button>
      </div>
      <div class="toolbar-right">
        <el-tag v-if="capture" type="success" effect="light" round size="large">
          已抓取设备: {{ capture.device_id }}
        </el-tag>
        <el-tag v-else type="info" effect="light" round size="large">
          暂无屏幕快照
        </el-tag>
      </div>
    </div>

    <!-- Main Content Tabs -->
    <el-tabs v-model="activeTab" class="modern-tabs mt-4">
      <!-- Tab 1: Execution & Monitoring -->
      <el-tab-pane name="execution">
        <template #label>
          <span class="custom-tab-label">
            <el-icon><Monitor /></el-icon>
            <span>流程执行与监控</span>
          </span>
        </template>
        
        <el-row :gutter="24">
          <el-col :span="8">
            <el-card shadow="never" class="modern-card mb-4">
              <template #header><div class="card-title">执行配置</div></template>
              <el-form label-position="top">
                <el-form-item label="调试模式">
                  <el-radio-group v-model="mode" class="w-full custom-radio">
                    <el-radio-button value="url" class="w-1/2">给定 URL</el-radio-button>
                    <el-radio-button value="current" class="w-1/2">当前界面</el-radio-button>
                  </el-radio-group>
                </el-form-item>
                <el-form-item label="拼多多 URL" v-if="mode === 'url'">
                  <el-input v-model="url" placeholder="输入完整 pinduoduo:// 跳转链接" clearable />
                </el-form-item>
                <el-button type="primary" :icon="VideoPlay" class="w-full run-btn mt-2" size="large" :loading="running" @click="submit">
                  开始调试
                </el-button>
              </el-form>
            </el-card>

            <el-card shadow="never" class="modern-card stream-card">
              <template #header><div class="card-title">实时日志</div></template>
              <div v-if="streamEvents?.length" class="timeline-container">
                <el-timeline>
                  <el-timeline-item
                    v-for="(item, index) in streamEvents"
                    :key="`${item.type}-${index}`"
                    :type="getTimelineItemType(item.type)"
                    :hollow="true"
                    size="large"
                  >
                    <div class="timeline-content">{{ formatStreamTitle(item) }}</div>
                  </el-timeline-item>
                </el-timeline>
              </div>
              <el-empty v-else description="暂无日志" :image-size="80" />
            </el-card>
          </el-col>

          <el-col :span="16">
            <el-card shadow="never" class="modern-card h-full result-container">
              <template #header><div class="card-title">执行结果</div></template>
              <div v-if="result">
                <el-tabs class="inner-tabs">
                  <el-tab-pane label="基本信息" name="basic">
                    <el-descriptions :column="2" border class="result-descriptions" size="large">
                      <el-descriptions-item label="任务ID" :span="2">
                        <span class="mono-text">{{ result.task_id }}</span>
                      </el-descriptions-item>
                      <el-descriptions-item label="是否匹配">
                        <el-tag :type="result.matched ? 'success' : 'danger'" size="default" effect="dark">
                          {{ result.matched ? '是' : '否' }}
                        </el-tag>
                      </el-descriptions-item>
                      <el-descriptions-item label="状态">
                        <el-tag type="info" size="default">{{ result.detail.status }}</el-tag>
                      </el-descriptions-item>
                      <el-descriptions-item label="总耗时">
                        <span class="highlight-text">{{ result.timing?.total_elapsed_ms?.toFixed(2) ?? '-' }} ms</span>
                      </el-descriptions-item>
                      <el-descriptions-item label="打开链接耗时">
                        {{ result.timing?.open_url_elapsed_ms != null ? `${result.timing.open_url_elapsed_ms.toFixed(2)} ms` : '-' }}
                      </el-descriptions-item>
                      <el-descriptions-item label="识别结果" :span="2">
                        <strong>{{ result.detail.recognition }}</strong>
                      </el-descriptions-item>
                      <el-descriptions-item label="说明" :span="2">{{ result.detail.message || '-' }}</el-descriptions-item>
                    </el-descriptions>

                    <div v-if="result.timing?.capture_steps?.length" class="mt-6">
                      <div class="section-title">截图耗时统计</div>
                      <div class="timing-chip-list mt-3">
                        <el-tag v-for="step in result.timing.capture_steps" :key="step.loop_count" effect="plain" size="large" round>
                          第 {{ step.loop_count }} 轮: {{ step.elapsed_ms.toFixed(2) }} ms
                        </el-tag>
                      </div>
                    </div>
                  </el-tab-pane>
                  
                  <el-tab-pane label="匹配详情" name="details">
                    <div v-if="result.opencv_results.length > 0">
                      <el-collapse class="modern-collapse" accordion>
                        <el-collapse-item
                          v-for="(item, index) in result.opencv_results"
                          :key="`${item.template_id}-${item.loop_count}-${item.stage_name}-${index}`"
                          :name="`${item.template_id}-${item.loop_count}-${item.stage_name}-${index}`"
                        >
                          <template #title>
                            <div class="collapse-title">
                              <el-tag :type="item.match.found ? 'success' : 'info'" size="small" effect="dark" class="mr-2">
                                {{ item.match.found ? '命中' : '未命中' }}
                              </el-tag>
                              <span>第 {{ item.loop_count }} 轮 / {{ formatStageName(item.stage_name) }} / {{ item.template_label }}</span>
                            </div>
                          </template>
                          <div class="detail-content">
                            <el-row :gutter="16">
                              <el-col :span="12">
                                <div class="detail-item"><strong>类型:</strong> {{ formatTemplateType(item.template_type) }}</div>
                                <div class="detail-item"><strong>引擎:</strong> <el-tag size="small">{{ formatRecognitionEngine(item.recognition_engine) }}</el-tag></div>
                                <div class="detail-item"><strong>置信度:</strong> {{ item.match.confidence }} / <strong>阈值:</strong> {{ item.match.threshold }}</div>
                              </el-col>
                              <el-col :span="12">
                                <div class="detail-item"><strong>调度耗时:</strong> {{ item.request_elapsed_ms.toFixed(2) }} ms</div>
                                <div class="detail-item"><strong>{{ item.recognition_engine === 'ocr' ? 'OCR/匹配耗时' : 'OpenCV 耗时' }}:</strong> {{ Number(item.match.elapsed_ms ?? 0).toFixed(2) }} ms</div>
                                <div v-if="item.recognition_engine === 'ocr'" class="detail-item">
                                  <strong>OCR 执行:</strong>
                                  <el-tag size="small" type="warning" effect="plain" class="ml-1">
                                    {{ item.match.ocr_used_cache ? '复用本轮缓存' : item.match.ocr_executed ? '本次重新识别' : '未执行' }}
                                  </el-tag>
                                  <span v-if="item.match.ocr_executed" class="ml-2 text-gray">
                                    (耗时: {{ Number(item.match.ocr_exec_elapsed_ms ?? 0).toFixed(2) }} ms)
                                  </span>
                                </div>
                              </el-col>
                            </el-row>
                            <div v-if="item.match.matched_text || item.match.full_text" class="text-result-box mt-3">
                              <div v-if="item.match.matched_text" class="mb-1"><strong>命中文本:</strong> <span class="highlight-text">{{ item.match.matched_text }}</span></div>
                              <div v-if="item.match.full_text"><strong>识别全文:</strong> <div class="full-text">{{ item.match.full_text }}</div></div>
                            </div>
                          </div>
                        </el-collapse-item>
                      </el-collapse>
                    </div>
                    <el-empty v-else description="无匹配详情" :image-size="80" />
                  </el-tab-pane>

                  <el-tab-pane label="最终截图" name="capture" v-if="finalCaptureUrl">
                    <div class="final-preview-wrap mt-2">
                      <MatchPreview
                        :image-url="finalCaptureUrl"
                        :top-left="finalMatchedResult?.match.top_left"
                        :width="finalMatchedResult?.match.width"
                        :height="finalMatchedResult?.match.height"
                        :label="result.detail.recognition"
                      />
                    </div>
                  </el-tab-pane>
                </el-tabs>
              </div>
              <el-empty v-else description="尚未执行调试，无结果" :image-size="120" />
            </el-card>
          </el-col>
        </el-row>
      </el-tab-pane>

      <!-- Tab 2: Image & Text Debugging (Workbench) -->
      <el-tab-pane name="workbench">
        <template #label>
          <span class="custom-tab-label">
            <el-icon><Picture /></el-icon>
            <span>图像与特征调试</span>
          </span>
        </template>
        
        <div v-if="capture">
          <el-row :gutter="24">
            <!-- Left: Canvas -->
            <el-col :span="14">
              <el-card shadow="never" class="modern-card canvas-card">
                <template #header>
                  <div class="flex-between">
                    <div class="card-title">屏幕预览</div>
                    <el-tag type="info" effect="light" round>当前设备: {{ capture.device_id }}</el-tag>
                  </div>
                </template>
                
                <div class="selection-toolbar mb-4">
                  <el-radio-group v-model="selectionEngine" size="default" class="engine-switch">
                    <el-radio-button value="opencv">OpenCV 找图</el-radio-button>
                    <el-radio-button value="ocr">OCR 文本</el-radio-button>
                  </el-radio-group>
                  
                  <div v-if="selectionEngine === 'opencv'" class="mode-switch-wrapper ml-4">
                    <el-radio-group v-model="activeSelectionMode" size="default">
                      <el-radio-button value="template"><el-icon><Scissor /></el-icon> 小图区域</el-radio-button>
                      <el-radio-button value="search"><el-icon><Aim /></el-icon> 查找区域</el-radio-button>
                    </el-radio-group>
                  </div>
                  <div v-else class="ml-4">
                     <el-tag type="warning" effect="light" round>提示: 当前框选即为 OCR 识别区域</el-tag>
                  </div>
                </div>
                
                <div class="canvas-wrapper">
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
                </div>
                <div class="selection-tip mt-3">
                  <el-icon><Monitor /></el-icon> 预览图已缩小显示，框选坐标仍按原始截图像素换算，不影响区域精度。
                </div>
              </el-card>
            </el-col>

            <!-- Right: Config & Results -->
            <el-col :span="10">
              <el-card shadow="never" class="modern-card toolbox-card mb-4">
                <el-tabs v-model="selectionEngine" class="toolbox-tabs inner-tabs">
                  <el-tab-pane label="OpenCV 找图配置" name="opencv">
                    <div class="config-section">
                      <div class="section-header">
                        <span class="section-title">目标小图 (Template)</span>
                        <el-button link type="primary" @click="clearRect('template')">清空</el-button>
                      </div>
                      <el-row :gutter="12" class="mb-3">
                        <el-col :span="6"><el-input :value="templateRect?.x ?? 0" size="default" placeholder="X" @input="updateRect('template', 'x', $event)" /></el-col>
                        <el-col :span="6"><el-input :value="templateRect?.y ?? 0" size="default" placeholder="Y" @input="updateRect('template', 'y', $event)" /></el-col>
                        <el-col :span="6"><el-input :value="templateRect?.width ?? 0" size="default" placeholder="宽" @input="updateRect('template', 'width', $event)" /></el-col>
                        <el-col :span="6"><el-input :value="templateRect?.height ?? 0" size="default" placeholder="高" @input="updateRect('template', 'height', $event)" /></el-col>
                      </el-row>
                      <div class="preview-box" v-if="templatePreviewUrl">
                        <img :src="templatePreviewUrl" alt="框选小图预览" />
                        <el-button type="primary" plain size="small" :disabled="!canDownloadTemplate" @click="downloadTemplateImage" class="mt-2">
                          下载小图
                        </el-button>
                      </div>
                      <div v-else class="empty-preview">
                        请在左侧截图上拖动框选小图
                      </div>
                    </div>
                    
                    <el-divider border-style="dashed" class="my-4" />
                    
                    <div class="config-section">
                      <div class="section-header">
                        <span class="section-title">限制范围 (Search)</span>
                        <el-button link type="primary" @click="clearRect('search')">清空</el-button>
                      </div>
                      <el-row :gutter="12">
                        <el-col :span="6"><el-input :value="searchRect?.x ?? 0" size="default" placeholder="X" @input="updateRect('search', 'x', $event)" /></el-col>
                        <el-col :span="6"><el-input :value="searchRect?.y ?? 0" size="default" placeholder="Y" @input="updateRect('search', 'y', $event)" /></el-col>
                        <el-col :span="6"><el-input :value="searchRect?.width ?? 0" size="default" placeholder="宽" @input="updateRect('search', 'width', $event)" /></el-col>
                        <el-col :span="6"><el-input :value="searchRect?.height ?? 0" size="default" placeholder="高" @input="updateRect('search', 'height', $event)" /></el-col>
                      </el-row>
                      <div class="text-xs text-gray mt-2">不填时默认全屏查找</div>
                    </div>

                    <el-divider border-style="dashed" class="my-4" />
                    
                    <div class="config-section">
                      <div class="section-header"><span class="section-title">算法参数</span></div>
                      <el-form size="default" label-position="left" label-width="80px">
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
                      </el-form>
                      <el-button type="primary" class="w-full mt-2 test-btn" size="large" :loading="selectionTesting" :disabled="!templateRect?.width" @click="submitSelectionTest">
                        运行 OpenCV 测试
                      </el-button>
                    </div>
                  </el-tab-pane>

                  <el-tab-pane label="OCR 文本配置" name="ocr">
                    <div class="config-section">
                      <div class="section-header">
                        <span class="section-title">识别区域 (OCR Rect)</span>
                        <el-button link type="primary" @click="clearRect('ocr')">清空</el-button>
                      </div>
                      <el-row :gutter="12" class="mb-3">
                        <el-col :span="6"><el-input :value="ocrRect?.x ?? 0" size="default" placeholder="X" @input="updateRect('ocr', 'x', $event)" /></el-col>
                        <el-col :span="6"><el-input :value="ocrRect?.y ?? 0" size="default" placeholder="Y" @input="updateRect('ocr', 'y', $event)" /></el-col>
                        <el-col :span="6"><el-input :value="ocrRect?.width ?? 0" size="default" placeholder="宽" @input="updateRect('ocr', 'width', $event)" /></el-col>
                        <el-col :span="6"><el-input :value="ocrRect?.height ?? 0" size="default" placeholder="高" @input="updateRect('ocr', 'height', $event)" /></el-col>
                      </el-row>
                      <div class="text-xs text-gray">不框选时默认全屏识别</div>
                    </div>

                    <el-divider border-style="dashed" class="my-4" />
                    
                    <div class="config-section">
                      <div class="section-header"><span class="section-title">识别参数</span></div>
                      <el-form size="default" label-position="top">
                        <el-form-item label="期望文本">
                          <el-input v-model="ocrExpectedText" placeholder="如：店铺优惠&立即支付" />
                          <div class="text-xs text-gray mt-1">使用 & 时，区域内多个文本条件需同时满足才算命中</div>
                        </el-form-item>
                        <el-form-item label="OCR 阈值">
                          <el-input v-model="ocrThreshold" placeholder="默认 0.8，越高越严格" />
                        </el-form-item>
                      </el-form>
                      <el-button type="warning" class="w-full mt-4 test-btn" size="large" :loading="selectionTesting" @click="submitOcrSelectionTest">
                        运行 OCR 测试
                      </el-button>
                    </div>
                  </el-tab-pane>
                </el-tabs>
              </el-card>

              <!-- Test Result Display -->
              <transition name="el-fade-in-linear">
                <el-card v-if="selectionResult" shadow="never" class="modern-card result-card-animate">
                  <template #header><div class="card-title">测试结果</div></template>
                  <el-alert
                    :title="selectionResult.match.found ? '命中成功' : '未命中'"
                    :type="selectionResult.match.found ? 'success' : 'error'"
                    :description="`${selectionResult.recognition_engine === 'ocr' ? 'OCR' : 'OpenCV'} / 置信度: ${selectionResult.match.confidence} / 阈值: ${selectionResult.match.threshold}`"
                    show-icon
                    :closable="false"
                    class="mb-3"
                  />
                  <div v-if="selectionResult.match.matched_text || selectionResult.match.full_text" class="text-result-box mb-3">
                    <div v-if="selectionResult.match.matched_text" class="mb-1"><strong>命中文本：</strong><span class="highlight-text">{{ selectionResult.match.matched_text }}</span></div>
                    <div v-if="selectionResult.match.full_text"><strong>识别全文：</strong><div class="full-text">{{ selectionResult.match.full_text }}</div></div>
                  </div>
                  <div v-if="selectionResult.match.found" class="final-preview-wrap">
                    <MatchPreview
                      :image-url="capture.capture_url"
                      :top-left="selectionResult.match.top_left"
                      :width="selectionResult.match.width"
                      :height="selectionResult.match.height"
                      label="框选找图结果"
                    />
                  </div>
                </el-card>
              </transition>
            </el-col>
          </el-row>
        </div>
        <el-empty v-else description="请先在顶部操作栏点击【抓取屏幕】" :image-size="120">
          <el-button type="primary" :icon="Camera" :loading="capturing" @click="captureScreen" size="large">立即抓取</el-button>
        </el-empty>
      </el-tab-pane>
    </el-tabs>
  </div>
</template>

<style scoped>
/* Base Container */
.debug-container {
  display: flex;
  flex-direction: column;
  height: 100%;
  background-color: transparent;
}

/* Global Toolbar */
.global-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  background: #ffffff;
  padding: 16px 24px;
  border-radius: 12px;
  box-shadow: 0 2px 12px 0 rgba(0, 0, 0, 0.04);
}
.toolbar-left {
  display: flex;
  align-items: center;
}
.device-select {
  width: 320px;
}

/* Typography & Colors */
.card-title {
  font-size: 16px;
  font-weight: 600;
  color: #1f2937;
}
.section-title {
  font-size: 14px;
  font-weight: 600;
  color: #374151;
}
.text-gray {
  color: #6b7280;
}
.text-xs {
  font-size: 12px;
}
.mono-text {
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, Courier, monospace;
}
.highlight-text {
  color: #0ea5e9;
  font-weight: 500;
}

/* Modern Cards */
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

/* Layout Utilities */
.flex-between {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.w-full {
  width: 100%;
}
.w-1\/2 {
  width: 50%;
}
.ml-1 { margin-left: 4px; }
.ml-2 { margin-left: 8px; }
.ml-4 { margin-left: 16px; }
.mr-2 { margin-right: 8px; }
.mt-1 { margin-top: 4px; }
.mt-2 { margin-top: 8px; }
.mt-3 { margin-top: 12px; }
.mt-4 { margin-top: 16px; }
.mt-6 { margin-top: 24px; }
.mb-1 { margin-bottom: 4px; }
.mb-3 { margin-bottom: 12px; }
.mb-4 { margin-bottom: 16px; }
.my-4 { margin-top: 16px; margin-bottom: 16px; }

/* Tabs Styling */
.modern-tabs {
  background: #ffffff;
  border-radius: 12px;
  padding: 16px 24px;
  box-shadow: 0 2px 12px 0 rgba(0, 0, 0, 0.04);
}
.modern-tabs :deep(.el-tabs__nav-wrap::after) {
  height: 1px;
  background-color: #f3f4f6;
}
.custom-tab-label {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 15px;
  font-weight: 500;
}
.inner-tabs :deep(.el-tabs__item) {
  font-weight: 500;
}

/* Timeline */
.stream-card {
  min-height: 400px;
}
.timeline-container {
  max-height: 500px;
  overflow-y: auto;
  padding-right: 16px;
}
.timeline-content {
  font-size: 14px;
  color: #4b5563;
  line-height: 1.5;
}

/* Results Formatting */
.result-descriptions {
  border-radius: 8px;
  overflow: hidden;
}
.timing-chip-list {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
}
.detail-item {
  font-size: 13px;
  color: #4b5563;
  margin-bottom: 6px;
}
.text-result-box {
  background: #f8fafc;
  border-radius: 8px;
  padding: 12px 16px;
  font-size: 13px;
  color: #334155;
  border: 1px solid #e2e8f0;
}
.full-text {
  white-space: pre-wrap;
  background: #ffffff;
  padding: 8px;
  border-radius: 6px;
  margin-top: 4px;
  border: 1px dashed #cbd5e1;
}
.collapse-title {
  display: flex;
  align-items: center;
  font-weight: 500;
  font-size: 14px;
}

/* Selection Workbench */
.selection-toolbar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  background: #f8fafc;
  padding: 12px 16px;
  border-radius: 8px;
}
.selection-tip {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: #64748b;
}
.canvas-wrapper {
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  overflow: hidden;
  background: #f1f5f9;
}
.config-section {
  padding: 4px 0;
}
.section-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12px;
}
.preview-box {
  display: flex;
  flex-direction: column;
  align-items: center;
  background: #f8fafc;
  padding: 16px;
  border-radius: 8px;
  border: 1px dashed #cbd5e1;
}
.preview-box img {
  max-width: 100%;
  max-height: 120px;
  border-radius: 4px;
  box-shadow: 0 2px 4px rgba(0,0,0,0.1);
}
.empty-preview {
  color: #94a3b8;
  font-size: 13px;
  text-align: center;
  padding: 24px;
  background: #f8fafc;
  border-radius: 8px;
  border: 1px dashed #e2e8f0;
}

/* Images & Canvas Adjustments */
.final-preview-wrap :deep(.match-preview),
.final-preview-wrap :deep(.match-canvas) {
  max-height: 450px;
  border-radius: 8px;
  overflow: hidden;
  box-shadow: 0 4px 6px -1px rgba(0,0,0,0.1);
}
.final-preview-wrap :deep(img) {
  width: auto;
  max-width: 100%;
  max-height: 450px;
  object-fit: contain;
}

/* Buttons */
.run-btn {
  height: 44px;
  font-size: 16px;
  font-weight: 500;
  letter-spacing: 1px;
}
.test-btn {
  height: 40px;
  font-size: 14px;
  font-weight: 500;
}
</style>
