/**
 * 沉浸阅读。
 *
 * **选区与引用是同一套坐标**：`Locator` 同时用于批注、位置、扩展和对话里的
 * `selection`。不要为"选中的一段"另造一种格式，否则引用深链与批注会各算
 * 各的偏移。
 *
 * 批注与位置都带 `session_id` —— 它们是"在这个会话里读到哪里、标了什么"，
 * 不是材料自身的属性。
 */

import { request } from './client'

export interface ReadingLocator {
  material_id: string
  ordinal: number
  char_start: number
  char_end: number
  page?: number
}

export interface ReadingMaterial {
  material_id: string
  content_id: string
  filename: string
  source_type?: string
  created_at: string
  updated_at: string
  pinned?: boolean
  opened?: boolean
}

export interface ReadingUnit {
  ordinal: number
  char_start: number
  char_end: number
  page?: number
}

export interface ReadingPosition {
  material_id: string
  session_id: string
  locator: ReadingLocator
  revision: number
  created_at: string
  updated_at: string
}

export interface ReadingOutline {
  material: ReadingMaterial
  units: ReadingUnit[]
  text_chars: number
  /** 只有在请求里带了 `session_id` 且存在阅读位置时才有。 */
  position?: ReadingPosition
}

export interface ReadingPassage {
  material_id: string
  locator: ReadingLocator
  text: string
}

export type MarkKind = 'bookmark' | 'highlight' | 'note'

export interface ReadingMark {
  mark_id: string
  material_id: string
  /** 写这条批注的会话被删掉后，这个字段就没了。 */
  session_id?: string
  kind: MarkKind
  locator: ReadingLocator
  body?: string
  revision: number
  created_at: string
  updated_at: string
}

export interface Pairing {
  pairing_id: string
  /** 以被问的那份材料为"主"。 */
  primary_material_id: string
  counterpart_material_id: string
  primary_language: string
  counterpart_language: string
  created_at: string
}

export interface Bilingual {
  material_id: string
  enabled: boolean
  reason?: string
  pairing?: Pairing
}

export interface PairingCandidate {
  material_id: string
  filename: string
  language: string
  text_chars: number
  score: number
}

export type ResultSchema = 'text' | 'cards' | 'timeline' | 'quiz' | 'audio_ref'

export interface ReadingCard {
  title: string
  body: string
}

export interface ReadingMoment {
  label: string
  detail: string
}

export interface ReadingQuizQuestion {
  question: string
  choices: string[]
}

export interface ReadingAudioRef {
  url: string
}

export type ExtensionResult =
  | string
  | ReadingCard[]
  | ReadingMoment[]
  | ReadingQuizQuestion[]
  | ReadingAudioRef

export interface ExtensionAvailability {
  name: string
  schema: ResultSchema
  available: boolean
  /** `available=false` 时非空。 */
  reason?: string
}

export interface ExtensionOutcome {
  name: string
  schema: ResultSchema
  result?: ExtensionResult
  skipped: boolean
  /** `skipped=true` 时非空。 */
  reason?: string
}

export function listReadingMaterials(
  workspaceId?: string,
): Promise<{ materials: ReadingMaterial[] }> {
  return request<{ materials: ReadingMaterial[] }>('/reading/materials', {
    query: { workspace_id: workspaceId },
  })
}

export function createReadingMaterial(
  fileId: string,
  sourceType: string,
): Promise<{ material: ReadingMaterial }> {
  return request<{ material: ReadingMaterial }>('/reading/materials', {
    method: 'POST',
    body: { file_id: fileId, source_type: sourceType },
  })
}

export function getReadingOutline(
  materialId: string,
  sessionId?: string,
): Promise<{ outline: ReadingOutline }> {
  return request<{ outline: ReadingOutline }>(
    `/reading/materials/${encodeURIComponent(materialId)}`,
    { query: { session_id: sessionId } },
  )
}

export function getReadingContent(
  materialId: string,
  range: { char_start?: number; char_end?: number; ordinal?: number },
): Promise<{ passage: ReadingPassage }> {
  return request<{ passage: ReadingPassage }>(
    `/reading/materials/${encodeURIComponent(materialId)}/content`,
    {
      query: {
        char_start: range.char_start,
        char_end: range.char_end,
        ordinal: range.ordinal,
      },
    },
  )
}

export function listMarks(materialId: string): Promise<{ marks: ReadingMark[] }> {
  return request<{ marks: ReadingMark[] }>(
    `/reading/materials/${encodeURIComponent(materialId)}/marks`,
  )
}

export function addMark(
  materialId: string,
  mark: { session_id: string; kind: MarkKind; locator: ReadingLocator; body: string },
): Promise<{ mark: ReadingMark }> {
  return request<{ mark: ReadingMark }>(
    `/reading/materials/${encodeURIComponent(materialId)}/marks`,
    { method: 'POST', body: mark },
  )
}

export function savePosition(
  materialId: string,
  position: { session_id: string; locator: ReadingLocator; revision: number },
): Promise<{ position: ReadingPosition }> {
  return request<{ position: ReadingPosition }>(
    `/reading/materials/${encodeURIComponent(materialId)}/position`,
    { method: 'POST', body: position },
  )
}

export function getPairings(
  materialId: string,
): Promise<{ bilingual: Bilingual; candidates: PairingCandidate[] }> {
  return request<{ bilingual: Bilingual; candidates: PairingCandidate[] }>(
    `/reading/materials/${encodeURIComponent(materialId)}/pairings`,
  )
}

export function confirmPairing(
  materialId: string,
  counterpartMaterialId: string,
): Promise<{ pairing: Pairing }> {
  return request<{ pairing: Pairing }>(
    `/reading/materials/${encodeURIComponent(materialId)}/pairings`,
    { method: 'POST', body: { counterpart_material_id: counterpartMaterialId } },
  )
}

export function cancelPairing(
  pairingId: string,
): Promise<{ pairing_id: string; cancelled: boolean }> {
  return request<{ pairing_id: string; cancelled: boolean }>(
    `/reading/pairings/${encodeURIComponent(pairingId)}`,
    { method: 'DELETE' },
  )
}

export function listExtensions(
  materialId: string,
): Promise<{ extensions: ExtensionAvailability[] }> {
  return request<{ extensions: ExtensionAvailability[] }>(
    `/reading/materials/${encodeURIComponent(materialId)}/extensions`,
  )
}

export function runExtension(
  materialId: string,
  name: string,
  locator: ReadingLocator,
): Promise<{ extension: ExtensionOutcome }> {
  return request<{ extension: ExtensionOutcome }>(
    `/reading/materials/${encodeURIComponent(materialId)}/extensions/${encodeURIComponent(name)}`,
    { method: 'POST', body: { locator } },
  )
}
