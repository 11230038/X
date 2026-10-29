/**
 * 权限相关的展示文案。
 *
 * 三个表分别回答三个不同的问题，**不要合成一个**：
 * - `SURFACE_LABELS`：这是哪个面；
 * - `LAYER_LABELS`：是**哪一层**拒的（§4.10：角色 / 预设 / 单独授权 / 地板 /
 *   监护，这四层不是一条等级线）；
 * - `REASON_LABELS`：为什么。
 *
 * 后端的 `reason` 是稳定码；这里查不到时回退到原码，不编一句话。
 */

export const SURFACE_LABELS: Record<string, string> = {
  chat: '对话',
  library: '资料市场',
  knowledge_base: '知识库',
  notebook: '题库',
  practice: '练习',
  mastery: '掌握路径',
  reading: '沉浸阅读',
  book: '活书',
  watching: '视频学习',
  dashboard: '学习首页',
  memory: '记忆',
  system: '系统设置',
  admin: '管理功能',
  guardian: '监护',
}

export const LAYER_LABELS: Record<string, string> = {
  role: '账号角色',
  /** 只作用于"自己的材料"这类天然归属项。 */
  self: '本人',
  preset: '预设',
  grant: '单独授权',
  floor: '不可关闭的底线',
  guardian: '监护关系',
}

export const REASON_LABELS: Record<string, string> = {
  administrator: '管理员身份允许',
  own_material: '这是你自己上传的材料',
  baseline_unrestricted: '预设没有限制这一项',
  baseline_allows: '预设允许这一项',
  baseline_closed: '预设没有开放这一项',
  grant_allows: '被单独授予了这一项',
  grant_denies: '被单独关闭了这一项',
  floor_protected: '这是底线项，不能被关闭',
  learner_never: '学习者账号永远不开放这一项',
  no_relationship: '没有建立监护关系',
  permission_missing: '这个关系没有这项权限',
  permission_held: '这个关系持有这项权限',
  unknown_item: '不认识这一项',
  not_administrator: '只有管理员可以',
}

export const PERMISSION_KIND_LABELS: Record<string, string> = {
  model: '模型',
  capability: '能力',
  surface: '导航面',
  kb: '知识库',
  tool: '工具',
  exec: '命令执行',
  learning_policy: '学习策略',
}

/** 权限快照里的 14 个面，顺序照后端 `authz` 的常量。 */
export const SURFACES = [
  'chat',
  'library',
  'knowledge_base',
  'notebook',
  'practice',
  'mastery',
  'reading',
  'book',
  'watching',
  'dashboard',
  'memory',
  'system',
  'admin',
  'guardian',
] as const

export type Surface = (typeof SURFACES)[number]
