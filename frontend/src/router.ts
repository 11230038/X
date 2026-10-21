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
    // 写作页自己就是工作台：没有 sessionId 时是准备表，有 sessionId 时是那段
    // 写作。一条可选参数记录就够了，不再跳去对话页。
    path: '/co-writer/:sessionId?',
    name: 'co-writer',
    component: () => import('./views/CoWriter.vue'),
  },

  // ── 掌握路径 ────────────────────────────────────────────────────────────
  // 一级页面：不挂在学习外壳下，自己带 AppShell（`MasteryPage.vue`）。
  {
    path: '/mastery',
    name: 'mastery',
    component: () => import('./views/MasteryPage.vue'),
  },
  // 路径自己的对话页：能力固定 `mastery_path`，会话按 `kind=mastery` 分区。
  // 一条可选参数记录同时承担首页与某一段对话，与写作页同一种写法。
  {
    path: '/mastery/chat/:sessionId?',
    name: 'mastery-chat',
    component: () => import('./views/MasteryChatPage.vue'),
  },

  // ── 学习 ────────────────────────────────────────────────────────────────
  // 首页就是「学习」这个一级入口本身；其余学习页面已经独立，收在「其他」下。
  {
    path: '/learning',
    name: 'learning',
    component: () => import('./views/LearningPage.vue'),
  },
  // 旧地址保留重定向，书签不会失效。
  { path: '/learning/practice', redirect: { name: 'practice' } },
  { path: '/learning/mastery', redirect: { name: 'mastery' } },
  { path: '/learning/watching', redirect: { name: 'watching' } },
  { path: '/learning/map', redirect: { name: 'learning-map' } },
  { path: '/learning/reading', redirect: { name: 'reading' } },
  { path: '/learning/books', redirect: { name: 'books' } },
  { path: '/learning/books/:bookId', redirect: (to) => ({ name: 'book', params: to.params }) },

  // ── 学习地图 / 沉浸阅读 / 活书 ─────────────────────────────────────────
  // 三页从学习外壳里独立出来，各自带 AppShell。
  {
    path: '/learning-map',
    name: 'learning-map',
    component: () => import('./views/LearningMapPage.vue'),
  },
  {
    path: '/reading',
    name: 'reading',
    component: () => import('./views/ReadingPage.vue'),
  },
  {
    path: '/books',
    name: 'books',
    component: () => import('./views/BooksPage.vue'),
  },
  {
    path: '/books/:bookId',
    name: 'book',
    component: () => import('./views/BookPage.vue'),
    props: true,
  },

  // ── 视频学习 ────────────────────────────────────────────────────────────
  // 与「学习」并列，但收在「其他」下；自己带 AppShell（`WatchingPage.vue`）。
  {
    path: '/watching',
    name: 'watching',
    component: () => import('./views/WatchingPage.vue'),
  },

  // ── 数据库（知识库） ─────────────────────────────────────────────────────
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
  // 题目问答：题库自己的对话页。一条可选参数记录同时承担首页与某一段对话，
  // 与写作页、路径对话页同一种写法；会话按 `kind=question_bank` 分区。
  // 它排在 `/questions` 之后不影响匹配：两条都是静态前缀，参数只在这一条上。
  {
    path: '/questions/chat/:sessionId?',
    name: 'question-chat',
    component: () => import('./views/questions/QuestionChatPage.vue'),
  },
  // 练习归到题库下，自己带 AppShell（`PracticePage.vue`），不进学习外壳。
  {
    path: '/practice',
    name: 'practice',
    component: () => import('./views/PracticePage.vue'),
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

  // ── 管理台（头像菜单进入，不在导航入口里） ─────────────────────────────
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
