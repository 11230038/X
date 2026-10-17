/**
 * 通用状态与动作的文案。
 *
 * 全部中文集中在这里，页面里不写死句子：将来接 i18n 时换的是这一层，不是
 * 每个 `.vue`。`设计说明书.md` §7.1：**空状态给动作，不给解释** —— 所以这里
 * 的动作词都比说明句多。
 */

export const ACTIONS = {
  retry: '重试',
  back: '返回',
  cancel: '取消',
  confirm: '确认',
  save: '保存',
  saving: '正在保存…',
  create: '新建',
  rename: '重命名',
  remove: '删除',
  restore: '恢复',
  purge: '永久删除',
  close: '关闭',
  clearFilters: '清除筛选',
  loadMore: '加载更多',
  loadEarlier: '加载更早',
  refresh: '重新加载',
  copy: '复制',
  copied: '已复制',
} as const

export const STATES = {
  loading: '正在加载…',
  saving: '正在保存…',
  submitting: '正在提交…',
  working: '正在处理…',
  stillWorking: '仍在进行…',
  empty: '没有内容',
  failed: '加载失败',
  partial: '有一部分没有加载出来',
  stale: '更新失败，显示的是上次结果',
  permissionUnknown: '权限信息暂不可用，部分入口可能隐藏',
  quotaExhausted: '本期额度已用完',
  rateLimited: '请求太频繁',
  notFound: '页面不存在',
  requestId: '请求编号',
  exit: '出口',
} as const

/** 不能只靠颜色表达状态（§6.2 硬规则 3）：每个状态都有文字。 */
export const BADGES = {
  ready: '就绪',
  pending: '准备中',
  running: '进行中',
  failed: '失败',
  skipped: '已跳过',
  unknown: '未知',
  disabled: '已禁用',
  locked: '已锁定',
  planned: '规划中',
} as const
