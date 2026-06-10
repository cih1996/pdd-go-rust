import type { RuntimeSummary } from './types'

export async function getRuntimeSummary(): Promise<RuntimeSummary> {
  const response = await fetch('http://127.0.0.1:8080/api/runtime/summary')
  if (!response.ok) {
    throw new Error(`请求失败: ${response.status}`)
  }
  return response.json()
}
