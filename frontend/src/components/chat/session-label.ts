/**
 * 一个会话在界面上叫什么。
 *
 * 独立成一个模块，是因为**两处必须给出同一个字符串**：列表栏的每一行，和内容
 * 区头部那一行。它们分别住在两个组件里，各写一份的话，新建会话那段时间里标题
 * 会在一处出现、在另一处不出现。
 *
 * 里面那条判断是设计决定，不是兜底：**新建的会话有一段没有标题的时间**（标题
 * 由服务端在第一轮回答结束之后才生成，§3.2），那时显示创建日期而不是"新会
 * 话"—— 后者会在标题到达时整行换掉，前者只是把标题补在前面。
 */

import type { SessionSummary } from '../../api/sessions'

export function sessionLabel(session: SessionSummary): string {
  if (session.title !== '') {
    return session.title
  }
  return formatTime(session.created_at)
}

export function formatTime(value: string): string {
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) {
    return value
  }
  return parsed.toLocaleString(undefined, {
    month: 'numeric',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}
