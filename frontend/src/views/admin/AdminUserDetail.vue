<script setup lang="ts">
/**
 * 一个用户的预设基线与逐项覆盖。
 *
 * 权限画成"一条基线 + 逐项覆盖"：预设是基线，`grants` 是叠在上面的覆盖
 * 项。不是等级线 —— 打开一项不会顺带打开别的。
 *
 * 地板项锁定打开、learner 的 never 项锁定关闭、`admin` 面不可授予。这些判断
 * 来自 `auth/policy-mirror.ts`，它只是后端策略的镜像。没有「清空全部授权」：
 * 撤回是逐项、立即生效的。监护关系由 `AdminGuardianLinks` 负责。
 */

import { ref, watch } from 'vue'

import {
  listGrants,
  revokeUserSessions,
  setGrant,
  setUserPreset,
  unlockUser,
  withdrawGrant,
  type AdminGrant,
  type AdminUser,
  type GrantKind,
  type Preset,
} from '../../api/admin'
import type { ApiFailure } from '../../api/client'
import FailureState from '../../components/common/FailureState.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import { lockReason } from '../../auth/policy-mirror'
import { ADMIN } from '../../copy/admin'
import { confirmAction } from '../../shared/confirm'
import { toFailure } from '../../shared/resource'
import { notify } from '../../shared/toast'

import AdminAccountActions from './AdminAccountActions.vue'
import AdminGuardianLinks from './AdminGuardianLinks.vue'

const props = defineProps<{ user: AdminUser }>()
const emit = defineEmits<{ (event: 'changed'): void }>()

const GRANT_KINDS: GrantKind[] = [
  'model',
  'capability',
  'surface',
  'kb',
  'tool',
  'exec',
  'learning_policy',
]

const PRESETS: Preset[] = ['standard', 'learner', 'custom']

const KIND_LABELS: Record<GrantKind, string> = {
  model: '模型',
  capability: '能力',
  surface: '导航面',
  kb: '知识库',
  tool: '工具',
  exec: '命令执行',
  learning_policy: '学习策略',
}

const grants = ref<AdminGrant[]>([])
const grantsStatus = ref<'idle' | 'loading' | 'ready' | 'failed'>('idle')
const grantsFailure = ref<ApiFailure | null>(null)
const detailBusy = ref(false)
const actionFailure = ref<ApiFailure | null>(null)

const addKind = ref<GrantKind>('capability')
const addId = ref('')
const addAllowed = ref(true)

function isLearnerTarget(): boolean {
  return props.user.preset_of === 'learner'
}

function lockOf(kind: string, id: string): 'floor' | 'learner_never' | 'admin_only' | null {
  return lockReason(kind, id, isLearnerTarget())
}

function lockLabel(kind: string, id: string): string {
  const reason = lockOf(kind, id)
  if (reason === 'floor') return ADMIN.floorLocked
  if (reason === 'learner_never') return ADMIN.learnerNever
  if (reason === 'admin_only') return '只有管理员可以持有'
  return ''
}

async function loadGrants(): Promise<void> {
  const user = props.user
  grantsStatus.value = 'loading'
  grantsFailure.value = null
  actionFailure.value = null
  try {
    const result = await listGrants(user.user_id)
    if (props.user.user_id !== user.user_id) return
    grants.value = result.grants
    grantsStatus.value = 'ready'
  } catch (cause) {
    if (props.user.user_id !== user.user_id) return
    grantsFailure.value = toFailure(cause)
    grantsStatus.value = 'failed'
  }
}

watch(
  () => props.user.user_id,
  () => {
    grants.value = []
    actionFailure.value = null
    addId.value = ''
    void loadGrants()
  },
  { immediate: true },
)

async function changePreset(event: Event): Promise<void> {
  const user = props.user
  const value = (event.target as HTMLSelectElement).value as Preset
  if (value === user.preset_of) return
  detailBusy.value = true
  actionFailure.value = null
  try {
    await setUserPreset(user.user_id, value)
    emit('changed')
    await loadGrants()
  } catch (cause) {
    actionFailure.value = toFailure(cause)
  } finally {
    detailBusy.value = false
  }
}

