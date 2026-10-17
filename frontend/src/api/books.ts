/**
 * 活书。
 *
 * 四个接口是"读"，两个 POST 是"生成"，两个写是"存"：
 * - **生成接口没有请求体。** 要什么全在路径和书自己的行里，客户端不发一个
 *   空的 `{}`；
 * - **大纲的数组顺序就是章节顺序**，客户端不传 `ordinal`；
 * - **`source_text` 永远不在响应里**。它只用于生成，见 `book.Summary`。
 *
 * 章节正文不在 `BookDetail.outline` 里：大纲只有标题、摘要与状态，正文由
 * `getBookChapter` 单独读。这样书架与大纲接口不会把整本书拖进一次响应。
 */

import { request } from './client'

export type BookSourceKind = 'session' | 'knowledge_base' | 'file' | 'question_bank'

export type ChapterStatus = 'pending' | 'ready' | 'failed'

export interface BookSummary {
  book_id: string
  title: string
  source_kind: BookSourceKind
  source_id: string
  source_title: string
  source_chars: number
  source_truncated: boolean
  outline_confirmed: boolean
  outline_revision: number
  last_ordinal: number
  chapter_count: number
  ready_chapters: number
  created_at: string
  updated_at: string
}

export interface BookOutlineItem {
  ordinal: number
  title: string
  summary: string
  status: ChapterStatus
  error_code: string
}

export interface BookDetail extends BookSummary {
  outline: BookOutlineItem[]
}

export interface BookChapter {
  chapter_id: string
  book_id: string
  ordinal: number
  title: string
  summary: string
  /** 未生成时是空串；渲染前先看 `status`。 */
  content: string
  status: ChapterStatus
  error_code: string
  updated_at: string
}

export interface BookExport {
  filename: string
  content: string
}

export interface CreateBookInput {
  /** 留空时服务端用来源标题，再退回一个按来源种类写的名字。 */
  title: string
  source_kind: BookSourceKind
  source_id: string
}

export interface SaveOutlineInput {
  chapters: ReadonlyArray<{ title: string; summary: string }>
  confirmed: boolean
}

export function listBooks(): Promise<{ books: BookSummary[] }> {
  return request<{ books: BookSummary[] }>('/books')
}

export function createBook(input: CreateBookInput): Promise<BookDetail> {
  return request<BookDetail>('/books', { method: 'POST', body: input })
}

export function getBook(bookId: string): Promise<BookDetail> {
  return request<BookDetail>(`/books/${encodeURIComponent(bookId)}`)
}

export function deleteBook(bookId: string): Promise<void> {
  return request<void>(`/books/${encodeURIComponent(bookId)}`, { method: 'DELETE' })
}

/** 生成大纲需要一次模型调用，可能跑到两分钟；服务端会先存成未确认。 */
export function generateBookOutline(bookId: string): Promise<BookDetail> {
  return request<BookDetail>(`/books/${encodeURIComponent(bookId)}/outline`, {
    method: 'POST',
  })
}

export function saveBookOutline(
  bookId: string,
  input: SaveOutlineInput,
): Promise<BookDetail> {
  return request<BookDetail>(`/books/${encodeURIComponent(bookId)}/outline`, {
    method: 'PUT',
    body: input,
  })
}

/** 生成或重生成一章。同样没有请求体。 */
export function generateBookChapter(
  bookId: string,
  ordinal: number,
): Promise<BookChapter> {
  return request<BookChapter>(
    `/books/${encodeURIComponent(bookId)}/chapters/${ordinal}/generate`,
    { method: 'POST' },
  )
}

export function getBookChapter(bookId: string, ordinal: number): Promise<BookChapter> {
  return request<BookChapter>(
    `/books/${encodeURIComponent(bookId)}/chapters/${ordinal}`,
  )
}

/** `ordinal` 0 表示"还没开始"。返回更新后的书摘要。 */
export function saveBookProgress(
  bookId: string,
  ordinal: number,
): Promise<BookSummary> {
  return request<BookSummary>(`/books/${encodeURIComponent(bookId)}/progress`, {
    method: 'PUT',
    body: { ordinal },
  })
}

/** 只导出已经生成的章节；一章都没有时后端回 409 `no_ready_chapters`。 */
export function exportBook(bookId: string): Promise<BookExport> {
  return request<BookExport>(`/books/${encodeURIComponent(bookId)}/export`)
}
