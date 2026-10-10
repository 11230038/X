import { createRouter, createWebHistory } from 'vue-router'

import { getToken } from '../auth/client'
import { authState, ensureSession } from '../auth/store'
import HomePage from '../pages/HomePage.vue'
import LoginPage from '../pages/LoginPage.vue'
import RegisterPage from '../pages/RegisterPage.vue'

declare module 'vue-router' {
  interface RouteMeta {
    requiresAuth?: boolean
    /** Reachable only while signed out; a signed-in visitor is sent home. */
    guestOnly?: boolean
  }
}

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'home', component: HomePage, meta: { requiresAuth: true } },
    { path: '/login', name: 'login', component: LoginPage, meta: { guestOnly: true } },
    { path: '/register', name: 'register', component: RegisterPage, meta: { guestOnly: true } },
    { path: '/:pathMatch(.*)*', redirect: { name: 'home' } },
  ],
})

router.beforeEach(async (to) => {
  // A hard refresh lands here with a token but no user yet. Validate it before
  // deciding where the visitor belongs.
  if (!authState.ready && getToken()) {
    await ensureSession()
  }
  if (to.meta.requiresAuth && !authState.user) {
    return { name: 'login' }
  }
  if (to.meta.guestOnly && authState.user) {
    return { name: 'home' }
  }
  return true
})

export default router
