/** 导入导出两半共用的说法：作业状态、预览动作、对象类型。 */

import { SETTINGS } from '../../../copy/settings'

export const ACTION_LABELS: Record<string, string> = {
  create: '将新建',
  conflict: '已存在',
  missing: '缺少依赖',
}

export const REASON_LABELS: Record<string, string> = {
  already_present: '对象已经在工作区里',
  depends_on: '需要先导入它依赖的对象',
}

export const KIND_LABELS: Record<string, string> = {
  session: '会话',
  material: '材料',
  question: '题目',
  knowledge_base: '知识库',
}

export function kindLabel(kind: string): string {
  return KIND_LABELS[kind] ?? kind
}

export function jobStateLabel(state: string): string {
  switch (state) {
    case 'preview':
      return SETTINGS.jobPreview
    case 'running':
      return SETTINGS.jobRunning
    case 'committed':
      return SETTINGS.jobCommitted
    case 'failed':
      return SETTINGS.jobFailed
    case 'recovered':
      return SETTINGS.jobRecovered
    default:
      return state
  }
}
