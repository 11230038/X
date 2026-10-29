<script setup lang="ts">
/**
 * 新建账号。**只有超级管理员看得到入口**，但这只是显隐 —— 请求本身由服务端
 * 的白名单门拒绝或放行。
 *
 * 表单规则与后端同一条：用户名 `^[A-Za-z0-9_-]{3,32}$`，密码 8–72 **字节**
 * （bcrypt 的输入上限；中文一个字符算三字节，所以不能只看 `length`）。这里
 * 先判一次是为了让常见的错在提交之前就说清楚，后端仍然会再判一次。
 *
 * 建出来的账号不带登录态：管理员建的是别人的凭据，不是把这个人登进来。
 */

import { computed, ref } from 'vue'

import { createUser, type AdminUser, type Preset, type Role } from '../../api/admin'
import type { ApiFailure } from '../../api/client'
import { ADMIN } from '../../copy/admin'
import { toFailure } from '../../shared/resource'

const emit = defineEmits<{ (event: 'created', user: AdminUser): void }>()

const dialog = ref<HTMLDialogElement | null>(null)

const username = ref('')
const password = ref('')
const role = ref<Role>('user')
const preset = ref<Preset>('standard')
const submitting = ref(false)
const failure = ref<ApiFailure | null>(null)

const ROLES: Role[] = ['user', 'admin']
const PRESETS: Preset[] = ['standard', 'learner', 'custom']

const USERNAME_PATTERN = /^[A-Za-z0-9_-]{3,32}$/
const MIN_PASSWORD_BYTES = 8
const MAX_PASSWORD_BYTES = 72

/** 密码的长度规则是字节，不是字符。 */
function passwordBytes(value: string): number {
  return new TextEncoder().encode(value).length
}

const usernameOk = computed(() => USERNAME_PATTERN.test(username.value))
const passwordOk = computed(() => {
  const size = passwordBytes(password.value)
  return size >= MIN_PASSWORD_BYTES && size <= MAX_PASSWORD_BYTES
})
const ready = computed(() => usernameOk.value && passwordOk.value && !submitting.value)

function show(): void {
  username.value = ''
  password.value = ''
  role.value = 'user'
  preset.value = 'standard'
  failure.value = null
  submitting.value = false
  if (!dialog.value?.open) dialog.value?.showModal()
}

function hide(): void {
  if (dialog.value?.open) dialog.value.close()
}

async function submit(): Promise<void> {
  if (!ready.value) return
  submitting.value = true
  failure.value = null
  try {
    const result = await createUser({
      username: username.value.trim(),
      password: password.value,
      role: role.value,
      preset: preset.value,
    })
    hide()
    emit('created', result.user)
  } catch (cause) {
    failure.value = toFailure(cause)
  } finally {
    submitting.value = false
  }
}

defineExpose({ show, hide })
</script>

<template>
  <dialog ref="dialog" class="account-dialog" @cancel.prevent="hide">
    <h2 class="account-dialog__title">{{ ADMIN.createUser }}</h2>
    <p class="account-dialog__hint">{{ ADMIN.createUserScope }}</p>

    <div class="account-form">
      <label class="field">
        <span class="field__label">{{ ADMIN.username }}</span>
        <input
          v-model="username"
          type="text"
          class="field__input"
          autocomplete="off"
          :disabled="submitting"
        />
        <span class="field__note">{{ ADMIN.usernameHint }}</span>
      </label>

      <label class="field">
        <span class="field__label">{{ ADMIN.initialPassword }}</span>
        <input
          v-model="password"
          type="password"
          class="field__input"
          autocomplete="new-password"
          :disabled="submitting"
        />
        <span class="field__note">{{ ADMIN.passwordHint }}</span>
      </label>

      <div class="account-form__row">
        <label class="field">
          <span class="field__label">{{ ADMIN.role }}</span>
          <select v-model="role" class="field__input" :disabled="submitting">
            <option v-for="option in ROLES" :key="option" :value="option">
              {{ option === 'admin' ? ADMIN.roleAdmin : ADMIN.roleUser }}
            </option>
          </select>
        </label>

        <label class="field">
          <span class="field__label">{{ ADMIN.preset }}</span>
          <select v-model="preset" class="field__input" :disabled="submitting">
            <option v-for="option in PRESETS" :key="option" :value="option">
              {{ option }}
            </option>
          </select>
        </label>
      </div>
    </div>

    <p v-if="failure" class="error">
      {{ failure.message }}
      <code v-if="failure.requestId" class="account-dialog__req">{{ failure.requestId }}</code>
    </p>

    <div class="account-dialog__actions">
      <button type="button" class="ghost" :disabled="submitting" @click="hide">取消</button>
      <button type="button" class="primary" :disabled="!ready" @click="submit">
        {{ submitting ? '创建中…' : ADMIN.createUser }}
      </button>
    </div>
  </dialog>
</template>

<style scoped src="./AdminUsers.css"></style>
<style scoped>
.account-dialog {
  width: min(460px, calc(100vw - 32px));
  padding: 24px;
  border: 1px solid var(--glass-border);
  border-radius: var(--radius-card);
  background: rgb(255 255 255 / 92%);
  color: var(--text);
  box-shadow: 0 24px 64px -12px rgb(51 68 85 / 20%);
  backdrop-filter: blur(30px);
  -webkit-backdrop-filter: blur(30px);
}

.account-dialog::backdrop {
  background: rgb(20 26 38 / 25%);
  backdrop-filter: blur(6px);
}

.account-dialog__title {
  margin: 0 0 6px;
  font-size: 15px;
  font-weight: 650;
}

.account-dialog__hint {
  margin: 0 0 12px;
  color: var(--text-muted);
  font-size: 14px;
  line-height: 1.6;
}

.account-form {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.account-form__row {
  display: flex;
  gap: 14px;
}

.account-form__row .field {
  flex: 1;
}

.field__note {
  color: var(--text-faint);
  font-size: 14px;
}

.account-dialog__actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 16px;
}

.account-dialog__req {
  color: var(--text-faint);
  font-size: 14px;
}
</style>
