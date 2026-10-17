/**
 * 人格。
 *
 * 一个人格是一段**内容**，不是一种权限：它只决定这一轮的语气与回答方式。
 * 所以这里的五个调用都只关乎"我的提示词"，没有授权、分享或启停。
 *
 * 列表、单条之外的三个写入都直接返回那一条，不再包一层 —— 那正是表单保存
 * 之后要拿来就地更新列表的东西。
 */

import { request } from './client'

export interface Persona {
  persona_id: string
  name: string
  description: string
  content: string
  created_at: string
  updated_at: string
}

export interface PersonaInput {
  name: string
  description: string
  content: string
}

export function listPersonas(): Promise<{ personas: Persona[] }> {
  return request<{ personas: Persona[] }>('/personas')
}

export function getPersona(personaId: string): Promise<Persona> {
  return request<Persona>(`/personas/${encodeURIComponent(personaId)}`)
}

export function createPersona(input: PersonaInput): Promise<Persona> {
  return request<Persona>('/personas', { method: 'POST', body: input })
}

export function updatePersona(personaId: string, input: PersonaInput): Promise<Persona> {
  return request<Persona>(`/personas/${encodeURIComponent(personaId)}`, {
    method: 'PUT',
    body: input,
  })
}

export function deletePersona(personaId: string): Promise<void> {
  return request<void>(`/personas/${encodeURIComponent(personaId)}`, { method: 'DELETE' })
}
