/**
 * 路由表。
 *
 * 三条规矩：
 * 1. **有后端的页面才有真路由**；没有后端的占位页已全部移除，未实现的
 *    路径落到 404。技能与工具共用 `/skills` / `/tools` 的只读目录，不算占位。
 * 2. 认不出的路径落到 404，**不再静默重定向到对话**——静默重定向会让一个写错
 *    的链接看起来"能用"，也会把用户刚敲的地址悄悄换掉。
 * 3. 权限不在守卫里判。守卫只看本地登录态；某个面能不能进由页面里的
 *    `SurfaceGate` 按权限快照的三态处理（未知时照常渲染）。
 *
 * `/chat/recycle-bin` 必须排在 `/chat/:sessionId` 前面，否则它会被当成一个
 * 会话 id。
 */

import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

import { isSignedIn } from './session'

declare module 'vue-router' {
  interface RouteMeta {
    /** 只给未登录的人看。 */
    signedOutOnly?: boolean
  }
}

const routes: RouteRecordRaw[] = [
  {
    path: '/login',
    name: 'login',
    component: () => import('./views/Login.vue'),
    meta: { signedOutOnly: true },
  },
  {
    path: '/register',
    name: 'register',
    component: () => import('./views/Register.vue'),
    meta: { signedOutOnly: true },
  },

  // ── 对话 ────────────────────────────────────────────────────────────────
  {
    path: '/chat',
    name: 'chat',
    component: () => import('./views/Chat.vue'),
  },
  {
    path: '/chat/recycle-bin',
    name: 'chat-recycle-bin',
    component: () => import('./views/ChatRecycleBin.vue'),
  },
  {
    path: '/chat/:sessionId',
    name: 'conversation',
    component: () => import('./views/Chat.vue'),
  },
  // 人格是对话这一轮的一项选择，管理页挨着对话放；入口在输入区。
  {
    path: '/personas',
    name: 'personas',
    component: () => import('./views/Personas.vue'),
  },
  // 伙伴与协同写作读的是同一份人格：前者选人开始对话，后者选人并带一句
  // 开场白进对话。两个页面都没有独立的数据表。
  {
    path: '/partners',
    name: 'partners',
    component: () => import('./views/Partners.vue'),
  },
  {
    path: '/co-writer',
    name: 'co-writer',
    component: () => import('./views/CoWriter.vue'),
  },

  // ── 学习 ────────────────────────────────────────────────────────────────
  {
    path: '/learning',
    component: () => import('./views/learning/LearningLayout.vue'),
    children: [
      {
        path: '',
        name: 'learning',
        component: () => import('./views/learning/LearningHome.vue'),
      },
      {
        path: 'practice',
        name: 'practice',
        component: () => import('./views/learning/Practice.vue'),
      },
      {
        path: 'mastery',
        name: 'mastery',
        component: () => import('./views/learning/Mastery.vue'),
      },
      {
        path: 'map',
        name: 'learning-map',
        component: () => import('./views/learning/LearningMap.vue'),
      },
      {
        path: 'reading',
        name: 'reading',
        component: () => import('./views/learning/Reading.vue'),
      },
      {
        path: 'watching',
        name: 'watching',
        component: () => import('./views/learning/Watching.vue'),
      },
      // 书架与大纲/阅读页。详情页用 props 接 `bookId`，组件里再 `watch` 它，
      // 这样从一本书跳到另一本书时不用重建页面。
      {
        path: 'books',
        name: 'books',
        component: () => import('./views/learning/Books.vue'),
      },
      {
        path: 'books/:bookId',
        name: 'book',
        component: () => import('./views/learning/Book.vue'),
        props: true,
      },
    ],
  },

  // ── 材料 ────────────────────────────────────────────────────────────────
  {
    path: '/files',
    name: 'files',
    component: () => import('./views/Files.vue'),
  },
  {
    path: '/knowledge-bases',
    name: 'knowledge-bases',
    component: () => import('./views/KnowledgeBases.vue'),
  },
  {
    path: '/knowledge-bases/:kbId',
    name: 'knowledge-base',
    component: () => import('./views/KnowledgeBases.vue'),
    props: true,
  },
  // 设计说明书里这块叫"材料"；地址保留两种写法，统一落到知识库。
  { path: '/materials', redirect: { name: 'knowledge-bases' } },
  { path: '/materials/:kbId', redirect: (to) => ({ name: 'knowledge-base', params: to.params }) },

  // ── 记忆 ────────────────────────────────────────────────────────────────
  {
    path: '/memory',
    component: () => import('./views/memory/MemoryLayout.vue'),
    children: [
      {
        path: '',
        name: 'memory',
        component: () => import('./views/memory/MemoryHome.vue'),
      },
      {
        path: 'l1',
        name: 'memory-l1',
        component: () => import('./views/memory/MemoryL1.vue'),
      },
      {
        path: 'l2',
        name: 'memory-l2',
        component: () => import('./views/memory/MemoryL2.vue'),
      },
      {
        path: 'l3',
        name: 'memory-l3',
        component: () => import('./views/memory/MemoryL3.vue'),
      },
      {
        path: 'graph',
        name: 'memory-graph',
        component: () => import('./views/memory/MemoryGraph.vue'),
      },
      {
        path: 'resolve',
        name: 'memory-resolve',
        component: () => import('./views/memory/MemoryResolve.vue'),
      },
    ],
  },

  // ── 题库 ────────────────────────────────────────────────────────────────
  {
    path: '/questions',
    name: 'questions',
    component: () => import('./views/Questions.vue'),
  },

  // ── 设置 ────────────────────────────────────────────────────────────────
  {
    path: '/settings',
    component: () => import('./views/settings/SettingsLayout.vue'),
    children: [
      { path: '', redirect: { name: 'settings-account' } },
      {
        path: 'account',
        name: 'settings-account',
        component: () => import('./views/settings/SettingsAccount.vue'),
      },
      {
        path: 'workspace',
        name: 'settings-workspace',
        component: () => import('./views/settings/SettingsWorkspace.vue'),
      },
      {
        path: 'usage',
        name: 'settings-usage',
        component: () => import('./views/settings/SettingsUsage.vue'),
      },
      {
        path: 'system',
        name: 'settings-system',
        component: () => import('./views/settings/SettingsSystem.vue'),
      },
      {
        path: 'knowledge-bases',
        name: 'settings-knowledge-bases',
        component: () => import('./views/settings/SettingsKnowledgeBases.vue'),
      },
      {
        path: 'guardian',
        name: 'settings-guardian',
        component: () => import('./views/settings/SettingsGuardian.vue'),
      },
      {
        path: 'about',
        name: 'settings-about',
        component: () => import('./views/settings/SettingsAbout.vue'),
      },
    ],
  },

  // ── 管理台（头像菜单进入，不在九组里） ─────────────────────────────────
  {
    path: '/admin',
    component: () => import('./views/admin/AdminLayout.vue'),
    children: [
      { path: '', redirect: { name: 'admin-users' } },
      {
        path: 'users',
        name: 'admin-users',
        component: () => import('./views/admin/AdminUsers.vue'),
      },
      {
        path: 'audit',
        name: 'admin-audit',
        component: () => import('./views/admin/AdminAudit.vue'),
      },
    ],
  },

  // ── 扩展 ────────────────────────────────────────────────────────────────
  // 技能与工具共用一页：工具由能力声明，没有独立的工具接口。
  {
    path: '/skills',
    name: 'skills',
    component: () => import('./views/extensions/CapabilityCatalog.vue'),
  },
  {
    path: '/tools',
    name: 'tools',
    component: () => import('./views/extensions/CapabilityCatalog.vue'),
  },
  { path: '/', redirect: { name: 'chat' } },
  {
    path: '/:pathMatch(.*)*',
    name: 'not-found',
    component: () => import('./views/NotFoundView.vue'),
  },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
})

/**
 * 守卫。**判的是本地这一份登录态，不是"这个 token 还有效吗"** —— 后者只有
 * 服务端知道，问它需要在每次导航上花一个请求。
 *
 * 所以这个守卫只回答"该去哪儿"，不回答"能不能进"：一个已经被撤回的 token
 * 会一路走进页面，然后被第一个真请求的 401 挡回来（`api/client.ts` 把它标成
 * `unauthorized`，`auth/auth-failure.ts` 据此拆连接、清状态、回登录页）。
 */
router.beforeEach((to) => {
  if (to.meta.signedOutOnly === true && isSignedIn.value) {
    return { name: 'chat' }
  }
  if (to.meta.signedOutOnly !== true && !isSignedIn.value) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  return true
})
