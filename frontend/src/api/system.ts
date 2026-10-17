/**
 * 运行状态与版本。
 *
 * `unfinished_calls` 在**读不到**时是缺省的，不是 0 —— 所以类型是可选，界面
 * 也不要把"没有这个字段"画成"0 个"。
 */

import { request } from './client'

export interface SystemVersion {
  version: string
  go_version: string
  migration_version: number
  build_time?: string
}

export interface SystemCheck {
  name: string
  ok: boolean
}

export interface SystemStatus {
  env: string
  checks: SystemCheck[]
  active_turns: number
  indexing: boolean
  unfinished_calls?: number
}

export function getVersion(): Promise<SystemVersion> {
  return request<SystemVersion>('/system/version')
}

export function getStatus(): Promise<SystemStatus> {
  return request<SystemStatus>('/system/status')
}
