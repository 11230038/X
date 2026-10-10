<script setup lang="ts">
import { useRouter } from 'vue-router'

import { register } from '../auth/store'
import CredentialsForm from '../components/CredentialsForm.vue'

const router = useRouter()

async function submit(username: string, password: string): Promise<void> {
  await register(username, password)
  await router.push({ name: 'home' })
}
</script>

<template>
  <CredentialsForm
    heading="注册"
    submit-label="注册"
    password-autocomplete="new-password"
    :submit="submit"
  >
    <RouterLink class="auth-alt" to="/login">已有账号？去登录</RouterLink>
  </CredentialsForm>
</template>
