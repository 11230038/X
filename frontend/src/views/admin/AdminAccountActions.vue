<script setup lang="ts">
/**
 * 账号的三种生命周期操作：重置密码、启用/停用、删除。
 *
 * 与详情页其余部分不同的是，这三件事**只有超级管理员能做**。入口按当前登录
 * 账号的 `super_admin` 显隐，但那只是界面：服务端的白名单门是唯一的判据，
 * 普通管理员绕过界面直接请求会拿到 403。
 *
 * 目标本身若是超级管理员，三件事全部不出现，只留一句说明 —— 服务端同样会
 * 用 `protected_account` 拒绝，且调用者自己始终在保护范围内。这不是防误操
 * 作的界面糖，是"至少留一个能撤销一切的账号"那条规则的第一层。
 */

import { computed, ref } from 'vue'

import { deleteUser, setUserDisabled, type AdminUser } from '../../api/admin'
import type { ApiFailure } from '../../api/client'
import { ADMIN } from '../../copy/admin'
import { currentAccount } from '../../session'
import { confirmAction } from '../../shared/confirm'
import { toFailure } from '../../shared/resource'
import { notify } from '../../shared/toast'

import AdminResetPasswordDialog from './AdminResetPasswordDialog.vue'

const props = defineProps<{ user: AdminUser }>()
const emit = defineEmits<{ (event: 'changed'): void }>()

const resetDialog = ref<InstanceType<typeof AdminResetPasswordDialog> | null>(null)
const busy = ref(false)
const actionFailure = ref<ApiFailure | null>(null)

const canManage = computed(() => currentAccount.value?.super_admin === true)

function openResetPassword(): void {
  actionFailure.value = null
  resetDialog.value?.show()
}

async function toggleDisabled(): Promise<void> {
  const user = props.user
  const disabling = !user.disabled
  const confirmed = await confirmAction({
    title: disabling ? ADMIN.disableAccount : ADMIN.enableAccount,
    body: disabling ? ADMIN.disableScope : ADMIN.enableScope,
    confirmLabel: disabling ? ADMIN.disableAccount : ADMIN.enableAccount,
    danger: disabling,
  })
  if (!confirmed) return
  busy.value = true
  actionFailure.value = null
  try {
    await setUserDisabled(user.user_id, disabling)
    notify(disabling ? '账号已停用，现有登录已失效' : '账号已启用', 'success')
    emit('changed')
  } catch (cause) {
    actionFailure.value = toFailure(cause)
  } finally {
    busy.value = false
  }
}

async function remove(): Promise<void> {
  const user = props.user
  const confirmed = await confirmAction({
    title: `${ADMIN.deleteAccount} · ${user.username}`,
    body: ADMIN.deleteScope,
    confirmLabel: ADMIN.deleteAccount,
    danger: true,
  })
  if (!confirmed) return
  busy.value = true
  actionFailure.value = null
  try {
    await deleteUser(user.user_id)
    notify('账号已删除', 'success')
    emit('changed')
  } catch (cause) {
    actionFailure.value = toFailure(cause)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <section v-if="canManage" class="block">
    <div class="account-actions__head">
      <h3 class="block__title">{{ ADMIN.accountActions }}</h3>
      <span v-if="props.user.super_admin" class="chip">{{ ADMIN.superAdmin }}</span>
    </div>

    <p v-if="props.user.super_admin" class="hint">{{ ADMIN.protectedAccount }}</p>

    <div v-else class="detail__actions">
      <button type="button" class="ghost" :disabled="busy" @click="openResetPassword">
        {{ ADMIN.resetPassword }}
      </button>
      <button type="button" class="ghost" :disabled="busy" @click="toggleDisabled">
        {{ props.user.disabled ? ADMIN.enableAccount : ADMIN.disableAccount }}
      </button>
      <button type="button" class="ghost ghost--danger" :disabled="busy" @click="remove">
        {{ ADMIN.deleteAccount }}
      </button>
    </div>

    <p v-if="actionFailure" class="error">
      {{ actionFailure.message }}
      <code v-if="actionFailure.requestId" class="account-actions__req">
        {{ actionFailure.requestId }}
      </code>
    </p>

    <AdminResetPasswordDialog
      ref="resetDialog"
      :user="props.user"
      @done="emit('changed')"
    />
  </section>
</template>

<style scoped src="./AdminUsers.css"></style>
<style scoped>
.account-actions__head {
  display: flex;
  gap: 8px;
  align-items: baseline;
  margin-bottom: 6px;
}

.account-actions__head .block__title {
  margin: 0;
}

.account-actions__req {
  color: var(--text-faint);
  font-size: 14px;
}

.ghost--danger {
  border-color: var(--danger);
  color: var(--danger);
}
</style>
