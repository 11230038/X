<script setup lang="ts">
/**
 * 账号与改密。
 *
 * 改密成功后后端会吊销**包括当前 token 在内**的全部凭据（204），所以这里
 * 按"主动登出"处理：清本地登录态、清权限与能力缓存、回登录页。不是"改完
 * 继续用"。
 *
 * 没有邮箱、没有找回密码 —— 这个版本没有对应接口，页面不画假入口。
 */

import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import { changePassword, me, type Account } from '../../api/auth'
import type { ApiFailure } from '../../api/client'
import { describeFailure } from '../../api/errors'
import PartialBanner from '../../components/common/PartialBanner.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import { SETTINGS } from '../../copy/settings'
import {
  canSurface,
  permissionSnapshot,
  refreshPermissions,
  resetPermissions,
} from '../../auth/permissions'
import { clearSession } from '../../session'
import { resetCapabilities } from '../../shared/capabilities'
import { toFailure } from '../../shared/resource'
import { notify } from '../../shared/toast'

const router = useRouter()

const account = ref<Account | null>(null)
const loading = ref(true)
const loadFailure = ref<ApiFailure | null>(null)

const oldPassword = ref('')
const newPassword = ref('')
const confirmPassword = ref('')
const submitting = ref(false)
const formFailure = ref('')

const preset = computed(() => permissionSnapshot.value?.preset ?? '')

async function load(): Promise<void> {
  loading.value = true
  loadFailure.value = null
  const [meResult] = await Promise.allSettled([me(), refreshPermissions()])
  if (meResult.status === 'fulfilled') {
    account.value = meResult.value
  } else {
    loadFailure.value = toFailure(meResult.reason)
  }
  loading.value = false
}

function passwordBytes(value: string): number {
  return new TextEncoder().encode(value).length
}

async function submit(): Promise<void> {
  formFailure.value = ''
  if (oldPassword.value === '' || newPassword.value === '') {
    formFailure.value = '当前密码与新密码都不能为空'
    return
  }
  const bytes = passwordBytes(newPassword.value)
  if (bytes < 8 || bytes > 72) {
    formFailure.value = '新密码长度需要在 8–72 字节之间'
    return
  }
  if (newPassword.value !== confirmPassword.value) {
    formFailure.value = '两次输入的新密码不一致'
    return
  }

  submitting.value = true
  try {
    await changePassword(oldPassword.value, newPassword.value)
    // 服务端已经吊销了全部 token（含这一张），所以本地不再发任何请求。
    clearSession()
    resetPermissions()
    resetCapabilities()
    notify(SETTINGS.passwordChanged, 'success')
    await router.push({ name: 'login' })
  } catch (cause) {
    formFailure.value = describeFailure(toFailure(cause))
  } finally {
    submitting.value = false
  }
}

onMounted(() => {
  void load()
})
</script>

<template>
  <div class="account">
    <SkeletonBlock v-if="loading" :lines="4" :line-height="26" />

    <template v-else>
      <PartialBanner
        v-if="loadFailure"
        :failure="loadFailure"
        text="账号信息没能从服务端确认，下面显示的是本机保存的那一份"
        @retry="load"
      />

      <section class="card">
        <h2 class="card__title">{{ SETTINGS.account }}</h2>
        <dl class="meta">
          <dt>{{ SETTINGS.username }}</dt>
          <dd>{{ account?.username ?? '—' }}</dd>
          <dt>{{ SETTINGS.userId }}</dt>
          <dd>{{ account?.user_id ?? '—' }}</dd>
          <dt>{{ SETTINGS.role }}</dt>
          <dd>{{ account?.role ?? '—' }}</dd>
          <dt>{{ SETTINGS.preset }}</dt>
          <dd>{{ preset || '—' }}</dd>
        </dl>
        <p class="hint">{{ SETTINGS.noEmail }}</p>
        <p v-if="canSurface('system') === false" class="hint">
          {{ SETTINGS.whyHidden }}
        </p>
      </section>

      <section class="card">
        <h2 class="card__title">{{ SETTINGS.changePassword }}</h2>
        <p class="hint">改完密码后，所有设备上的登录都会失效，需要重新登录。</p>

        <form class="form" @submit.prevent="submit">
          <label class="field">
            <span class="field__label">{{ SETTINGS.oldPassword }}</span>
            <input v-model="oldPassword" type="password" class="field__input" autocomplete="current-password" />
          </label>
          <label class="field">
            <span class="field__label">{{ SETTINGS.newPassword }}</span>
            <input v-model="newPassword" type="password" class="field__input" autocomplete="new-password" />
          </label>
          <label class="field">
            <span class="field__label">再输一次新密码</span>
            <input v-model="confirmPassword" type="password" class="field__input" autocomplete="new-password" />
          </label>
          <p v-if="formFailure" class="error">{{ formFailure }}</p>
          <div class="actions">
            <button type="submit" class="primary" :disabled="submitting">
              {{ submitting ? '正在修改…' : SETTINGS.changePassword }}
            </button>
          </div>
        </form>
      </section>
    </template>
  </div>
</template>

<style scoped>
.account {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.card {
  padding: 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
}

.card__title {
  margin: 0 0 10px;
  font-size: 13px;
  font-weight: 650;
}

.meta {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 5px 14px;
  margin: 0;
  font-size: 12px;
}

.meta dt {
  color: var(--text-faint);
}

.meta dd {
  margin: 0;
  color: var(--text);
}

.hint {
  margin: 8px 0 0;
  color: var(--text-muted);
  font-size: 11px;
  line-height: 1.6;
}

.form {
  display: flex;
  flex-direction: column;
  gap: 10px;
  max-width: 360px;
  margin-top: 10px;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.field__label {
  color: var(--text-faint);
  font-size: 11px;
}

.field__input {
  padding: 6px 9px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  font-size: 13px;
}

.error {
  margin: 0;
  color: var(--danger);
  font-size: 11px;
}

.actions {
  display: flex;
  justify-content: flex-end;
}

.primary {
  padding: 5px 13px;
  border: 1px solid var(--accent);
  border-radius: var(--radius-sm);
  background: var(--accent);
  color: #fff;
  font-size: 12px;
  cursor: pointer;
}

.primary:disabled {
  cursor: default;
  opacity: 0.55;
}
</style>
