<script setup lang="ts">
/**
 * 注册。`设计说明书.md` §4.1。
 *
 * **注册成功就直接是登录状态**：后端回的是 201 加一份完整的登录态，与登录同
 * 形。所以这里没有"注册完再去登录一次"——那一步在后端不存在。
 *
 * 额外的三种失败（用户名不合法 / 密码不合法 / 用户名已存在）各自给出**具体
 * 边界**，而不是一句"不合法"。边界写在 `api/errors.ts`，那里也记着它对应
 * `auth/validation.go` 的哪条规则。
 */

import { ref } from 'vue'
import { useRouter } from 'vue-router'

import AuthForm from '../components/auth/AuthForm.vue'
import { describeFailure } from '../api/errors'
import { isApiFailure } from '../api/client'
import { register } from '../api/auth'
import { startSession } from '../session'
import { bootstrapSignedIn } from '../auth/bootstrap'

const router = useRouter()

const pending = ref(false)
const error = ref<string | null>(null)

async function submit(credentials: { username: string; password: string }): Promise<void> {
  pending.value = true
  error.value = null
  try {
    startSession(await register(credentials))
    await bootstrapSignedIn()
    await router.replace({ name: 'chat' })
  } catch (cause) {
    error.value = isApiFailure(cause) ? describeFailure(cause.failure) : '注册没能完成，请重试'
  } finally {
    pending.value = false
  }
}
</script>

<template>
  <AuthForm
    mode="register"
    title="注册"
    submit-label="注册"
    :pending="pending"
    :error="error"
    @submit="submit"
  >
    <template #footer>
      <RouterLink to="/login">已有账号，去登录</RouterLink>
    </template>
  </AuthForm>
</template>
