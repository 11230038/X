/**
 * 资源加载的四种状态。
 *
 * 这是全站列表与详情共用的唯一状态机，规则来自《前端设计注意事项》§2：
 *
 * - **首次加载** → `loading`，画骨架屏；
 * - **空结果** → `ready` + 空数据，画带真实动作的空状态，**不是失败**；
 * - **首次失败** → `failed`，画错误 + 重试 + 出口；
 * - **刷新失败但已有旧数据** → 仍是 `ready`，`stale=true`，保留旧内容并在
 *   顶部挂一条部分失败条。绝不因为一次刷新失败就把人已经读到的内容换成
 *   骨架或错误页。
 *
 * 没有引 pinia，也没有 `useAsync` 之类的抽象：每个页面一个实例，生命周期
 * 跟着组件走。
 */

import { ref, shallowRef, type Ref, type ShallowRef } from 'vue'
import { isApiFailure, type ApiFailure } from '../api/client'

export type ResourceStatus = 'idle' | 'loading' | 'ready' | 'failed'

export interface Resource<T> {
  status: Ref<ResourceStatus>
  data: ShallowRef<T | null>
  failure: Ref<ApiFailure | null>
  /** 最近一次成功加载的时间戳；用于"更新于 …"这类说明。 */
  lastLoadedAt: Ref<number | null>
  /** 有旧数据但最近一次刷新失败了。 */
  stale: Ref<boolean>
  load: () => Promise<void>
  reset: () => void
}

/** 把任意 catch 到的东西归一成 `ApiFailure`，页面不必自己判类型。 */
export function toFailure(cause: unknown): ApiFailure {
  if (isApiFailure(cause)) return cause.failure
  if (cause instanceof Error) {
    return { kind: 'malformed', status: 0, code: '', message: cause.message }
  }
  return { kind: 'malformed', status: 0, code: '', message: '发生了未预期的错误' }
}

export function useResource<T>(loader: () => Promise<T>): Resource<T> {
  const status = ref<ResourceStatus>('idle')
  const data = shallowRef<T | null>(null)
  const failure = ref<ApiFailure | null>(null)
  const lastLoadedAt = ref<number | null>(null)
  const stale = ref(false)

  let inFlight: Promise<void> | null = null

  async function load(): Promise<void> {
    // 同一个资源同时只跑一次：路由参数快速切换时，晚到的旧响应不能覆盖新的。
    // 调用方若在切换后需要新结果，`reset()` 会先清掉 data 并解除这个锁。
    if (inFlight) return inFlight
    if (data.value === null) {
      status.value = 'loading'
    }
    failure.value = null

    const run = (async () => {
      try {
        const next = await loader()
        data.value = next
        failure.value = null
        stale.value = false
        lastLoadedAt.value = Date.now()
        status.value = 'ready'
      } catch (cause) {
        failure.value = toFailure(cause)
        if (data.value === null) {
          status.value = 'failed'
        } else {
          stale.value = true
          status.value = 'ready'
        }
      } finally {
        inFlight = null
      }
    })()

    inFlight = run
    return run
  }

  function reset(): void {
    status.value = 'idle'
    data.value = null
    failure.value = null
    lastLoadedAt.value = null
    stale.value = false
    inFlight = null
  }

  return { status, data, failure, lastLoadedAt, stale, load, reset }
}
