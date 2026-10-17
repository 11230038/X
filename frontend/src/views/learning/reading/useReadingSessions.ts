/**
 * 阅读会话。
 *
 * 批注与阅读位置都必须挂在一条会话上。从导航直接进来时没有会话，页面不会
 * 偷偷写一条无主的批注，而是先让用户选一条。
 */

import { computed, ref, type Ref } from 'vue'
import type { RouteLocationNormalizedLoaded, Router } from 'vue-router'

import type { ApiFailure } from '../../../api/client'
import { listSessions, type SessionSummary } from '../../../api/sessions'
import { toFailure } from '../../../shared/resource'

type Status = 'idle' | 'loading' | 'ready' | 'failed'

export function useReadingSessions(
  route: RouteLocationNormalizedLoaded,
  router: Router,
  currentId: Ref<string>,
  reloadOutline: () => Promise<void>,
) {
  const sessions = ref<SessionSummary[]>([])
  const sessionsStatus = ref<Status>('idle')
  const sessionsFailure = ref<ApiFailure | null>(null)
  const sessionId = ref(typeof route.query.session === 'string' ? route.query.session : '')
  const pendingSession = ref(sessionId.value)
  const sessionHint = ref(false)

  const sessionTitle = computed(() => {
    if (!sessionId.value) return ''
    const found = sessions.value.find((item) => item.session_id === sessionId.value)
    return found?.title ?? '当前阅读会话'
  })

  const sessionMissing = computed(
    () =>
      sessionId.value !== '' &&
      !sessions.value.some((item) => item.session_id === sessionId.value),
  )

  async function loadSessions(): Promise<void> {
    sessionsStatus.value = 'loading'
    sessionsFailure.value = null
    try {
      const loaded = await listSessions({ limit: 50 })
      sessions.value = loaded.sessions
      sessionsStatus.value = 'ready'
    } catch (cause) {
      sessionsFailure.value = toFailure(cause)
      sessionsStatus.value = 'failed'
    }
  }

  function applySession(): void {
    if (pendingSession.value === '' || pendingSession.value === sessionId.value) return
    sessionId.value = pendingSession.value
    sessionHint.value = false
    void router.replace({
      name: 'reading',
      query: {
        material: currentId.value,
        session: sessionId.value,
      },
    })
    void reloadOutline()
  }

  function clearSession(): void {
    sessionId.value = ''
    pendingSession.value = ''
    sessionHint.value = false
    void router.replace({ name: 'reading', query: { material: currentId.value } })
    void reloadOutline()
  }

  function needSession(): boolean {
    if (sessionId.value !== '') return true
    sessionHint.value = true
    return false
  }

  function setSession(next: string): void {
    sessionId.value = next
    pendingSession.value = next
  }

  return {
    sessions,
    sessionsStatus,
    sessionsFailure,
    sessionId,
    pendingSession,
    sessionHint,
    sessionTitle,
    sessionMissing,
    loadSessions,
    applySession,
    clearSession,
    needSession,
    setSession,
  }
}
