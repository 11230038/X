/**
 * 题库。
 *
 * 导入是两步：同一段原文先 `dry_run=true` 拿逐行结果，人确认后再提交。
 * 请求体是 **JSON Lines 纯文本**，不是 JSON 对象 —— 所以走 `rawBody`。
 */

import { request } from './client'

export type QuestionType = 'choice' | 'fill_blank' | 'short_answer'

export interface QuestionOption {
  key: string
  text: string
}

export interface Question {
  question_id: string
  text: string
  question_type: QuestionType
  options: QuestionOption[]
  correct_answer?: string
  explanation?: string
  difficulty?: string
  source?: string
  material_id?: string
  /**
   * 掌握路径出题时记下的坐标。两者要么都有、要么都没有；别的来源（知识库、
   * 附件、导入）没有这两个字段。
   */
  mastery_path_id?: string
  knowledge_point_id?: string
  knowledge_points?: string[]
  categories: string[]
}

export interface QuestionAnswer {
  user_answer?: string
  /** null = 从未作答。 */
  is_correct: boolean | null
  result?: 'correct' | 'wrong' | 'mastered' | 'needs_review' | 'skipped'
  resolved: boolean | null
  attempt_count: number
  bookmarked: boolean
  updated_at: string
  /** null = 从未复习（不是"现在到期"）。 */
  due_at: string | null
}

export interface QuestionEntry {
  question: Question
  answer: QuestionAnswer
  created_at: string
}

export interface QuestionList {
  questions: QuestionEntry[]
  limit: number
  offset: number
}

export interface QuestionCategory {
  name: string
  count: number
}

export interface ListQuestionsOptions {
  q?: string
  source?: string
  material_id?: string
  /** 只列出这条掌握路径出的题。 */
  mastery_path_id?: string
  difficulty?: string
  category?: string
  question_type?: QuestionType
  resolved?: '' | 'untouched' | 'open' | 'done'
  bookmarked?: boolean
  limit?: number
  offset?: number
}

export function listQuestions(options: ListQuestionsOptions = {}): Promise<QuestionList> {
  return request<QuestionList>('/questions', {
    query: {
      q: options.q,
      source: options.source,
      material_id: options.material_id,
      mastery_path_id: options.mastery_path_id,
      difficulty: options.difficulty,
      category: options.category,
      question_type: options.question_type,
      resolved: options.resolved,
      bookmarked: options.bookmarked,
      limit: options.limit,
      offset: options.offset,
    },
  })
}

export function getQuestion(questionId: string): Promise<QuestionEntry> {
  return request<QuestionEntry>(`/questions/${encodeURIComponent(questionId)}`)
}

export interface PatchQuestion {
  /** 不传 = 不改；`false` 是一个真实的改动。 */
  bookmarked?: boolean
  resolved?: boolean
  /** `[]` 表示清空分类；不传表示不改。 */
  categories?: string[]
}

export function patchQuestion(questionId: string, patch: PatchQuestion): Promise<QuestionEntry> {
  return request<QuestionEntry>(`/questions/${encodeURIComponent(questionId)}`, {
    method: 'PATCH',
    body: patch,
  })
}

export function listQuestionCategories(): Promise<{ categories: QuestionCategory[] }> {
  return request<{ categories: QuestionCategory[] }>('/question-categories')
}

export type ImportOutcome = 'writable' | 'missing_answer' | 'duplicate' | 'invalid'

export interface ImportEntry {
  line: number
  outcome: ImportOutcome
  question_id: string
  conflict: string
  conflicting_question_id: string
  conflicting_line: number
  reason: string
}

export interface ImportReport {
  dry_run: boolean
  lines: number
  parsed: number
  writable: number
  missing_answer: number
  duplicate: number
  invalid: number
  entries: ImportEntry[]
}

/** `text` 原样上传；`dry_run` 为 true 时只预览、不写入。 */
export function importQuestions(text: string, dryRun: boolean): Promise<ImportReport> {
  return request<ImportReport>('/questions/import', {
    method: 'POST',
    rawBody: { text, contentType: 'text/plain; charset=utf-8' },
    query: { dry_run: dryRun },
  })
}
