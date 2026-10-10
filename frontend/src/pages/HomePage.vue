<script setup lang="ts">
import { useRouter } from 'vue-router'

import { authState, logout } from '../auth/store'

const router = useRouter()

async function signOut(): Promise<void> {
  logout()
  await router.push({ name: 'login' })
}
</script>

<template>
  <main class="app-shell">
    <section class="home">
      <h1>{{ authState.user?.username }}</h1>
      <button type="button" @click="signOut">退出登录</button>
    </section>
  </main>
</template>
