<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { CropRegion } from '../types'
import { ZoomIn, ZoomOut, FullScreen, Rank, Scissor } from '@element-plus/icons-vue'

const props = defineProps<{
  imageUrl: string
  activeMode: 'template' | 'search' | 'ocr'
  templateRect: CropRegion | null
  searchRect: CropRegion | null
  ocrRect: CropRegion | null
}>()

const emit = defineEmits<{
  (event: 'update:templateRect', value: CropRegion | null): void
  (event: 'update:searchRect', value: CropRegion | null): void
  (event: 'update:ocrRect', value: CropRegion | null): void
}>()

const imageRef = ref<HTMLImageElement | null>(null)
const boardRef = ref<HTMLElement | null>(null)
const imageLayerRef = ref<HTMLElement | null>(null)
const naturalSize = ref({ width: 0, height: 0 })
const scale = ref(1)
const mode = ref<'select' | 'pan'>('select')
const isSpaceDown = ref(false)

const draftRect = ref<CropRegion | null>(null)
const drawing = ref(false)
let startPoint = { x: 0, y: 0 }

let isPanning = false
let startPanX = 0
let startPanY = 0
let startScrollLeft = 0
let startScrollTop = 0

const displaySize = computed(() => {
  return {
    width: naturalSize.value.width * scale.value,
    height: naturalSize.value.height * scale.value,
  }
})

function fitScreen() {
  if (!boardRef.value || !naturalSize.value.width) return
  const padding = 24
  const boardWidth = boardRef.value.clientWidth - padding
  const boardHeight = boardRef.value.clientHeight - padding
  const scaleX = boardWidth / naturalSize.value.width
  const scaleY = boardHeight / naturalSize.value.height
  scale.value = Math.max(0.1, Math.min(scaleX, scaleY, 1))
}

function zoomIn() {
  scale.value = Math.min(scale.value * 1.2, 10)
}

function zoomOut() {
  scale.value = Math.max(scale.value / 1.2, 0.1)
}

function resetZoom() {
  scale.value = 1
}

function handleWheel(event: WheelEvent) {
  if (event.ctrlKey || event.metaKey) {
    event.preventDefault()
    const zoomFactor = event.deltaY > 0 ? 0.9 : 1.1
    const newScale = Math.max(0.1, Math.min(scale.value * zoomFactor, 10))
    
    if (boardRef.value && imageRef.value) {
      const box = imageRef.value.getBoundingClientRect()
      const pointerX = event.clientX - box.left
      const pointerY = event.clientY - box.top
      
      const oldWidth = box.width
      const oldHeight = box.height
      
      scale.value = newScale
      
      const newWidth = naturalSize.value.width * newScale
      const newHeight = naturalSize.value.height * newScale
      
      const dx = (newWidth - oldWidth) * (pointerX / oldWidth)
      const dy = (newHeight - oldHeight) * (pointerY / oldHeight)
      
      boardRef.value.scrollLeft += dx
      boardRef.value.scrollTop += dy
    } else {
      scale.value = newScale
    }
  }
}

function normalizeRect(rect: CropRegion): CropRegion | null {
  const width = Math.max(1, Math.round(rect.width ?? 0))
  const height = Math.max(1, Math.round(rect.height ?? 0))
  if (!naturalSize.value.width || !naturalSize.value.height || width <= 0 || height <= 0) return null
  return {
    x: Math.max(0, Math.round(rect.x)),
    y: Math.max(0, Math.round(rect.y)),
    width: Math.min(width, naturalSize.value.width - Math.round(rect.x)),
    height: Math.min(height, naturalSize.value.height - Math.round(rect.y)),
  }
}

function toNaturalRect(startX: number, startY: number, endX: number, endY: number): CropRegion | null {
  if (!displaySize.value.width || !displaySize.value.height || !naturalSize.value.width || !naturalSize.value.height) return null
  const left = Math.max(0, Math.min(startX, endX))
  const top = Math.max(0, Math.min(startY, endY))
  const right = Math.min(displaySize.value.width, Math.max(startX, endX))
  const bottom = Math.min(displaySize.value.height, Math.max(startY, endY))
  return normalizeRect({
    x: (left / displaySize.value.width) * naturalSize.value.width,
    y: (top / displaySize.value.height) * naturalSize.value.height,
    width: ((right - left) / displaySize.value.width) * naturalSize.value.width,
    height: ((bottom - top) / displaySize.value.height) * naturalSize.value.height,
  })
}

