<script setup lang="ts">
import { ref } from 'vue'

import { errorMessage } from '../auth/errors'

const props = defineProps<{
  heading: string
  submitLabel: string
  passwordAutocomplete: 'current-password' | 'new-password'
  /** Performs the request and navigates on success; a throw is shown inline. */
  submit: (username: string, password: string) => Promise<void>
}>()

const username = ref('')
const password = ref('')
const error = ref('')
const pending = ref(false)

async function onSubmit(): Promise<void> {
  error.value = ''
  pending.value = true
  try {
    await props.submit(username.value, password.value)
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    pending.value = false
  }
}
</script>

<template>
  <main class="app-shell">
    <form class="auth-form" @submit.prevent="onSubmit">
      <h1>{{ heading }}</h1>
      <label for="username">用户名</label>
      <input
        id="username"
        v-model="username"
        name="username"
        type="text"
        autocomplete="username"
        autocapitalize="none"
        spellcheck="false"
        required
      />
      <label for="password">密码</label>
      <input
        id="password"
        v-model="password"
        name="password"
        type="password"
        :autocomplete="passwordAutocomplete"
        required
      />
      <button type="submit" :disabled="pending">{{ submitLabel }}</button>
      <p v-if="error" class="auth-error" role="alert">{{ error }}</p>
      <slot />
    </form>
  </main>
</template>
