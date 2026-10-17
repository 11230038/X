/**
 * 文件库。
 *
 * `document: null` 是一个**正常状态**：文件已经存下来了，只是还没被解析
 * （没进过知识库）。界面画"已上传，尚未解析"，不是失败，也不是永远加载。
 */

import { download, request, upload, type UploadOptions } from './client'

export interface UploadedFile {
  id: string
  sha256: string
  filename: string
  mime_type: string
  size_bytes: number
  category: string
  created_at: string
}

export interface FileSummary extends UploadedFile {
  /** 活跃文件是 null；回收站里是 RFC3339。字段始终存在。 */
  deleted_at: string | null
}

export interface DocumentView {
  document_id: string
  /** 附件型文档没有知识库。 */
  kb_id?: string
  file_id: string
  filename: string
  engine: string
  text_chars: number
  truncated: boolean
  chunk_size: number
  chunk_overlap: number
  active_revision: number
  vector_state: 'pending' | 'running' | 'ready' | 'failed' | string
  vector_error?: string
  created_at: string
  updated_at: string
}

export interface ChunkSpan {
  ordinal: number
  char_start: number
  char_end: number
  page?: number
}

export interface FileView {
  file: UploadedFile
  /** 从没解析过就是 null。 */
  document: DocumentView | null
}

export interface FileTextView {
  document: DocumentView
  text: string
  chunks: ChunkSpan[]
  char_start: number
  char_end: number
  total_chars: number
}

export interface ListFilesOptions {
  filename?: string
  category?: string
  since?: string
  until?: string
  deleted?: boolean
  limit?: number
  offset?: number
}

export function listFiles(options: ListFilesOptions = {}): Promise<{ files: FileSummary[] }> {
  return request<{ files: FileSummary[] }>('/library/files', {
    query: {
      filename: options.filename,
      category: options.category,
      since: options.since,
      until: options.until,
      deleted: options.deleted,
      limit: options.limit,
      offset: options.offset,
    },
  })
}

/** 一次一个文件；multipart 字段名固定为 `file`。 */
export function uploadFile(
  file: File,
  options: UploadOptions = {},
): Promise<UploadedFile> {
  return upload<{ file: UploadedFile }>('/library/files', file, options).then(
    (result) => result.file,
  )
}

export function getFile(fileId: string): Promise<FileView> {
  return request<FileView>(`/library/files/${encodeURIComponent(fileId)}`)
}

export function getFileText(
  fileId: string,
  options: { offset?: number; limit?: number } = {},
): Promise<FileTextView> {
  return request<FileTextView>(`/library/files/${encodeURIComponent(fileId)}/text`, {
    query: { offset: options.offset, limit: options.limit },
  })
}

/** 下载原始文件（裸字节，没有信封）。 */
export function downloadFileContent(fileId: string): Promise<{ blob: Blob; filename: string }> {
  return download(`/library/files/${encodeURIComponent(fileId)}/content`)
}

/** 软删：200，返回带 `deleted_at` 的文件。 */
export function deleteFile(fileId: string): Promise<FileSummary> {
  return request<{ file: FileSummary }>(`/library/files/${encodeURIComponent(fileId)}`, {
    method: 'DELETE',
  }).then((result) => result.file)
}

export function restoreFile(fileId: string): Promise<FileSummary> {
  return request<{ file: FileSummary }>(
    `/library/files/${encodeURIComponent(fileId)}/restore`,
    { method: 'POST' },
  ).then((result) => result.file)
}

/**
 * 永久删除，204。被知识库引用时后端回 409 `file_in_use`，`details.references`
 * 里列出引用方 —— 页面要把它们列出来，而不是盲目重试。
 */
export function purgeFile(fileId: string): Promise<void> {
  return request<void>(`/library/files/${encodeURIComponent(fileId)}/purge`, {
    method: 'DELETE',
  })
}

export interface PurgeReference {
  Kind: string
  ID: string
  Name: string
}

/** 只有 409 `file_in_use` 的 details 是这个形状；其他失败返回空数组。 */
export function purgeReferences(details: unknown): PurgeReference[] {
  if (!details || typeof details !== 'object') return []
  const references = (details as { references?: unknown }).references
  if (!Array.isArray(references)) return []
  return references.filter(
    (item): item is PurgeReference =>
      !!item &&
      typeof item === 'object' &&
      typeof (item as PurgeReference).ID === 'string',
  )
}