async function revokeSessions(): Promise<void> {
  const user = props.user
  const confirmed = await confirmAction({
    title: ADMIN.revokeSessions,
    body: ADMIN.revokeSessionsScope,
    confirmLabel: ADMIN.revokeSessions,
    danger: true,
  })
  if (!confirmed) return
  detailBusy.value = true
  actionFailure.value = null
  try {
    await revokeUserSessions(user.user_id)
    notify('会话已吊销', 'success')
  } catch (cause) {
    actionFailure.value = toFailure(cause)
  } finally {
    detailBusy.value = false
  }
}

async function unlock(): Promise<void> {
  const user = props.user
  const confirmed = await confirmAction({
    title: ADMIN.unlock,
    body: ADMIN.unlockScope,
    confirmLabel: ADMIN.unlock,
  })
  if (!confirmed) return
  detailBusy.value = true
  actionFailure.value = null
  try {
    await unlockUser(user.user_id)
    notify('账号已解锁', 'success')
  } catch (cause) {
    actionFailure.value = toFailure(cause)
  } finally {
    detailBusy.value = false
  }
}

async function toggleGrant(grant: AdminGrant): Promise<void> {
  const user = props.user
  if (lockOf(grant.kind, grant.id)) return
  detailBusy.value = true
  actionFailure.value = null
  try {
    const result = await setGrant(user.user_id, {
      kind: grant.kind,
      id: grant.id,
      allowed: !grant.allowed,
    })
    grants.value = grants.value.map((item) =>
      item.kind === result.grant.kind && item.id === result.grant.id ? result.grant : item,
    )
  } catch (cause) {
    actionFailure.value = toFailure(cause)
  } finally {
    detailBusy.value = false
  }
}

async function removeGrant(grant: AdminGrant): Promise<void> {
  const user = props.user
  const confirmed = await confirmAction({
    title: ADMIN.withdraw,
    body: ADMIN.withdrawScope,
    confirmLabel: ADMIN.withdraw,
    danger: true,
  })
  if (!confirmed) return
  detailBusy.value = true
  actionFailure.value = null
  try {
    await withdrawGrant(user.user_id, { kind: grant.kind, id: grant.id })
    grants.value = grants.value.filter(
      (item) => !(item.kind === grant.kind && item.id === grant.id),
    )
  } catch (cause) {
    actionFailure.value = toFailure(cause)
  } finally {
    detailBusy.value = false
  }
}

async function addGrant(): Promise<void> {
  const user = props.user
  const id = addId.value.trim()
  if (id === '') return
  detailBusy.value = true
  actionFailure.value = null
  try {
    const result = await setGrant(user.user_id, {
      kind: addKind.value,
      id,
      allowed: addAllowed.value,
    })
    const existing = grants.value.find(
      (item) => item.kind === result.grant.kind && item.id === result.grant.id,
    )
    grants.value = existing
      ? grants.value.map((item) =>
          item.kind === result.grant.kind && item.id === result.grant.id ? result.grant : item,
        )
      : [...grants.value, result.grant]
    addId.value = ''
  } catch (cause) {
    actionFailure.value = toFailure(cause)
  } finally {
    detailBusy.value = false
  }
}
</script>

