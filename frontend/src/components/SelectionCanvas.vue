<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { CropRegion } from '../types'

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
const naturalSize = ref({ width: 0, height: 0 })
const displaySize = ref({ width: 0, height: 0 })
const draftRect = ref<CropRegion | null>(null)
const drawing = ref(false)
let startPoint = { x: 0, y: 0 }
let resizeObserver: ResizeObserver | null = null

function updateDisplaySize() {
  if (!imageRef.value) return
  displaySize.value = {
    width: imageRef.value.clientWidth,
    height: imageRef.value.clientHeight,
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
  const box = imageRef.value?.getBoundingClientRect()
  if (!box) return null
  return {
    x: Math.max(0, Math.min(event.clientX - box.left, box.width)),
    y: Math.max(0, Math.min(event.clientY - box.top, box.height)),
  }
}

function handlePointerDown(event: MouseEvent) {
  const point = pointerPosition(event)
  if (!point) return
  drawing.value = true
  startPoint = point
  draftRect.value = toNaturalRect(point.x, point.y, point.x, point.y)
}

function handlePointerMove(event: MouseEvent) {
  if (!drawing.value) return
  const point = pointerPosition(event)
  if (!point) return
  draftRect.value = toNaturalRect(startPoint.x, startPoint.y, point.x, point.y)
}

function finishSelection() {
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
  updateDisplaySize()
}

watch(
  () => props.imageUrl,
  () => {
    draftRect.value = null
  },
)

onMounted(() => {
  if (typeof ResizeObserver !== 'undefined') {
    resizeObserver = new ResizeObserver(() => updateDisplaySize())
    if (imageRef.value) resizeObserver.observe(imageRef.value)
  }
  window.addEventListener('mouseup', finishSelection)
})

onBeforeUnmount(() => {
  resizeObserver?.disconnect()
  window.removeEventListener('mouseup', finishSelection)
})
</script>

<template>
  <div class="selection-board">
    <div class="selection-stage">
      <img
        ref="imageRef"
        :src="imageUrl"
        alt="调试截图"
        draggable="false"
        @load="handleImageLoad"
        @mousedown.prevent="handlePointerDown"
        @mousemove.prevent="handlePointerMove"
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
</template>

<style scoped>
.selection-board {
  width: 100%;
  overflow: auto;
  padding: 12px;
  border: 1px solid #ebeef5;
  border-radius: 8px;
  background: #f5f7fa;
  text-align: center;
}

.selection-stage {
  position: relative;
  display: inline-block;
  max-width: 100%;
}

.selection-stage img {
  display: block;
  max-width: 100%;
  max-height: 520px;
  width: auto;
  height: auto;
  cursor: crosshair;
  user-select: none;
}

.selection-box {
  position: absolute;
  box-sizing: border-box;
  border: 2px solid transparent;
  pointer-events: none;
}

.selection-box span {
  position: absolute;
  left: 0;
  top: -24px;
  padding: 2px 8px;
  border-radius: 999px;
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
