/**
 * 路径对话的两个入口：开一段新的，或回到某条路径自己的那段。
 *
 * 它们放在这里是因为三个页面要同一件事——掌握路径与学习地图的「去路径对话
 * 新建」、「下一步：学」。两条路都要先问后端，答案都在既有的接口里，不为它们
 * 新增一条：
 *
 *   - 新的那段是 `POST /sessions?kind=mastery`；路径不由前端建，第一句落进去
 *     时后端才建（`mastery.Open`）。
 *   - 旧的那段在 `GET /mastery/conversations` 里按 `path_id` 找——一条路径与
 *     创建它的那段对话一对一（`mastery_paths.owner_session_id`）。
 *
 * 返回的是要显示在页面上的那句话（成功为 `null`），不是抛出去的错误：失败
 * 必须留在发生的地方，带编号，而不是一条自行消失的浮层（`shared/toast.ts`）。
 */

import type { Router } from 'vue-router'

import { describeFailure } from '../api/errors'
import { isApiFailure } from '../api/client'
import { listMasteryConversations } from '../api/mastery'
import { createSession } from '../api/sessions'
import { MASTERY } from '../copy/mastery'

/** 一次只开一次：连点两下不该建出两段空对话。 */
let opening = false

function reason(cause: unknown, fallback: string): string {
  return isApiFailure(cause) ? describeFailure(cause.failure) : fallback
}

async function enter(router: Router, sessionID: string): Promise<void> {
  await router.push({ name: 'mastery-chat', params: { sessionId: sessionID } })
}

/** 开一段新的路径对话：建一条 `kind=mastery` 的会话，再进二级。 */
export async function openNewMasteryChat(router: Router): Promise<string | null> {
  if (opening) return null
  opening = true
  try {
    const created = await createSession('mastery')
    await enter(router, created.session_id)
    return null
  } catch (cause) {
    return reason(cause, MASTERY.chatCreateFailed)
  } finally {
    opening = false
  }
}

/** 回到这条路径自己的那段对话。 */
export async function openMasteryChat(router: Router, pathID: string): Promise<string | null> {
  if (opening || pathID === '') return null
  opening = true
  try {
    const { conversations } = await listMasteryConversations()
    const found = conversations.find((conversation) => conversation.path_id === pathID)
    if (!found) return MASTERY.chatOpenMissing
    await enter(router, found.session_id)
    return null
  } catch (cause) {
    return reason(cause, MASTERY.chatOpenFailed)
  } finally {
    opening = false
  }
}
