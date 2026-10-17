/**
 * 练习与复习。
 *
 * 判分有**两种性质**，界面必须分开说：`objective` 是判定结果，
 * `model_judgment` 是"AI 的判断"，带置信度、评分标准与边界。把后者画成客观
 * 对错是在制造一个不存在的权威。
 *
 * 判分失败（503 `judge_unavailable` / 422 `question_not_judgeable`）不是判分
 * 结果：作答要保留，`request_id` 要保留，重试原样复用。
 */

import { request } from './client'
import type { Question } from './questions'

export type Marker = '' | 'mastered' | 'needs_review' | 'skipped'

export interface QueueItem {
  question: Question
  answer: {
    user_answer?: string
    is_correct: boolean | null
    result?: string
    resolved: boolean | null
    attempt_count: number
    bookmarked: boolean
    updated_at: string
    due_at: string | null
  }
  reason: 'due' | 'new'
  is_mistake: boolean
  /** 从未复习过的题是 null —— 此时没有 version 可回传。 */
  state: ReviewState | null
}

export interface ReviewState {
  due_time: string
  streak: number
  lapses: number
  review_count: number
  ease: number
  is_mistake: boolean
  version: number
}

export interface Judgment {
  kind: 'objective' | 'model_judgment'
  is_correct: boolean
  reason: string
  rubric: string[]
  feedback: string
  suggestion: string
  /** 客观判定时为 null。 */
  confidence: number | null
  boundary: string
  model: string | null
}

export interface ReviewResult {
  request_id: string
  question_id: string
  /** true = 这次是幂等重放（同一 request_id 再次提交），仍是成功。 */
  replayed: boolean
  /** 跳过时没有判分。 */
  judgment: Judgment | null
  state: ReviewState
  entry: {
    is_correct: boolean | null
    attempt_count: number
    resolved: boolean | null
    result: string
  }
}

export interface SubmitReview {
  request_id: string
  question_id: string
  user_answer: string
  marker: Marker
  rating: number | null
  /** 有 `state.version` 时必须回传。 */
  version: number | null
}

export function getPracticeQueue(): Promise<{ queue: QueueItem[] }> {
  return request<{ queue: QueueItem[] }>('/practice/queue')
}

export function submitReview(review: SubmitReview): Promise<ReviewResult> {
  return request<ReviewResult>('/practice/reviews', { method: 'POST', body: review })
}

export function getReview(requestId: string): Promise<ReviewResult> {
  return request<ReviewResult>(
    `/practice/reviews/${encodeURIComponent(requestId)}`,
  )
}

export interface BucketStat {
  label: string
  reviews: number
  correct: number
  wrong: number
  added: number
  accuracy: number
}

export interface Distribution {
  name: string
  count: number
}

export interface PracticeStats {
  from: string
  to: string
  bucket: 'day' | 'week'
  series: BucketStat[]
  sources: Distribution[]
  categories: Distribution[]
}

export function getPracticeStats(options: {
  from?: string
  to?: string
  bucket?: 'day' | 'week'
} = {}): Promise<PracticeStats> {
  return request<PracticeStats>('/practice/stats', {
    query: { from: options.from, to: options.to, bucket: options.bucket },
  })
}
