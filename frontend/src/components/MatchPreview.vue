<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

const props = defineProps<{
  imageUrl: string
  topLeft?: [number, number] | null
  width?: number | null
  height?: number | null
  label?: string
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
      <div v-if="boxStyle" class="match-box" :style="boxStyle">
        <span v-if="label" class="match-label">{{ label }}</span>
      </div>
    </div>
  </div>
</template>
