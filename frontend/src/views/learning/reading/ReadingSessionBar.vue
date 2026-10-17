<script setup lang="ts">
/**
 * 阅读会话条。
 *
 * 批注与阅读位置都挂在这条会话上。没有会话时这里只做选择，不隐藏"为什么
 * 需要它"；会话列表读失败时就地重试，不影响正文继续读。
 */

import { RouterLink } from 'vue-router'

import type { SessionSummary } from '../../../api/sessions'
import { READING } from '../../../copy/reading'

defineProps<{
  sessionId: string
  sessionTitle: string
  sessions: SessionSummary[]
  sessionsStatus: 'idle' | 'loading' | 'ready' | 'failed'
  sessionMissing: boolean
  sessionHint: boolean
  pendingSession: string
  positionOrdinal: number | null
}>()

const emit = defineEmits<{
  'update:pendingSession': [value: string]
  apply: []
  clear: []
  reloadSessions: []
  jumpPosition: []
}>()

function changePending(event: Event): void {
  emit('update:pendingSession', (event.target as HTMLSelectElement).value)
}
</script>

<template>
  <div class="sessionbar" :class="{ 'sessionbar--warn': sessionHint }">
    <div class="sessionbar__text">
      <span class="sessionbar__label">{{ READING.sessionTitle }}</span>
      <span v-if="sessionId" class="sessionbar__current">{{ sessionTitle }}</span>
      <span v-else class="sessionbar__current sessionbar__current--none">
        {{ READING.sessionNone }}
      </span>
      <span class="sessionbar__hint">{{ READING.sessionHint }}</span>
    </div>
    <div class="sessionbar__actions">
      <template v-if="sessionId">
        <button
          v-if="positionOrdinal !== null"
          type="button"
          class="ghost"
          @click="$emit('jumpPosition')"
        >
          {{ READING.resumeAt(positionOrdinal) }}
        </button>
        <button type="button" class="ghost" @click="$emit('clear')">
          {{ READING.changeSession }}
        </button>
      </template>
      <template v-else>
        <select class="select" :value="pendingSession" :aria-label="READING.sessionChoose" @change="changePending">
          <option value="">{{ READING.sessionChoose }}</option>
          <option v-for="session in sessions" :key="session.session_id" :value="session.session_id">
            {{ session.title }}
          </option>
        </select>
        <button
          type="button"
          class="ghost"
          :disabled="pendingSession === ''"
          @click="$emit('apply')"
        >
          {{ READING.useSession }}
        </button>
      </template>
    </div>
  </div>

  <p v-if="sessionsStatus === 'failed'" class="inline-warn">
    {{ READING.sessionListFailed }}
    <button type="button" class="link" @click="$emit('reloadSessions')">
      {{ READING.retry }}
    </button>
  </p>
  <p v-if="sessionsStatus === 'ready' && sessions.length === 0" class="inline-warn">
    {{ READING.noSessions }} ——
    <RouterLink class="link" :to="{ name: 'chat' }">{{ READING.goChat }}</RouterLink>
  </p>
  <p v-if="sessionMissing" class="inline-warn">
    这个会话不在最近的列表里，可能是更早创建的；它仍然可以用来记录批注与位置。
  </p>
  <p v-if="sessionHint && !sessionId" class="inline-warn">
    {{ READING.sessionRequired }}
  </p>
</template>

<style scoped src="./controls.css"></style>
<style scoped src="./session-bar.css"></style>
