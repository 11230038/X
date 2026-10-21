/**
 * 会话列表：`limit+offset` 分页、重命名、软删除。
 *
 * 当前会话被删掉时，调用方负责拆连接、清历史并回到对话首页 —— 这里只发
 * 一个回调，不替页面做决定。
 */

import { computed, ref } from 'vue'
import type {
  RouteLocationNormalizedLoaded,
  RouteLocationRaw,
  Router,
} from 'vue-router'

import { isApiFailure } from '../../api/client'
import { describeFailure } from '../../api/errors'
import {
  deleteSession,
  listSessions,
  renameSession,
  type SessionKind,
  type SessionSummary,
} from '../../api/sessions'
import { canSurface } from '../../auth/permissions'
import { CHAT } from '../../copy/chat'
import { confirmAction } from '../../shared/confirm'

const SESSION_PAGE = 50

function describe(cause: unknown, fallback: string): string {
  return isApiFailure(cause) ? describeFailure(cause.failure) : fallback
}

export function useChatSessions(deps: {
  route: RouteLocationNormalizedLoaded
  router: Router
  /** 这个页面只列哪一种会话。写作页传 `writing`，对话页传 `chat`。 */
  kind: SessionKind
  /** 当前会话被删掉、或者要离开当前会话时回到哪里。 */
  homeRoute: RouteLocationRaw
  /** 当前选中的会话被删掉了：页面负责拆连接、清历史与跳转。 */
  onActiveRemoved: () => Promise<void> | void
}) {
  const sessions = ref<SessionSummary[]>([])
  const sessionsLoading = ref(true)
  const sessionsLoadingMore = ref(false)
  const sessionsHasMore = ref(false)
  const sessionListError = ref<string | null>(null)

  const activeId = computed(() => {
    const id = deps.route.params.sessionId
    return typeof id === 'string' && id !== '' ? id : null
  })

  const activeSession = computed(
    () => sessions.value.find((session) => session.session_id === activeId.value) ?? null,
  )

  async function loadSessions(reset = true): Promise<void> {
    if (canSurface('chat') === false) {
      sessionsLoading.value = false
      return
    }
    if (reset) {
      sessionsLoading.value = true
    } else {
      sessionsLoadingMore.value = true
    }
    try {
      const result = await listSessions({
        kind: deps.kind,
        limit: SESSION_PAGE,
        offset: reset ? 0 : sessions.value.length,
      })
      const rows = result.sessions
      if (reset) {
        sessions.value = rows
      } else {
        const known = new Set(sessions.value.map((session) => session.session_id))
        sessions.value = [...sessions.value, ...rows.filter((row) => !known.has(row.session_id))]
      }
      sessionsHasMore.value = rows.length === SESSION_PAGE
      sessionListError.value = null
    } catch (cause) {
      sessionListError.value = describe(cause, '会话列表没能载入，请重试')
    } finally {
      sessionsLoading.value = false
      sessionsLoadingMore.value = false
    }
  }

  function patchSession(updated: SessionSummary): void {
    sessions.value = sessions.value.map((session) =>
      session.session_id === updated.session_id ? updated : session,
    )
  }

  function patchTitle(sessionId: string, title: string): void {
    sessions.value = sessions.value.map((session) =>
      session.session_id === sessionId ? { ...session, title } : session,
    )
  }

  async function onRename(sessionId: string, title: string): Promise<void> {
    try {
      const updated = await renameSession(sessionId, title)
      patchSession(updated)
      sessionListError.value = null
    } catch (cause) {
      sessionListError.value = describe(cause, CHAT.failedToRename)
    }
  }

  async function onRemove(session: SessionSummary): Promise<void> {
    const confirmed = await confirmAction({
      title: CHAT.removeTitle,
      body: CHAT.removeBody,
      note: CHAT.removeNote,
      confirmLabel: CHAT.removeConfirm,
      danger: true,
    })
    if (!confirmed) return

    try {
      await deleteSession(session.session_id)
      sessions.value = sessions.value.filter((row) => row.session_id !== session.session_id)
      sessionListError.value = null
      if (activeId.value === session.session_id) {
        await deps.onActiveRemoved()
        await deps.router.push(deps.homeRoute)
      }
      void loadSessions(true)
    } catch (cause) {
      sessionListError.value = describe(cause, CHAT.failedToRemove)
    }
  }

  return {
    sessions,
    sessionsLoading,
    sessionsLoadingMore,
    sessionsHasMore,
    sessionListError,
    activeId,
    activeSession,
    loadSessions,
    patchTitle,
    onRename,
    onRemove,
  }
}
