/**
 * 文档卡左侧的类型牌。
 *
 * 服务端只给了文件名，没有文件大小，也没有独立的类型字段；类型牌就取扩展名，
 * 不猜内容。扩展名最多截四个字母——牌面是 48px 的方块，再长就放不下了。
 */

export type DocumentKind = 'pdf' | 'doc' | 'ppt' | 'sheet' | 'markdown' | 'text' | 'other'

/** 同一类文件给同一个颜色，牌子只区分"这是哪种东西"。 */
const KINDS: Record<string, DocumentKind> = {
  pdf: 'pdf',
  doc: 'doc',
  docx: 'doc',
  odt: 'doc',
  rtf: 'doc',
  ppt: 'ppt',
  pptx: 'ppt',
  odp: 'ppt',
  xls: 'sheet',
  xlsx: 'sheet',
  csv: 'sheet',
  ods: 'sheet',
  md: 'markdown',
  markdown: 'markdown',
  txt: 'text',
  text: 'text',
  log: 'text',
}

/** 截四个字母会变成一个词的，单独给名字。 */
const LABELS: Record<string, string> = {
  markdown: 'MD',
}

export interface DocumentBadge {
  label: string
  kind: DocumentKind
}

export function documentBadge(filename: string): DocumentBadge {
  const match = /\.([^./\\]+)$/.exec(filename)
  const extension = (match?.[1] ?? '').toLowerCase()
  return {
    label: LABELS[extension] ?? (extension === '' ? 'FILE' : extension.slice(0, 4).toUpperCase()),
    kind: KINDS[extension] ?? 'other',
  }
}
