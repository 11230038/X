import { createApp } from 'vue'

import './style.css'
import App from './App.vue'
import { router } from './router'
import { isSignedIn, restoreSession } from './session'
import { registerLoginRedirect } from './auth/auth-failure'
import { bootstrapSignedIn } from './auth/bootstrap'

// **顺序是有意的**：先把 token 从 `localStorage` 装回 `api/client` 并让 `session`
// 知道当前账号，再让路由开始解析。反过来的话，守卫还没读到登录态就先判了一次，
// 刷新页面会被推到登录页 —— 而那个 token 其实还在。
restoreSession()

// import 这一行也会把 401 的全局出口注册到 `api/client`（模块顶层调用）。
// 跳转目标在这里注入，因为 `auth-failure` 不能直接依赖路由。
registerLoginRedirect((redirect) => {
  void router.replace({ name: 'login', query: { redirect } })
})

createApp(App).use(router).mount('#app')

// 刷新页面时把权限与能力补回来。放在挂载之后：这两个请求失败只会挂一条部分
// 失败条，不该拖住首屏；而一条 401 也需要路由已经装好才能跳登录页。
if (isSignedIn.value) {
  void bootstrapSignedIn()
}
