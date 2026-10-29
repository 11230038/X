/**
 * 队列里"眼前这道题"。
 *
 * 练习页的主列（`Practice.vue`）和右侧面板是两个平级的 `AppShell` 插槽，
 * 面板读不到主列的队列状态。错题解析要的只是当前这道题，所以由主列在题目
 * 变化时写进来，面板直接读 —— 一个模块级的 `shallowRef`，与 `session.ts`
 * 同样的写法，不为一个字段引 pinia。
 *
 * 只读它、不改它：写入口只有一个 `setPracticeQuestion`，主列卸载时清空，
 * 面板不会拿着上一页的题继续解析。
 *
 * 旁边还有**这道题这一轮作答了没有**。错题解析里会说出正确答案，没作答就
 * 打开它等于先看答案再做；面板看不到主列的 `phase`，所以由主列在判分落下来
 * （或用户主动跳过）时写进来，换题或回到作答中都会重新锁上。
 */

import { shallowRef } from 'vue'

import type { QueueItem } from '../../../api/practice'

export const practiceQuestion = shallowRef<QueueItem | null>(null)

export function setPracticeQuestion(item: QueueItem | null): void {
  practiceQuestion.value = item
}

export const practiceAnswered = shallowRef(false)

export function setPracticeAnswered(answered: boolean): void {
  practiceAnswered.value = answered
}