<template>
  <section class="detail">
    <header class="detail__head">
      <h2 class="detail__title">{{ props.user.username }}</h2>
      <div class="detail__actions">
        <button type="button" class="ghost" :disabled="detailBusy" @click="revokeSessions">
          {{ ADMIN.revokeSessions }}
        </button>
        <button type="button" class="ghost" :disabled="detailBusy" @click="unlock">
          {{ ADMIN.unlock }}
        </button>
      </div>
    </header>

    <p class="hint">{{ ADMIN.revokeSessionsScope }}</p>
    <p class="hint">{{ ADMIN.unlockScope }}</p>

    <p v-if="actionFailure" class="error">
      这一步没能完成：{{ actionFailure.message }}
    </p>

    <!-- 账号生命周期：只有超级管理员看得到，目标受保护时只留说明 -->
    <AdminAccountActions :user="props.user" @changed="emit('changed')" />

    <!-- 基线 -->
    <section class="block">
      <h3 class="block__title">{{ ADMIN.baseline }}</h3>
      <p class="hint">{{ ADMIN.presetScope }}</p>
      <label class="field">
        <span class="field__label">{{ ADMIN.preset }}</span>
        <select
          class="field__input"
          :value="props.user.preset_of"
          :disabled="detailBusy"
          @change="changePreset"
        >
          <option v-for="preset in PRESETS" :key="preset" :value="preset">
            {{ preset }}
          </option>
        </select>
      </label>
    </section>

    <!-- 覆盖项 -->
    <section class="block">
      <h3 class="block__title">{{ ADMIN.grants }}</h3>

      <SkeletonBlock v-if="grantsStatus === 'loading'" :lines="3" :line-height="26" />

      <FailureState
        v-else-if="grantsStatus === 'failed' && grantsFailure"
        :failure="grantsFailure"
        :title="ADMIN.grants"
        @retry="loadGrants"
      />

      <template v-else>
        <p v-if="grants.length === 0" class="empty">{{ ADMIN.override }}：没有覆盖项</p>
        <ul v-else class="grants">
          <li v-for="grant in grants" :key="`${grant.kind}:${grant.id}`" class="grant">
            <span class="chip">{{ KIND_LABELS[grant.kind] ?? grant.kind }}</span>
            <code class="grant__id">{{ grant.id || '（整个类型）' }}</code>
            <span class="grant__state">{{ grant.allowed ? '允许' : '关闭' }}</span>
            <span v-if="lockLabel(grant.kind, grant.id)" class="grant__lock">
              {{ lockLabel(grant.kind, grant.id) }}
            </span>
            <span class="grant__actions">
              <button
                type="button"
                class="link"
                :disabled="detailBusy || lockLabel(grant.kind, grant.id) !== ''"
                @click="toggleGrant(grant)"
              >
                {{ grant.allowed ? ADMIN.deny : ADMIN.allow }}
              </button>
              <button
                type="button"
                class="link link--danger"
                :disabled="detailBusy"
                @click="removeGrant(grant)"
              >
                {{ ADMIN.withdraw }}
              </button>
            </span>
          </li>
        </ul>

        <div class="add">
          <h4 class="add__title">{{ ADMIN.addGrant }}</h4>
          <div class="add__row">
            <label class="field">
              <span class="field__label">{{ ADMIN.kind }}</span>
              <select v-model="addKind" class="field__input">
                <option v-for="kind in GRANT_KINDS" :key="kind" :value="kind">
                  {{ KIND_LABELS[kind] }}
                </option>
              </select>
            </label>
            <label class="field">
              <span class="field__label">{{ ADMIN.itemId }}</span>
              <input v-model="addId" type="text" class="field__input" />
            </label>
            <label class="field field--check">
              <span class="field__label">{{ ADMIN.allowed }}</span>
              <input v-model="addAllowed" type="checkbox" />
            </label>
            <button
              type="button"
              class="primary"
              :disabled="detailBusy || addId.trim() === '' || (addId.trim() !== '' && lockLabel(addKind, addId.trim()) !== '')"
              @click="addGrant"
            >
              {{ ADMIN.addGrant }}
            </button>
          </div>
          <p class="hint">{{ ADMIN.noGrantCatalog }}</p>
          <p v-if="addId.trim() && lockLabel(addKind, addId.trim())" class="hint">
            {{ lockLabel(addKind, addId.trim()) }}
          </p>
        </div>
      </template>
    </section>

    <p class="hint">{{ ADMIN.noClearAll }}</p>

    <!-- 监护关系 -->
    <AdminGuardianLinks :user-id="props.user.user_id" />
  </section>
</template>

<style scoped src="./AdminUsers.css"></style>
