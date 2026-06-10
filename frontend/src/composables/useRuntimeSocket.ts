import { ref } from 'vue'

export function useRuntimeSocket(url: string) {
  const status = ref<'idle' | 'connecting' | 'open' | 'error'>('idle')
  const messages = ref<string[]>([])
  let socket: WebSocket | null = null

  function connect() {
    if (socket && (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING)) {
      return
    }
    status.value = 'connecting'
    socket = new WebSocket(url)
    socket.onopen = () => {
      status.value = 'open'
      messages.value.unshift('WebSocket 已连接')
    }
    socket.onerror = () => {
      status.value = 'error'
      messages.value.unshift('WebSocket 连接失败')
    }
    socket.onmessage = (event) => {
      messages.value.unshift(String(event.data))
    }
    socket.onclose = () => {
      if (status.value !== 'error') {
        status.value = 'idle'
      }
      socket = null
    }
  }

  return { status, messages, connect }
}
