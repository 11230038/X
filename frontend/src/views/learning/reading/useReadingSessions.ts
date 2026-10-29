/**
 * 阅读会话。
 *
 * 批注与阅读位置都必须挂在一条会话上（后端强制）。页面不再让人挑会话：
 * 从对话进入时用 URL 上那条，从导航直接进入时用最近的一条。两条都没有时，
 * 写操作会说明原因 —— 不偷偷写一条无主的批注。
 */

import { ref } from 'vue'
import type { RouteLocationNormalizedLoaded } from 'vue-router'

import { listSessions } from '../../../api/sessions'
import { READING } from '../../../copy/reading'
import { notify } from '../../../shared/toast'

export function useReadingSessions(route: RouteLocationNormalizedLoaded) {
  const sessionId = ref(typeof route.query.session === 'string' ? route.query.session : '')

  async function loadSessions(): Promise<void> {
    try {
      const loaded = await listSessions({ limit: 50 })
      // 没有指定会话时落到最近一条：批注需要一个落点，而用户没有在页面上
      // 选过任何东西。列表按更新时间倒序，所以第一条就是最近用过的。
      if (sessionId.value === '' && loaded.sessions.length > 0) {
        sessionId.value = loaded.sessions[0].session_id
      }
    } catch {
      // 读不出来就保持没有会话：写操作会提示先建一个，正文照旧能读。
    }
  }

  function needSession(): boolean {
    if (sessionId.value !== '') return true
    notify(READING.sessionRequired, 'error')
    return false
  }

  function setSession(next: string): void {
    sessionId.value = next
  }

  return { sessionId, loadSessions, needSession, setSession }
}