function toDisplayStyle(rect: CropRegion | null) {
  if (!rect || !displaySize.value.width || !displaySize.value.height || !naturalSize.value.width || !naturalSize.value.height) {
    return null
  }
  return {
    left: `${(rect.x / naturalSize.value.width) * displaySize.value.width}px`,
    top: `${(rect.y / naturalSize.value.height) * displaySize.value.height}px`,
    width: `${((rect.width ?? 0) / naturalSize.value.width) * displaySize.value.width}px`,
    height: `${((rect.height ?? 0) / naturalSize.value.height) * displaySize.value.height}px`,
  }
}

const templateStyle = computed(() => toDisplayStyle(props.templateRect))
const searchStyle = computed(() => toDisplayStyle(props.searchRect))
const ocrStyle = computed(() => toDisplayStyle(props.ocrRect))
const draftStyle = computed(() => toDisplayStyle(draftRect.value))

function pointerPosition(event: MouseEvent) {
  const box = imageLayerRef.value?.getBoundingClientRect()
  if (!box) return null
  return {
    x: Math.max(0, Math.min(event.clientX - box.left, box.width)),
    y: Math.max(0, Math.min(event.clientY - box.top, box.height)),
  }
}

function handlePointerDown(event: MouseEvent) {
  if (mode.value === 'pan' || isSpaceDown.value || event.button === 1 || event.button === 2) {
    isPanning = true
    startPanX = event.clientX
    startPanY = event.clientY
    startScrollLeft = boardRef.value?.scrollLeft || 0
    startScrollTop = boardRef.value?.scrollTop || 0
    document.body.style.cursor = 'grabbing'
    event.preventDefault()
    return
  }

  if (event.button === 0) {
    const point = pointerPosition(event)
    if (!point) return
    drawing.value = true
    startPoint = point
    draftRect.value = toNaturalRect(point.x, point.y, point.x, point.y)
  }
}

function handlePointerMove(event: MouseEvent) {
  if (isPanning) {
    if (boardRef.value) {
      boardRef.value.scrollLeft = startScrollLeft - (event.clientX - startPanX)
      boardRef.value.scrollTop = startScrollTop - (event.clientY - startPanY)
    }
    return
  }

  if (!drawing.value) return
  const point = pointerPosition(event)
  if (!point) return
  draftRect.value = toNaturalRect(startPoint.x, startPoint.y, point.x, point.y)
}

function finishSelection() {
  if (isPanning) {
    isPanning = false
    document.body.style.cursor = ''
    return
  }

  if (!drawing.value) return
  drawing.value = false
  if (props.activeMode === 'template') {
    emit('update:templateRect', draftRect.value)
  } else if (props.activeMode === 'ocr') {
    emit('update:ocrRect', draftRect.value)
  } else {
    emit('update:searchRect', draftRect.value)
  }
  draftRect.value = null
}

function handleImageLoad() {
  if (!imageRef.value) return
  naturalSize.value = {
    width: imageRef.value.naturalWidth,
    height: imageRef.value.naturalHeight,
  }
  fitScreen()
}

function handleKeyDown(e: KeyboardEvent) {
  if (e.code === 'Space' && document.activeElement?.tagName !== 'INPUT' && document.activeElement?.tagName !== 'TEXTAREA') {
    isSpaceDown.value = true
    e.preventDefault()
  }
}

function handleKeyUp(e: KeyboardEvent) {
  if (e.code === 'Space') {
    isSpaceDown.value = false
  }
}

watch(
  () => props.imageUrl,
  () => {
    draftRect.value = null
  },
)

onMounted(() => {
  window.addEventListener('mouseup', finishSelection)
  window.addEventListener('keydown', handleKeyDown)
  window.addEventListener('keyup', handleKeyUp)
})

