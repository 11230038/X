<script setup lang="ts">
/**
 * 登录。`设计说明书.md` §4.1。
 *
 * 失败有三种，出口各不相同（这正是 `api/errors.ts` 那张表存在的理由）：
 * 凭证不对 → 让人改输入；账号停用 → 找他自己的管理员；账号锁定 → 等一会儿。
 * 三句话不能互相顶替。
 */

import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import AuthForm from '../components/auth/AuthForm.vue'
import { describeFailure } from '../api/errors'
import { isApiFailure } from '../api/client'
import { login } from '../api/auth'
import { startSession } from '../session'

const router = useRouter()
const route = useRoute()

const pending = ref(false)
const error = ref<string | null>(null)

async function submit(credentials: { username: string; password: string }): Promise<void> {
  pending.value = true
  error.value = null
  try {
    // 登录回的是一份完整的登录态（`user` + `token` + `expires_at`），所以成
    // 功之后没有"再去换个 token"这一步。
    startSession(await login(credentials))

    // 回到被挡下来的那一页。守卫把原来想去的地方塞在 `from` 里，直接落到
    // `/chat` 会把用户刚点的那个会话丢掉。
    const from = route.query.from
    await router.replace(typeof from === 'string' && from !== '' ? from : { name: 'chat' })
  } catch (cause) {
    error.value = isApiFailure(cause) ? describeFailure(cause.failure) : '登录没能完成，请重试'
  } finally {
    pending.value = false
  }
}
</script>

<template>
  <AuthForm
    mode="login"
    title="登录"
    submit-label="登录"
    :pending="pending"
    :error="error"
    @submit="submit"
  >
    <template #footer>
      <!--
        §7.2：**没有"忘记密码"**，后端不实现，这一版也不画。
        §4.1：前往另一页的是一个链接，不是标签切换。
      -->
      <RouterLink to="/register">注册新账号</RouterLink>
    </template>
  </AuthForm>
</template>
