<script setup lang="ts">
/**
 * 对话列表栏：把一份会话清单铺成 `ListPane` 里的行。
 *
 * **搜索的状态留在这里**，不是因为它归界面管，而是因为 `ListPane` 把过滤判
 * 给了使用方（它的 `query` 注释就是这么写的）。这一段落到这里之后，`Chat.vue`
 * 关心的只剩"有哪些会话"与"载入/失败了吗"，不再需要知道"筛出来几条"。
 */

import { computed, ref } from 'vue'

import type { SessionSummary } from '../../api/sessions'
import ListPane from '../shell/ListPane.vue'
import { sessionLabel } from './session-label'

const props = defineProps<{
  sessions: readonly SessionSummary[]
  loading: boolean
  /** 列表本身没载进来时的那句话。整栏的错误，不是某一行的。 */
  error: string | null
}>()

const emit = defineEmits<{
  (event: 'new-session'): void
}>()

const query = ref('')

const filtered = computed(() => {
  const needle = query.value.trim().toLowerCase()
  if (needle === '') {
    return props.sessions
  }
  return props.sessions.filter((session) => session.title.toLowerCase().includes(needle))
})
</script>

<template>
  <ListPane
    title="对话"
    v-model:query="query"
    empty-action="开始一段对话"
    :loading="loading"
    :empty="filtered.length === 0"
    @empty-action="emit('new-session')"
  >
    <template #actions>
      <button type="button" class="icon-button" aria-label="新建会话" @click="emit('new-session')">
        ＋
      </button>
    </template>

    <li v-for="session in filtered" :key="session.session_id">
      <RouterLink
        class="session"
        active-class="session--active"
        :to="{ name: 'conversation', params: { sessionId: session.session_id } }"
      >
        <span class="session__title" :class="{ 'session__title--untitled': session.title === '' }">
          {{ sessionLabel(session) }}
        </span>
      </RouterLink>
    </li>

    <li v-if="error" class="session__error">{{ error }}</li>
  </ListPane>
</template>

<style scoped>
.icon-button {
  padding: 3px 8px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text-muted);
  cursor: pointer;
}

.icon-button:hover {
  border-color: var(--accent);
  color: var(--accent);
}

.session {
  display: block;
  padding: 8px 10px;
  border-radius: var(--radius-sm);
  color: inherit;
  text-decoration: none;
}

.session:hover {
  background: var(--surface-sunken);
}

.session--active {
  background: var(--accent-soft);
}

.session__title {
  display: block;
  overflow: hidden;
  font-size: 13px;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.session__title--untitled {
  color: var(--text-faint);
}

.session__error {
  padding: 10px;
  color: var(--danger);
  font-size: 13px;
}
</style>
