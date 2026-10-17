/**
 * 危险动作的确认对话框。
 *
 * 一个模块级单例，`ConfirmDialog` 挂在根组件里只渲染一次；页面调用
 * `confirmAction()` 拿到一个 Promise<boolean>。不用 `window.confirm` 的原因
 * 不是美观：原生 confirm 写不下**影响范围**，而 `设计说明书.md` §7.3 要求
 * 每一次不可逆操作都必须把"会失去什么、能不能回来"写在按钮旁边。
 */

import { shallowRef } from 'vue'

export interface ConfirmOptions {
  title: string
  /** 影响范围。会原样显示在标题下面。 */
  body?: string
  /** 补充说明，例如"这个动作会记进审计"。 */
  note?: string
  confirmLabel?: string
  cancelLabel?: string
  /** 危险动作的确认按钮不用主色。 */
  danger?: boolean
}

export interface ConfirmRequest extends ConfirmOptions {
  resolve: (confirmed: boolean) => void
}

/** 当前待确认的请求；null 表示没有对话框。 */
export const pendingConfirm = shallowRef<ConfirmRequest | null>(null)

export function confirmAction(options: ConfirmOptions): Promise<boolean> {
  const previous = pendingConfirm.value
  if (previous) {
    // 上一张还没答就来了下一张：按"取消"结掉它，不然它的 await 永远挂着。
    previous.resolve(false)
  }
  return new Promise<boolean>((resolve) => {
    pendingConfirm.value = { ...options, resolve }
  })
}

/** 由 `ConfirmDialog` 调用。 */
export function settleConfirm(confirmed: boolean): void {
  const request = pendingConfirm.value
  pendingConfirm.value = null
  request?.resolve(confirmed)
}
