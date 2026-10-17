/**
 * 逐章生成。
 *
 * 一次 HTTP 请求生成一章，没有后台队列 —— 所以"生成整本"在这里就是**顺序
 * 发请求**，不是服务端的一个任务。这带来三条必须由界面守住的行为：
 *
 * 1. **首个失败就停。** 一次生成失败通常不是这一章特有的（没有模型、额度
 *    用完、限流），继续发只会把同样的话重复 N 遍。
 * 2. **离开页面不取消。** 请求还在服务端跑，结果仍会落库；文案不承诺"取消"，
 *    只提供"停止"，含义是"不再发下一章"。
 * 3. **每章结束都刷新一次书。** 大纲状态与已生成计数是服务端算的，本地改
 *    容易和它分叉；这本书很小，重读一次比手工合并便宜。
 */

import { ref } from 'vue'

import type { ApiFailure } from '../../../api/client'
import { generateBookChapter, type BookChapter } from '../../../api/books'
import { describeFailure } from '../../../api/errors'
import { BOOK_ERRORS, BOOKS } from '../../../copy/books'
import { toFailure } from '../../../shared/resource'

interface Options {
  /** 一章的正文已经拿到，交给页面放进缓存。 */
  onChapter: (chapter: BookChapter) => void
  /** 每一章结束后读一次书；刷新失败由页面自己的状态机处理。 */
  afterEach: () => Promise<void> | void
}

export function useBookGeneration(bookId: () => string, options: Options) {
  /** 有生成在跑（单章或批量）。 */
  const running = ref(false)
  /** 正在生成的那一章；0 表示没有。 */
  const activeOrdinal = ref(0)
  const batchTotal = ref(0)
  const batchDone = ref(0)
  const failure = ref<ApiFailure | null>(null)
  /** 会一直留在页面上的一句话：进度、停止、或失败的原因。 */
  const notice = ref('')

  let stopped = false

  async function attempt(ordinal: number): Promise<BookChapter | null> {
    failure.value = null
    try {
      const chapter = await generateBookChapter(bookId(), ordinal)
      options.onChapter(chapter)
      return chapter
    } catch (cause) {
      failure.value = toFailure(cause)
      return null
    } finally {
      await options.afterEach()
    }
  }

  function describe(failed: ApiFailure): string {
    return BOOK_ERRORS[failed.code] ?? describeFailure(failed)
  }

  function batchFailure(ordinal: number, done: number, total: number): string {
    const failed = failure.value
    if (!failed) return BOOKS.batchStopped(done, total, ordinal)
    if (failed.kind === 'quota') return BOOKS.batchQuota
    if (failed.kind === 'rate_limited') return BOOKS.batchRateLimited
    if (failed.code === 'generation_in_flight') return BOOKS.batchInFlight
    if (failed.code === 'outline_changed') return BOOKS.batchOutlineChanged
    return BOOKS.batchStopped(done, total, ordinal)
  }

  async function generateOne(ordinal: number): Promise<BookChapter | null> {
    if (running.value) return null
    running.value = true
    activeOrdinal.value = ordinal
    notice.value = ''
    try {
      const chapter = await attempt(ordinal)
      if (!chapter && failure.value) notice.value = describe(failure.value)
      return chapter
    } finally {
      running.value = false
      activeOrdinal.value = 0
    }
  }

  async function generateAll(ordinals: ReadonlyArray<number>): Promise<void> {
    if (running.value || ordinals.length === 0) return
    running.value = true
    stopped = false
    failure.value = null
    batchTotal.value = ordinals.length
    batchDone.value = 0

    let done = 0
    let current = 0
    try {
      for (const ordinal of ordinals) {
        if (stopped) break
        current = ordinal
        notice.value = BOOKS.generatingRemaining(done, ordinals.length)
        const chapter = await attempt(ordinal)
        if (!chapter) {
          notice.value = batchFailure(ordinal, done, ordinals.length)
          return
        }
        done += 1
        batchDone.value = done
      }
      if (stopped) {
        notice.value = BOOKS.batchPaused(done, ordinals.length)
      } else {
        notice.value =
          done === ordinals.length
            ? BOOKS.batchDone(ordinals.length)
            : BOOKS.batchStopped(done, ordinals.length, current)
      }
    } finally {
      running.value = false
      activeOrdinal.value = 0
    }
  }

  /** 不再发下一章。当前这一章的请求仍然会跑完并落库。 */
  function stop(): void {
    stopped = true
  }

  /** 换了一本书时清掉上一本的提示。 */
  function reset(): void {
    running.value = false
    activeOrdinal.value = 0
    batchTotal.value = 0
    batchDone.value = 0
    failure.value = null
    notice.value = ''
  }

  return {
    running,
    activeOrdinal,
    batchTotal,
    batchDone,
    failure,
    notice,
    generateOne,
    generateAll,
    stop,
    reset,
  }
}
