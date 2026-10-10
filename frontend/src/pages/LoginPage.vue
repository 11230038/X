<script setup lang="ts">
import { useRouter } from 'vue-router'

import { login } from '../auth/store'
import CredentialsForm from '../components/CredentialsForm.vue'

const router = useRouter()

async function submit(username: string, password: string): Promise<void> {
  await login(username, password)
  await router.push({ name: 'home' })
}
</script>

<template>
  <CredentialsForm
    heading="登录"
    submit-label="登录"
    password-autocomplete="current-password"
    :submit="submit"
  >
    <RouterLink class="auth-alt" to="/register">没有账号？去注册</RouterLink>
  </CredentialsForm>
</template>