onBeforeUnmount(() => {
  window.removeEventListener('mouseup', finishSelection)
  window.removeEventListener('keydown', handleKeyDown)
  window.removeEventListener('keyup', handleKeyUp)
})
</script>

<template>
  <div class="selection-canvas-container">
    <div class="canvas-toolbar">
      <el-radio-group v-model="mode" size="small">
        <el-radio-button value="select"><el-icon><Scissor /></el-icon> 框选</el-radio-button>
        <el-radio-button value="pan"><el-icon><Rank /></el-icon> 拖拽</el-radio-button>
      </el-radio-group>
      <el-divider direction="vertical" />
      <el-button-group size="small">
        <el-button @click="zoomOut"><el-icon><ZoomOut /></el-icon></el-button>
        <el-button @click="resetZoom">{{ Math.round(scale * 100) }}%</el-button>
        <el-button @click="zoomIn"><el-icon><ZoomIn /></el-icon></el-button>
        <el-button @click="fitScreen"><el-icon><FullScreen /></el-icon></el-button>
      </el-button-group>
    </div>

    <div 
      class="selection-board" 
      ref="boardRef"
      @wheel="handleWheel"
      @contextmenu.prevent
    >
      <div class="selection-stage">
        <div
          ref="imageLayerRef"
          class="selection-image-layer"
          :style="{ width: displaySize.width + 'px', height: displaySize.height + 'px' }"
          @mousedown.prevent="handlePointerDown"
          @mousemove.prevent="handlePointerMove"
        >
          <img
            ref="imageRef"
            :src="imageUrl"
            alt="调试截图"
            draggable="false"
            @load="handleImageLoad"
            :style="{ width: displaySize.width + 'px', height: displaySize.height + 'px' }"
          />
          <div v-if="templateStyle" class="selection-box template" :style="templateStyle">
            <span>小图区域</span>
          </div>
          <div v-if="searchStyle" class="selection-box search" :style="searchStyle">
            <span>查找区域</span>
          </div>
          <div v-if="ocrStyle" class="selection-box ocr" :style="ocrStyle">
            <span>OCR 区域</span>
          </div>
          <div v-if="draftStyle" class="selection-box draft" :style="draftStyle" />
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.selection-canvas-container {
  position: relative;
  display: flex;
  flex-direction: column;
  height: 100%;
}

.canvas-toolbar {
  position: absolute;
  top: 12px;
  right: 12px;
  z-index: 10;
  display: flex;
  align-items: center;
  background: rgba(255, 255, 255, 0.9);
  backdrop-filter: blur(4px);
  padding: 6px;
  border-radius: 8px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.1);
  border: 1px solid #e2e8f0;
}

.selection-board {
  width: 100%;
  height: 520px;
  overflow: auto;
  border: 1px solid #ebeef5;
  border-radius: 8px;
  background: #f5f7fa;
  position: relative;
}

.selection-stage {
  position: relative;
  display: flex;
  justify-content: center;
  align-items: flex-start;
  min-width: 100%;
  min-height: 100%;
}

.selection-image-layer {
  position: relative;
  flex: 0 0 auto;
}

.selection-stage img {
  display: block;
  cursor: crosshair;
  user-select: none;
  transform-origin: 0 0;
}

.selection-box {
  position: absolute;
  box-sizing: border-box;
  border: 2px solid transparent;
  pointer-events: none;
}

.selection-box span {
  position: absolute;
  left: -2px;
  top: -24px;
  padding: 2px 8px;
  border-radius: 4px;
  font-size: 12px;
  color: #fff;
  white-space: nowrap;
}

.selection-box.template {
  border-color: #409eff;
  background: rgba(64, 158, 255, 0.12);
}

.selection-box.template span {
  background: #409eff;
}

.selection-box.search {
  border-color: #67c23a;
  background: rgba(103, 194, 58, 0.12);
}

.selection-box.search span {
  background: #67c23a;
}

.selection-box.ocr {
  border-color: #e6a23c;
  background: rgba(230, 162, 60, 0.14);
}

.selection-box.ocr span {
  background: #e6a23c;
}

.selection-box.draft {
  border-color: #f56c6c;
  background: rgba(245, 108, 108, 0.1);
}
</style>
