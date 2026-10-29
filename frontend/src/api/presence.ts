/**
 * 在线时长与连续打卡。
 *
 * 服务端记的是"活过的分钟"：一次心跳写一行当前分钟，主键去重，所以同一分钟
 * 打十次也只有一行。今日时长就是今天有多少行，连续打卡就是往前数有多少个
 * 相邻的日期有行。
 *
 * 两个接口返回同一个形状：`POST` 是"这一分钟我在"并顺带回答，`GET` 只回答。
 * 页面打开时读一次，之后靠心跳刷新。
 */

import { request } from './client'

export interface Presence {
  /** 今天已经记下的分钟数。 */
  minutes: number
  /** 昨天一整天的分钟数，用来写"比昨天多学了 N 分钟"。 */
  yesterday_minutes: number
  /** 连续打卡的天数，今天算在内；今天还没有记录时从昨天往前数。 */
  streak_days: number
}

export function getPresence(): Promise<Presence> {
  return request<Presence>('/presence')
}

/** 记下当前这一分钟，并返回当天的最新统计。 */
export function touchPresence(): Promise<Presence> {
  return request<Presence>('/presence', { method: 'POST' })
}
