/**
 * 工作区导出 / 导入。
 *
 * 导出包是一条**没有信封**的裸 ZIP（带 `Content-Disposition`），所以走
 * `download()` 取 blob，再把 blob 交给浏览器 —— 不能直接把 URL 交给
 * `window.open`，因为凭据在 `Authorization` 头里。
 *
 * 导入是三步：上传 ZIP 预览 → 人确认 → 提交。失败的作业可以恢复，已提交
 * 的不行。
 */

import { download, request, upload } from './client'

export type WorkspaceKind = 'session' | 'material' | 'question' | 'knowledge_base'
export type WorkspaceJobKind = 'export' | 'import'
export type WorkspaceJobState = 'preview' | 'running' | 'committed' | 'failed' | 'recovered'

export interface WorkspaceRef {
  kind: WorkspaceKind
  id: string
}

export interface WorkspaceManifestEntry {
  kind: WorkspaceKind
  id: string
  sha256: string
  bytes: number
}

export interface WorkspaceWritten {
  kind: WorkspaceKind
  id: string
}

export interface WorkspaceJob {
  job_id: string
  kind: WorkspaceJobKind
  state: WorkspaceJobState
  archive_path?: string
  manifest?: WorkspaceManifestEntry[]
  written?: WorkspaceWritten[]
  counts?: Record<string, number>
  failure_code?: string
  created_at: string
  updated_at: string
}

export type WorkspacePreviewAction = 'create' | 'conflict' | 'missing'

export interface WorkspacePreviewItem {
  ref: WorkspaceRef
  action: WorkspacePreviewAction
  reason?: 'already_present' | 'depends_on'
  needs?: WorkspaceRef[]
}

export interface WorkspacePreview {
  job_id: string
  items: WorkspacePreviewItem[]
  objects: number
  actions: Record<string, number>
}

export function createExport(refs: WorkspaceRef[]): Promise<{ job: WorkspaceJob }> {
  return request<{ job: WorkspaceJob }>('/workspace/exports', {
    method: 'POST',
    body: { refs },
  })
}

export function listExports(limit?: number): Promise<{ jobs: WorkspaceJob[]; count: number }> {
  return request<{ jobs: WorkspaceJob[]; count: number }>('/workspace/exports', {
    query: { limit },
  })
}

export function downloadExport(jobId: string): Promise<{ blob: Blob; filename: string }> {
  return download(`/workspace/exports/${encodeURIComponent(jobId)}/content`)
}

/**
 * 这一步就是"上传导入包"。后端**没有**单独的 upload 路由：预览接口自己收
 * multipart，字段名固定为 `file`。
 */
export function previewImport(file: File): Promise<{ preview: WorkspacePreview }> {
  return upload<{ preview: WorkspacePreview }>('/workspace/imports/preview', file)
}

export function commitImport(jobId: string): Promise<{ job: WorkspaceJob }> {
  return request<{ job: WorkspaceJob }>('/workspace/imports', {
    method: 'POST',
    body: { job_id: jobId },
  })
}

export function getJob(jobId: string): Promise<{ job: WorkspaceJob }> {
  return request<{ job: WorkspaceJob }>(
    `/workspace/jobs/${encodeURIComponent(jobId)}`,
  )
}

export function recoverJob(jobId: string): Promise<{ job: WorkspaceJob }> {
  return request<{ job: WorkspaceJob }>(
    `/workspace/jobs/${encodeURIComponent(jobId)}/recover`,
    { method: 'POST' },
  )
}
