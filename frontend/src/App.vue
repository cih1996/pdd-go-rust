<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { getRuntimeSummary } from './api'
import { useRuntimeSocket } from './composables/useRuntimeSocket'
import type { RuntimeSummary } from './types'

const {
  status: socketStatus,
  messages,
  connect: connectSocket,
} = useRuntimeSocket('ws://127.0.0.1:8080/ws/events')

const runtimeSummary = ref<RuntimeSummary | null>(null)
const loading = ref(false)
const loadError = ref('')
const summary = computed<RuntimeSummary | null>(() => runtimeSummary.value)

async function loadSummary() {
  loading.value = true
  loadError.value = ''
  try {
    runtimeSummary.value = await getRuntimeSummary()
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : '加载运行时概览失败'
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void loadSummary()
  connectSocket()
})
</script>

<template>
  <main class="page-shell">
    <section class="hero-card">
      <div>
        <p class="eyebrow">PDD 新架构骨架</p>
        <h1>Go 业务端 + Rust 适配器 + Vue 网页前端</h1>
        <p class="hero-text">
          这一套目录用于替代原来的 workspace + opencv-server + ocr-server + electron。
          最终方向是把 OpenCV/OCR 内置到 Go 主业务进程里，避免本地 API 传图损耗。
        </p>
      </div>
      <div class="hero-actions">
        <button class="primary" :disabled="loading" @click="loadSummary">
          {{ loading ? '刷新中...' : '刷新运行概览' }}
        </button>
        <span class="socket-pill" :data-status="socketStatus">{{ socketStatus }}</span>
      </div>
      <p v-if="loadError" class="error-text">{{ loadError }}</p>
    </section>

    <section class="grid">
      <article class="panel">
        <h2>服务划分</h2>
        <ul class="plain-list">
          <li><strong>frontend</strong>：Vue 3 + Vite，纯网页端，不再用 Electron。</li>
          <li><strong>unified-server</strong>：Go 主业务服务，后续承接设备控制、模板匹配、调试台、任务执行。</li>
          <li><strong>adapter-rs</strong>：Rust Axum 适配器，独立隔离上游任务协议。</li>
        </ul>
      </article>

      <article class="panel">
        <h2>视觉策略</h2>
        <ul class="plain-list">
          <li>OpenCV 和 OCR 最终都放进 Go 进程内。</li>
          <li>没有 OCR 模板时，不触发 OCR 识别。</li>
          <li>存在 OCR 模板时，每轮截图只做 1 次 OCR，再复用结果匹配全部模板。</li>
          <li>OCR 模板支持 <code>店铺优惠&立即支付</code> 这种多条件同时命中。</li>
        </ul>
      </article>

      <article class="panel">
        <h2>运行概览</h2>
        <div v-if="summary" class="summary-stack">
          <div class="summary-row"><span>适配器地址</span><code>{{ summary.adapter_base_url }}</code></div>
          <div class="summary-row"><span>模板总数</span><strong>{{ summary.template_count }}</strong></div>
          <div class="summary-row"><span>OCR 模板</span><strong>{{ summary.ocr_templates }}</strong></div>
          <div class="summary-row"><span>OpenCV 模板</span><strong>{{ summary.opencv_templates }}</strong></div>
          <div class="summary-row"><span>视觉模式</span><strong>{{ summary.vision_mode }}</strong></div>
        </div>
        <p v-else class="muted-text">Go 服务尚未启动时，这里会显示连接失败。</p>
      </article>

      <article class="panel">
        <h2>WebSocket 占位</h2>
        <p class="muted-text">
          Vue 前端后续通过 WebSocket 接收设备进度、调试状态、模板命中和日志推送。
        </p>
        <ul class="message-list">
          <li v-for="(item, index) in messages.slice(0, 6)" :key="index">{{ item }}</li>
          <li v-if="messages.length === 0" class="muted-text">暂无消息</li>
        </ul>
      </article>
    </section>
  </main>
</template>
