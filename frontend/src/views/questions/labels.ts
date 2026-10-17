/**
 * 题库的标签与判读辅助。列表、详情、导入三处共用同一套说法，避免同一道题在
 * 不同位置换一种写法。
 */

import type { QuestionEntry, QuestionType } from '../../api/questions'
import { QUESTIONS } from '../../copy/questions'

export const TYPE_LABELS: Record<QuestionType, string> = {
  choice: QUESTIONS.typeChoice,
  fill_blank: QUESTIONS.typeFillBlank,
  short_answer: QUESTIONS.typeShortAnswer,
}

export const RESULT_LABELS: Record<string, string> = {
  correct: '答对',
  wrong: '答错',
  mastered: '已掌握',
  needs_review: '需要复习',
  skipped: '已跳过',
}

export const OUTCOME_LABELS: Record<string, string> = {
  writable: QUESTIONS.outcomeWritable,
  missing_answer: QUESTIONS.outcomeMissingAnswer,
  duplicate: QUESTIONS.outcomeDuplicate,
  invalid: QUESTIONS.outcomeInvalid,
}

export function typeLabel(type: string): string {
  return TYPE_LABELS[type as QuestionType] ?? type
}

export function resultLabel(result: string | undefined): string {
  if (!result) return ''
  return RESULT_LABELS[result] ?? result
}

export function resolvedLabel(entry: QuestionEntry): string {
  if (entry.answer.resolved === true) return QUESTIONS.resolvedMastered
  if (entry.answer.resolved === false) return QUESTIONS.resolvedOpen
  return QUESTIONS.resolvedUntouched
}

export function resolvedTone(entry: QuestionEntry): 'success' | 'warning' | 'neutral' {
  if (entry.answer.resolved === true) return 'success'
  if (entry.answer.resolved === false) return 'warning'
  return 'neutral'
}

export function isCorrectOption(entry: QuestionEntry, key: string): boolean {
  return entry.question.correct_answer === key
}
