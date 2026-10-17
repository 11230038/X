/**
 * 后端授权策略的**镜像**，只用于管理台把"锁死的开关"画对。
 *
 * 这不是事实来源。真正的判定在 `backend/internal/authz`，前端拿到的快照
 * 才是事实。镜像存在的唯一理由：管理台要在**还没提交**之前就让管理员看出
 * "这一项不归你改"，而不是等他点完拿到 403。
 *
 * 三项与后端一一对应：
 * - 地板：`chat` 能力与 `chat` 面任何预设都关不掉；
 * - learner 永不开放：工具、命令执行、`mcp` / `cli` 两个面；
 * - 仅管理员：`admin` 面不可授予。
 *
 * 后端改了策略而这里没改，最坏结果是一个开关看起来能点、提交后被拒 ——
 * 不会越权。**不要把它反过来用**：不要用这里的常量去替代 `can()`。
 */

export const FLOOR_ITEMS: ReadonlyArray<{ kind: string; id: string }> = [
  { kind: 'capability', id: 'chat' },
  { kind: 'surface', id: 'chat' },
]

export const LEARNER_NEVER_ITEMS: ReadonlyArray<{ kind: string; id: string }> = [
  { kind: 'tool', id: '*' },
  { kind: 'exec', id: '*' },
  { kind: 'surface', id: 'mcp' },
  { kind: 'surface', id: 'cli' },
]

export const ADMIN_ONLY_ITEMS: ReadonlyArray<{ kind: string; id: string }> = [
  { kind: 'surface', id: 'admin' },
]

function matches(
  items: ReadonlyArray<{ kind: string; id: string }>,
  kind: string,
  id: string,
): boolean {
  return items.some((item) => item.kind === kind && (item.id === '*' || item.id === id))
}

export function isFloor(kind: string, id: string): boolean {
  return matches(FLOOR_ITEMS, kind, id)
}

export function isLearnerNever(kind: string, id: string): boolean {
  return matches(LEARNER_NEVER_ITEMS, kind, id)
}

export function isAdminOnly(kind: string, id: string): boolean {
  return matches(ADMIN_ONLY_ITEMS, kind, id)
}

/**
 * 一个授权项为什么改不了。`null` 表示可改。
 * 顺序有意义：地板先于 learner（两者不会同时命中，但读起来更清楚）。
 */
export function lockReason(
  kind: string,
  id: string,
  targetIsLearner: boolean,
): 'floor' | 'learner_never' | 'admin_only' | null {
  if (isFloor(kind, id)) return 'floor'
  if (targetIsLearner && isLearnerNever(kind, id)) return 'learner_never'
  if (isAdminOnly(kind, id)) return 'admin_only'
  return null
}
