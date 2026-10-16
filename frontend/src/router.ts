/**
 * 路由表。
 *
 * `设计说明书.md` §2.3 那张页面总清单里，stage 0 只落了**对话那两条**加认证
 * 那两条。其余路径**故意不注册** —— 一个指向空白页的路由比一个 404 更难判断
 * 是"还没做"还是"做坏了"。一级导航里那些分组因此点不动，这是预期。
 */

import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

import { isSignedIn } from './session'

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
  {
    path: '/chat',
    name: 'chat',
    component: () => import('./views/Chat.vue'),
  },
  {
    // 列表栏里选中的会话。同一个组件，靠 `:sessionId` 决定连哪条流。
    path: '/chat/:sessionId',
    name: 'conversation',
    component: () => import('./views/Chat.vue'),
  },
  // 没有会话可看时落在对话首页，与后端"会话不存在"的错误各管一段：这里管
  // 路径本身，那里管路径指着的那个东西没了。
  { path: '/', redirect: { name: 'chat' } },
  { path: '/:pathMatch(.*)*', redirect: { name: 'chat' } },
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
 * 会一路走进 `/chat`，然后被第一个真请求的 401 挡回来（`api/client.ts` 把它
 * 标成 `unauthorized`，界面据此拆连接、清状态、回登录页）。**两条路都要有**，
 * 少一条就会出现"被踢回登录页但看不出来为什么"或者"停在空白页上不动"。
 */
router.beforeEach((to) => {
  if (to.meta.signedOutOnly === true && isSignedIn.value) {
    // 已经登录的人不该再看到登录页。这不是安全边界，是别让他填一个白填的
    // 表单。
    return { name: 'chat' }
  }
  if (to.meta.signedOutOnly !== true && !isSignedIn.value) {
    return { name: 'login', query: { from: to.fullPath } }
  }
  return true
})
