/**
 * 轻提示。
 *
 * 只用于"动作已经完成"的反馈（已复制、已恢复、已撤回）。失败不用 toast：
 * 失败必须留在发生的地方，带重试与 `request_id`，一条几秒后消失的浮层是
 * 对失败的不尊重，也让人来不及抄编号。
 */

import { ref } from 'vue'

export type ToastTone = 'info' | 'success' | 'error'

export interface Toast {
  id: number
  text: string
  tone: ToastTone
}

export const toasts = ref<Toast[]>([])

let nextId = 1

export function notify(text: string, tone: ToastTone = 'info', ttlMs = 4000): number {
  const id = nextId++
  toasts.value = [...toasts.value, { id, text, tone }]
  window.setTimeout(() => dismissToast(id), ttlMs)
  return id
}

export function dismissToast(id: number): void {
  toasts.value = toasts.value.filter((toast) => toast.id !== id)
}
