<script setup lang="ts">
/**
 * 一个用户的监护关系：谁在监护他、他在监护谁，以及建立新关系的表单。
 *
 * 关系是按用户过滤的；列表本身一次取回，增删只改本地这一份。
 */

import { computed, onMounted, ref } from 'vue'

import {
  createGuardianLink,
  listGuardians,
  revokeGuardianLink,
  type AdminGuardianLink,
  type GuardianPermission,
} from '../../api/admin'
import type { ApiFailure } from '../../api/client'
import PartialBanner from '../../components/common/PartialBanner.vue'
import { ADMIN } from '../../copy/admin'
import { confirmAction } from '../../shared/confirm'
import { toFailure } from '../../shared/resource'
import { notify } from '../../shared/toast'

const props = defineProps<{ userId: number }>()

const GUARDIAN_PERMISSIONS: GuardianPermission[] = [
  'view_reports',
  'assign_materials',
  'manage_restrictions',
  'reset_credentials',
]

const PERMISSION_LABELS: Record<GuardianPermission, string> = {
  view_reports: '查看学习报告',
  assign_materials: '分配材料',
  manage_restrictions: '管理限制',
  reset_credentials: '重置凭据',
}

const guardians = ref<AdminGuardianLink[]>([])
const guardiansFailure = ref<ApiFailure | null>(null)
const actionFailure = ref<ApiFailure | null>(null)
const guardianId = ref('')
const learnerId = ref('')
const guardianPermissions = ref<GuardianPermission[]>(['view_reports'])
const linking = ref(false)

const selectedLinks = computed(() =>
  guardians.value.filter(
    (link) => link.guardian_id === props.userId || link.learner_id === props.userId,
  ),
)

async function loadGuardians(): Promise<void> {
  guardiansFailure.value = null
  try {
    const result = await listGuardians()
    guardians.value = result.guardians
  } catch (cause) {
    guardiansFailure.value = toFailure(cause)
  }
}

function toggleGuardianPermission(permission: GuardianPermission): void {
  guardianPermissions.value = guardianPermissions.value.includes(permission)
    ? guardianPermissions.value.filter((item) => item !== permission)
    : [...guardianPermissions.value, permission]
}

async function addGuardianLink(): Promise<void> {
  const guardian = Number(guardianId.value)
  const learner = Number(learnerId.value)
  if (!Number.isInteger(guardian) || guardian <= 0 || !Number.isInteger(learner) || learner <= 0) {
    return
  }
  linking.value = true
  actionFailure.value = null
  try {
    const result = await createGuardianLink(guardian, learner, guardianPermissions.value)
    guardians.value = [...guardians.value, result.guardian]
    guardianId.value = ''
    learnerId.value = ''
    notify('监护关系已建立', 'success')
  } catch (cause) {
    actionFailure.value = toFailure(cause)
  } finally {
    linking.value = false
  }
}

async function removeGuardianLink(link: AdminGuardianLink): Promise<void> {
  const confirmed = await confirmAction({
    title: ADMIN.revokeLink,
    body: ADMIN.revokeLinkScope,
    confirmLabel: ADMIN.revokeLink,
    danger: true,
  })
  if (!confirmed) return
  actionFailure.value = null
  try {
    await revokeGuardianLink(link.link_id)
    guardians.value = guardians.value.filter((item) => item.link_id !== link.link_id)
  } catch (cause) {
    actionFailure.value = toFailure(cause)
  }
}

function permissionText(link: AdminGuardianLink): string {
  return link.permissions.map((permission) => PERMISSION_LABELS[permission]).join('、')
}

onMounted(() => {
  void loadGuardians()
})
</script>

<template>
  <section class="block">
    <h3 class="block__title">{{ ADMIN.guardianLinks }}</h3>

    <PartialBanner
      v-if="guardiansFailure"
      :failure="guardiansFailure"
      text="监护关系没有读出来"
      @retry="loadGuardians"
    />

    <p v-if="actionFailure" class="error">
      这一步没能完成：{{ actionFailure.message }}
    </p>

    <ul v-if="selectedLinks.length > 0" class="links">
      <li v-for="link in selectedLinks" :key="link.link_id" class="link-row">
        <span class="link-row__pair">
          {{ ADMIN.guardian }} #{{ link.guardian_id }} →
          {{ ADMIN.learner }} #{{ link.learner_id }}
        </span>
        <span class="link-row__permissions">
          {{ ADMIN.permissions }}：{{ permissionText(link) }}
        </span>
        <button
          type="button"
          class="link link--danger"
          :disabled="linking"
          @click="removeGuardianLink(link)"
        >
          {{ ADMIN.revokeLink }}
        </button>
      </li>
    </ul>
    <p v-else class="empty">这个用户没有监护关系</p>

    <div class="link-form">
      <h4 class="add__title">{{ ADMIN.linkGuardian }}</h4>
      <div class="add__row">
        <label class="field">
          <span class="field__label">{{ ADMIN.guardian }}编号</span>
          <input v-model="guardianId" type="number" min="1" class="field__input" />
        </label>
        <label class="field">
          <span class="field__label">{{ ADMIN.learner }}编号</span>
          <input v-model="learnerId" type="number" min="1" class="field__input" />
        </label>
        <button
          type="button"
          class="ghost"
          :disabled="linking || Number(guardianId) <= 0 || Number(learnerId) <= 0"
          @click="addGuardianLink"
        >
          {{ linking ? '正在建立…' : ADMIN.linkGuardian }}
        </button>
      </div>
      <div class="permission-options">
        <label
          v-for="permission in GUARDIAN_PERMISSIONS"
          :key="permission"
          class="permission"
        >
          <input
            type="checkbox"
            :checked="guardianPermissions.includes(permission)"
            @change="toggleGuardianPermission(permission)"
          />
          {{ PERMISSION_LABELS[permission] }}
        </label>
      </div>
    </div>
  </section>
</template>

<style scoped src="./AdminUsers.css"></style>
