<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

const props = defineProps<{
  imageUrl: string
  topLeft?: [number, number] | null
  width?: number | null
  height?: number | null
  label?: string
  boxes?: Array<{
    topLeft: [number, number]
    width: number
    height: number
    label?: string
    tone?: 'primary' | 'secondary'
  }>
}>()

const imageRef = ref<HTMLImageElement | null>(null)
const naturalSize = ref({ width: 0, height: 0 })
const displaySize = ref({ width: 0, height: 0 })
let resizeObserver: ResizeObserver | null = null

function updateDisplaySize() {
  if (!imageRef.value) return
  displaySize.value = {
    width: imageRef.value.clientWidth,
    height: imageRef.value.clientHeight,
  }
}

function handleImageLoad() {
  if (!imageRef.value) return
  naturalSize.value = {
    width: imageRef.value.naturalWidth,
    height: imageRef.value.naturalHeight,
  }
  updateDisplaySize()
}

const boxStyle = computed(() => {
  if (
    !props.topLeft ||
    !props.width ||
    !props.height ||
    !naturalSize.value.width ||
    !naturalSize.value.height ||
    !displaySize.value.width ||
    !displaySize.value.height
  ) {
    return null
  }
  return {
    left: `${(props.topLeft[0] / naturalSize.value.width) * displaySize.value.width}px`,
    top: `${(props.topLeft[1] / naturalSize.value.height) * displaySize.value.height}px`,
    width: `${(props.width / naturalSize.value.width) * displaySize.value.width}px`,
    height: `${(props.height / naturalSize.value.height) * displaySize.value.height}px`,
  }
})

const boxStyles = computed(() => {
  if (
    !props.boxes?.length ||
    !naturalSize.value.width ||
    !naturalSize.value.height ||
    !displaySize.value.width ||
    !displaySize.value.height
  ) {
    return []
  }
  return props.boxes.map((box) => ({
    label: box.label,
    tone: box.tone || 'primary',
    style: {
      left: `${(box.topLeft[0] / naturalSize.value.width) * displaySize.value.width}px`,
      top: `${(box.topLeft[1] / naturalSize.value.height) * displaySize.value.height}px`,
      width: `${(box.width / naturalSize.value.width) * displaySize.value.width}px`,
      height: `${(box.height / naturalSize.value.height) * displaySize.value.height}px`,
    },
  }))
})

onMounted(() => {
  if (typeof ResizeObserver !== 'undefined') {
    resizeObserver = new ResizeObserver(() => updateDisplaySize())
    if (imageRef.value) resizeObserver.observe(imageRef.value)
  }
})

onBeforeUnmount(() => {
  resizeObserver?.disconnect()
})
</script>

<template>
  <div class="match-preview">
    <div class="match-canvas">
      <img ref="imageRef" :src="imageUrl" alt="识别大图" @load="handleImageLoad" />
      <template v-if="boxStyles.length">
        <div
          v-for="(box, index) in boxStyles"
          :key="`${box.label || 'box'}-${index}`"
          class="match-box"
          :class="box.tone === 'secondary' ? 'match-box-secondary' : 'match-box-primary'"
          :style="box.style"
        >
          <span v-if="box.label" class="match-label">{{ box.label }}</span>
        </div>
      </template>
      <div v-else-if="boxStyle" class="match-box match-box-primary" :style="boxStyle">
        <span v-if="label" class="match-label">{{ label }}</span>
      </div>
    </div>
  </div>
</template>
