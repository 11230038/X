/**
 * 二级侧栏的内容表。照演示稿的 `renderSidebar(view)`：**每一页都有一条 240px
 * 的侧栏，内容随当前所在的空间换**，而不是把二级入口挂在图标栏上。
 *
 * 演示稿的四组是「快捷入口 / 历史会话 / 学习空间 / 掌握路径」，加上设置项与
 * 个人中心。这里保留同一套分组，只是把每一组里的条目换成后台真的有的页面：
 * 演示稿里的收藏夹、错题本、笔记本、资料市场没有接口，不画进来的入口。
 *
 * 会话列表与知识库列表自己就是侧栏（它们由页面通过 `#list` 插槽给出），所以
 * 这两类页面不在这里返回分组 —— 否则会出现两条并列的侧栏。
 */

import type { RouteLocationNormalized } from 'vue-router'

import { canSurface, isAdmin } from '../../auth/permissions'
import type { AppIconName } from '../common/AppIcon.vue'

/** 侧栏只需要看路由名与路径；收窄成这个形状便于在测试里直接构造。 */
type RouteRef = Pick<RouteLocationNormalized, 'name' | 'path'>

export interface SidebarItem {
  label: string
  /** 图标名，由 `AppIcon` 画（与一级图标栏同一套笔触与渐变）。 */
  icon: AppIconName
  to: string
  isActive: (route: RouteRef) => boolean
}

export interface SidebarSection {
  title: string
  items: SidebarItem[]
}

const byName =
  (...names: string[]): SidebarItem['isActive'] =>
  (route) =>
    names.includes(String(route.name ?? ''))

const byPath =
  (...prefixes: string[]): SidebarItem['isActive'] =>
  (route) =>
    prefixes.some((prefix) => route.path === prefix || route.path.startsWith(`${prefix}/`))

/* ── 快捷入口（学习首页） ─────────────────────────────────────────────── */

const LEARNING: SidebarSection = {
  title: '快捷入口',
  items: [
    { label: '最近对话', icon: 'chat', to: '/chat', isActive: byPath('/chat') },
    { label: '我的活书', icon: 'space', to: '/books', isActive: byPath('/books') },
    { label: '题库', icon: 'quiz', to: '/questions', isActive: byPath('/questions') },
    { label: '掌握路径', icon: 'mastery', to: '/mastery', isActive: byPath('/mastery') },
  ],
}

/* ── 学习空间（活书 / 知识库 / 阅读 / 题库 / 练习 / 视频 / 文件） ────── */

const SPACE: SidebarSection = {
  title: '学习空间',
  items: [
    { label: '我的活书', icon: 'space', to: '/books', isActive: byPath('/books') },
    { label: '知识库', icon: 'knowledge', to: '/knowledge-bases', isActive: byPath('/knowledge-bases', '/materials') },
    { label: '沉浸阅读', icon: 'reading', to: '/reading', isActive: byPath('/reading') },
    { label: '资料市场', icon: 'market', to: '/files', isActive: byPath('/files') },
    { label: '题库', icon: 'quiz', to: '/questions', isActive: byPath('/questions') },
    { label: '练习', icon: 'practice', to: '/practice', isActive: byPath('/practice') },
    { label: '视频学习', icon: 'video', to: '/watching', isActive: byPath('/watching') },
  ],
}

/* ── 掌握路径 ─────────────────────────────────────────────────────────── */

const MASTERY: SidebarSection = {
  title: '掌握路径',
  items: [
    { label: '路径总览', icon: 'mastery', to: '/mastery', isActive: byName('mastery') },
    {
      label: '路径对话',
      icon: 'chat',
      to: '/mastery/chat',
      // 一级是对话历史，二级是其中一段对话；两级都算「路径对话」。
      isActive: (route) => route.name === 'mastery-conversations' || route.name === 'mastery-chat',
    },
    { label: '学习地图', icon: 'map', to: '/learning-map', isActive: byName('learning-map') },
  ],
}

/* ── 设置 ─────────────────────────────────────────────────────────────── */

interface SettingsEntry {
  label: string
  to: string
  icon?: AppIconName
  surface?: string
}

/**
 * 设置侧栏先列设置页，最后才放"去处"（记忆、伙伴）。两者都**不换侧栏**：进去
 * 之后仍是这张设置列表，对应那条高亮 —— 记忆的子页（L1 / L2 / L3 / 归属分组图 /
 * 去重与清理）从工作台上的卡片进，不再在侧栏里另列一列菜单。这里只是入口，
 * 不是把那几页塞进 `/settings` 的子树。技能与工具相反，它们是 `/settings` 的
 * 子路由，所以直接列在设置分区里。
 *
 * 账号不在这张表里：它随头像搬到了个人中心（`/profile/account`）。用户管理
 * 与日志统计也不在这张表里：它们是个人中心的子页，入口在个人中心那一列。
 *
 * `SettingsLayout` 的"为什么有些设置看不到"也读这张表，所以导出。
 */
