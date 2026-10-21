/**
 * 知识库。
 *
 * 两件容易画错的事：
 * 1. `vector_state` 是**逐文档**的。全文检索在向量就绪前就能用，所以页面
 *    不能因为有一份 `pending` 就整体转圈；
 * 2. 重建也是**逐文档**的，没有全库重建接口。
 */

import { request } from './client'
import type { ChunkSpan } from './files'

export interface KnowledgeBase {
  kb_id: string
  name: string
  description?: string
  provider: string
  embedding_model: string
  is_default: boolean
  documents: number
  ready_documents: number
  created_at: string
  updated_at: string
}

export interface KbDocument {
  document_id: string
  kb_id?: string
  file_id: string
  filename: string
  engine: string
  text_chars: number
  truncated: boolean
  chunk_size: number
  chunk_overlap: number
  active_revision: number
  /** 全文可检索从这里开始；语义检索要等 `ready`。 */
  vector_state: 'pending' | 'running' | 'ready' | 'failed' | string
  vector_error?: string
  created_at: string
  updated_at: string
}

export interface KbDocumentTextView {
  document: KbDocument
  text: string
  chunks: ChunkSpan[]
  char_start: number
  char_end: number
  total_chars: number
}

export interface KbSource {
  index: number
  document_id: string
  file_id: string
  title: string
  engine?: string
  locator: {
    kind: string
    chunk_id?: string
    ordinal?: number
    char_start: number
    char_end: number
    page?: number | null
  }
  snippet: string
  score?: number
  revision: number
}

export function listKnowledgeBases(): Promise<{ knowledge_bases: KnowledgeBase[] }> {
  return request<{ knowledge_bases: KnowledgeBase[] }>('/knowledge-bases')
}

export function getKnowledgeBase(
  kbId: string,
): Promise<{ knowledge_base: KnowledgeBase; documents: KbDocument[] }> {
  return request<{ knowledge_base: KnowledgeBase; documents: KbDocument[] }>(
    `/knowledge-bases/${encodeURIComponent(kbId)}`,
  )
}

export function createKnowledgeBase(
  name: string,
  description: string,
): Promise<{ knowledge_base: KnowledgeBase }> {
  return request<{ knowledge_base: KnowledgeBase }>('/knowledge-bases', {
    method: 'POST',
    body: { name, description },
  })
}

export function deleteKnowledgeBase(kbId: string): Promise<void> {
  return request<void>(`/knowledge-bases/${encodeURIComponent(kbId)}`, {
    method: 'DELETE',
  })
}

/** 加入知识库会**同步解析**；向量化在后台继续。 */
export function addKnowledgeBaseDocument(
  kbId: string,
  fileId: string,
): Promise<{ document: KbDocument }> {
  return request<{ document: KbDocument }>(
    `/knowledge-bases/${encodeURIComponent(kbId)}/documents`,
    { method: 'POST', body: { file_id: fileId } },
  )
}

/**
 * 一份文档的抽取文本，一次一个窗口。
 *
 * 按 document 读而不是按 file 读：同一个文件可以被文件库和多个知识库各自
 * 解析成不同的 document，预览必须打开这一行自己的那一份。
 */
export function getKnowledgeDocumentText(
  kbId: string,
  documentId: string,
  options: { offset?: number; limit?: number } = {},
): Promise<KbDocumentTextView> {
  return request<KbDocumentTextView>(
    `/knowledge-bases/${encodeURIComponent(kbId)}/documents/${encodeURIComponent(documentId)}/text`,
    { query: { offset: options.offset, limit: options.limit } },
  )
}

export function removeKnowledgeBaseDocument(kbId: string, documentId: string): Promise<void> {
  return request<void>(
    `/knowledge-bases/${encodeURIComponent(kbId)}/documents/${encodeURIComponent(documentId)}`,
    { method: 'DELETE' },
  )
}

export function rebuildDocument(
  kbId: string,
  documentId: string,
): Promise<{ document: KbDocument }> {
  return request<{ document: KbDocument }>(
    `/knowledge-bases/${encodeURIComponent(kbId)}/rebuild`,
    { method: 'POST', body: { document_id: documentId } },
  )
}

export function searchKnowledgeBase(
  kbId: string,
  query: string,
  topK = 0,
): Promise<{ sources: KbSource[] }> {
  return request<{ sources: KbSource[] }>(
    `/knowledge-bases/${encodeURIComponent(kbId)}/search`,
    { method: 'POST', body: { query, top_k: topK } },
  )
}

export function setDefaultKnowledgeBase(
  kbId: string,
): Promise<{ knowledge_base: KnowledgeBase }> {
  return request<{ knowledge_base: KnowledgeBase }>(
    `/knowledge-bases/${encodeURIComponent(kbId)}/default`,
    { method: 'POST' },
  )
}
