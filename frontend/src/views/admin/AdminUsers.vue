<script setup lang="ts">
/**
 * 用户列表。选中一个用户后，授权与监护关系交给 `AdminUserDetail`。
 *
 * 没有「清空全部授权」：撤回是逐项、立即生效的。
 */

import { computed, onMounted, ref } from 'vue'

import { listUsers, type AdminUser } from '../../api/admin'
import type { ApiFailure } from '../../api/client'
import EmptyState from '../../components/common/EmptyState.vue'
import FailureState from '../../components/common/FailureState.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import { ADMIN } from '../../copy/admin'
import { currentAccount } from '../../session'
import { toFailure } from '../../shared/resource'

import AdminCreateUserDialog from './AdminCreateUserDialog.vue'
import AdminUserDetail from './AdminUserDetail.vue'

const users = ref<AdminUser[]>([])
const usersStatus = ref<'loading' | 'ready' | 'failed'>('loading')
const usersFailure = ref<ApiFailure | null>(null)
const search = ref('')

const selectedId = ref<number | null>(null)

const filteredUsers = computed(() => {
  const query = search.value.trim().toLowerCase()
  if (!query) return users.value
  return users.value.filter(
    (user) =>
      user.username.toLowerCase().includes(query) || String(user.user_id).includes(query),
  )
})

const selectedUser = computed(
  () => users.value.find((user) => user.user_id === selectedId.value) ?? null,
)

/**
 * 只有白名单里的账号看得到新建入口。显隐不是权限：四条生命周期路由在服务端
 * 各有一层白名单检查，改掉本地存储让按钮出现也不会多放行一条请求。
 */
const canManage = computed(() => currentAccount.value?.super_admin === true)

const createDialog = ref<InstanceType<typeof AdminCreateUserDialog> | null>(null)

function openCreate(): void {
  createDialog.value?.show()
}

/** 新建成功后重载列表，并选中刚建的那个 —— 下一步通常就是给它授权。 */
async function onCreated(user: AdminUser): Promise<void> {
  await loadUsers()
  selectedId.value = user.user_id
}

function presetLabel(user: AdminUser): string {
  return `${user.preset || '—'} / ${user.preset_of}`
}

async function loadUsers(): Promise<void> {
  usersStatus.value = 'loading'
  usersFailure.value = null
  try {
    const result = await listUsers()
    users.value = result.users
    usersStatus.value = 'ready'
    if (selectedId.value !== null && !users.value.some((user) => user.user_id === selectedId.value)) {
      selectedId.value = null
    }
  } catch (cause) {
    usersFailure.value = toFailure(cause)
    usersStatus.value = 'failed'
  }
}

function selectUser(user: AdminUser): void {
  selectedId.value = user.user_id
}

onMounted(() => {
  void loadUsers()
})
</script>


<template>
  <div class="admin">
    <SkeletonBlock v-if="usersStatus === 'loading'" :lines="6" :line-height="30" />

    <FailureState
      v-else-if="usersStatus === 'failed' && usersFailure"
      :failure="usersFailure"
      title="用户列表没能读出来"
      @retry="loadUsers"
    />

    <template v-else>
      <div class="toolbar">
        <input
          v-model="search"
          type="search"
          class="search"
          :placeholder="ADMIN.searchUsers"
        />
        <button type="button" class="ghost" @click="loadUsers">刷新</button>
        <button v-if="canManage" type="button" class="primary" @click="openCreate">
          {{ ADMIN.createUser }}
        </button>
      </div>

      <EmptyState
        v-if="filteredUsers.length === 0"
        :title="ADMIN.searchUsers"
        hint="没有匹配的用户。"
      />

      <ul v-else class="users">
        <li v-for="user in filteredUsers" :key="user.user_id">
          <button
            type="button"
            class="user"
            :class="{ 'user--active': user.user_id === selectedId }"
            @click="selectUser(user)"
          >
            <span class="user__name">{{ user.username }}</span>
            <span class="user__id">#{{ user.user_id }}</span>
            <span class="chip">{{ user.role === 'admin' ? ADMIN.roleAdmin : ADMIN.roleUser }}</span>
            <span v-if="user.super_admin" class="chip chip--super">{{ ADMIN.superAdmin }}</span>
            <span class="user__preset">{{ presetLabel(user) }}</span>
            <span v-if="user.disabled" class="chip chip--bad">{{ ADMIN.disabled }}</span>
          </button>
        </li>
      </ul>

      <AdminUserDetail
        v-if="selectedUser"
        :user="selectedUser"
        @changed="loadUsers"
      />
    </template>

    <AdminCreateUserDialog ref="createDialog" @created="onCreated" />
  </div>
</template>

<style scoped src="./AdminUsers.css"></style>