export const SETTINGS_ENTRIES: readonly SettingsEntry[] = [
  { label: '工作区', to: '/settings/workspace', icon: 'workspace', surface: 'system' },
  { label: '用量统计', to: '/settings/usage', icon: 'chart', surface: 'system' },
  { label: '运行状态', to: '/settings/system', icon: 'pulse', surface: 'system' },
  { label: '监护', to: '/settings/guardian', icon: 'heart', surface: 'guardian' },
  { label: '关于', to: '/settings/about', icon: 'info' },
  { label: '技能', to: '/settings/skills', icon: 'puzzle' },
  { label: '工具', to: '/settings/tools', icon: 'tool' },
  { label: '记忆', to: '/memory', icon: 'memory', surface: 'memory' },
  { label: '伙伴', to: '/partners', icon: 'partners' },
]

/**
 * 权限明确为 false 的分区整条隐藏；未知时照常出现，由子页自己处理。
 */
function settingsSection(): SidebarSection {
  return {
    title: '设置项',
    items: SETTINGS_ENTRIES.filter(
      (entry) => entry.surface === undefined || canSurface(entry.surface) !== false,
    ).map((entry) => ({
      label: entry.label,
      icon: entry.icon ?? 'settings',
      to: entry.to,
      // 按路径前缀判定：记忆那一项要覆盖 `/memory` 整棵子树，否则进到 L1 之后
      // 侧栏里没有任何一条是亮的。其余条目没有子页，前缀与精确判断等价。
      isActive: byPath(entry.to),
    })),
  }
}

/* ── 个人中心（头像进入） ─────────────────────────────────────────────── */

/**
 * 演示稿的个人中心侧栏是"个人资料 / 我的成就 / 学习报表"，后两项没有对应的
 * 页面与接口，不画。收藏夹挂在个人中心下面，所以它也在这一列里，而不是像
 * 演示稿那样另开一列"收藏分类"：打开收藏夹时这一列照旧在，当前项是它。
 *
 * 用户管理与日志统计是原来的「管理台」拆出来的两页，也挂在个人中心下。它们
 * 只对管理员出现：快照没到时不画，免得先出现再消失；`admin` 面被明确关掉时
 * 同样不画。页面里还有一层 `SurfaceGate`，这里只是入口。
 */
const PROFILE_ITEMS: SidebarItem[] = [
  { label: '个人资料', icon: 'user', to: '/profile', isActive: byName('profile') },
  { label: '收藏夹', icon: 'star', to: '/profile/favorites', isActive: byName('profile-favorites') },
  { label: '账号与安全', icon: 'shield', to: '/profile/account', isActive: byName('profile-account') },
]

const PROFILE_ADMIN_ITEMS: SidebarItem[] = [
  { label: '用户管理', icon: 'users', to: '/profile/users', isActive: byName('profile-users') },
  { label: '日志统计', icon: 'log', to: '/profile/logs', isActive: byName('profile-logs') },
]

function profileSection(): SidebarSection {
  const showAdmin = isAdmin.value && canSurface('admin') !== false
  return {
    title: '个人中心',
    items: showAdmin ? [...PROFILE_ITEMS, ...PROFILE_ADMIN_ITEMS] : PROFILE_ITEMS,
  }
}

const SPACE_NAMES = new Set([
  'books',
  'book',
  'knowledge-bases',
  'knowledge-base',
  'reading',
  'files',
  'questions',
  'question-chat',
  'practice',
  'watching',
])

/**
 * 当前路由对应哪一条二级侧栏。会话（`chat` / `conversation` / `co-writer` /
 * 回收站）返回 null：那几页的第二列由页面自己的会话列表占着。
 */
export function sectionFor(route: RouteRef): SidebarSection | null {
  const name = String(route.name ?? '')
  if (name === 'learning') return LEARNING
  if (SPACE_NAMES.has(name)) return SPACE
  if (
    name === 'mastery' ||
    name === 'mastery-conversations' ||
    name === 'mastery-chat' ||
    name === 'learning-map'
  ) {
    return MASTERY
  }
  // 记忆不换侧栏：设置那一列里的「记忆」就是它的入口，整棵 `/memory` 子树都
  // 停在这张列表上，那条高亮（用户要求「不要侧边栏再有一个菜单」）。
  if (name === 'memory' || name.startsWith('memory-')) return settingsSection()
  if (name === 'profile' || name.startsWith('profile-')) return profileSection()
  if (name.startsWith('settings-')) return settingsSection()
  // 伙伴是设置项里的一条，进去之后侧栏仍是那张设置列表（伙伴那条高亮），
  // 而不是换成一列只有一项的分组。
  if (name === 'partners' || name === 'personas') return settingsSection()
  return null
}
