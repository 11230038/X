import { createApp } from 'vue'

import './style.css'
import App from './App.vue'
import { router } from './router'
import { restoreSession } from './session'

// **顺序是有意的**：先把 token 从 `localStorage` 装回 `api/client` 并让 `session`
// 知道当前账号，再让路由开始解析。反过来的话，守卫还没读到登录态就先判了一次，
// 刷新页面会被推到登录页 —— 而那个 token 其实还在。
restoreSession()

createApp(App).use(router).mount('#app')
