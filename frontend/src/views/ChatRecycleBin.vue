<script setup lang="ts">
/**
 * 会话回收站。
 *
 * 两件与「删除」有关的事在这里区分开：
 * - **恢复**会把会话放回对话列表，什么都不丢；
 * - **永久删除**会连消息与回合记录一起删，不能撤销。所以它单独走一次危险
 *   确认，确认按钮不是主色。
 *
 * 这与 `Files.vue` 的回收站是同一条规矩：在用列表里只能「移入回收站」，只有
 * 回收站里才出现「永久删除」。
 */

import { computed, onMounted, ref } from 'vue'

import type { ApiFailure } from '../api/client'
import { describeFailure } from '../api/errors'
import {
  listSessions,
  purgeSession,
  restoreSession,
  type SessionKind,
  type SessionSummary,
} from '../api/sessions'
import { canSurface } from '../auth/permissions'
import EmptyState from '../components/common/EmptyState.vue'
import FailureState from '../components/common/FailureState.vue'
import LoadMore from '../components/common/LoadMore.vue'
import SkeletonBlock from '../components/common/SkeletonBlock.vue'
import SurfaceGate from '../components/common/SurfaceGate.vue'
import AppShell from '../components/shell/AppShell.vue'
import PageHeader from '../components/shell/PageHeader.vue'
import { sessionLabel } from '../components/chat/session-label'
import { CHAT } from '../copy/chat'
import { confirmAction } from '../shared/confirm'
import { formatDateTime } from '../shared/format'
import { toFailure } from '../shared/resource'
import { notify } from '../shared/toast'

const PAGE_SIZE = 30

type LoadStatus = 'idle' | 'loading' | 'ready' | 'failed'

/**
 * 回收站按会话分区分开看。各边的恢复与永久删除是同一条规矩，只是行来自
 * 不同的列表；合并成一份会让「对话」列表里突然冒出写作或路径对话。
 */
const activeKind = ref<SessionKind>('chat')

const sessions = ref<SessionSummary[]>([])
const status = ref<LoadStatus>('idle')
const failure = ref<ApiFailure | null>(null)
const staleFailure = ref<ApiFailure | null>(null)
const moreFailure = ref<ApiFailure | null>(null)
const loadingMore = ref(false)
const exhausted = ref(false)
/** 每一行自己的失败：恢复 / 永久删除失败只标在那一行上。 */
const rowErrors = ref<Record<string, string>>({})
const busyId = ref('')

const homeName = computed(() => {
  switch (activeKind.value) {
    case 'writing':
      return 'co-writer'
    case 'mastery':
      return 'mastery-chat'
    case 'question_bank':
      return 'question-chat'
    default:
      return 'chat'
  }
})
const backLabel = computed(() => {
  switch (activeKind.value) {
    case 'writing':
      return CHAT.backToWriting
    case 'mastery':
      return CHAT.backToMastery
    case 'question_bank':
      return CHAT.backToQuestionChat
    default:
      return CHAT.backToChat
  }
})
const emptyTitle = computed(() => {
  switch (activeKind.value) {
    case 'writing':
      return CHAT.recycleBinEmptyWriting
    case 'mastery':
      return CHAT.recycleBinEmptyMastery
    case 'question_bank':
      return CHAT.recycleBinEmptyQuestionBank
    default:
      return CHAT.recycleBinEmpty
  }
})
const emptyHint = computed(() => {
  switch (activeKind.value) {
    case 'writing':
      return CHAT.recycleBinEmptyWritingHint
    case 'mastery':
      return CHAT.recycleBinEmptyMasteryHint
    case 'question_bank':
      return CHAT.recycleBinEmptyQuestionBankHint
    default:
      return CHAT.recycleBinEmptyHint
  }
})

function switchKind(kind: SessionKind): void {
  if (activeKind.value === kind) return
  activeKind.value = kind
  sessions.value = []
  exhausted.value = false
  rowErrors.value = {}
  moreFailure.value = null
  staleFailure.value = null
  void load()
}

async function load(): Promise<void> {
  // 切 tab 时上一个请求可能还在飞；它回来时 kind 已经变了，不能把它的结果
  // 写进当前列表。
  const requestedKind = activeKind.value
  const reset = sessions.value.length === 0
  if (reset) {
    status.value = 'loading'
    failure.value = null
  }
  staleFailure.value = null

  try {
    const result = await listSessions({
      deleted: true,
      kind: requestedKind,
      limit: PAGE_SIZE,
      offset: 0,
    })
    if (activeKind.value !== requestedKind) return
    sessions.value = result.sessions
    exhausted.value = result.sessions.length < PAGE_SIZE
    status.value = 'ready'
    failure.value = null
  } catch (cause) {
    if (activeKind.value !== requestedKind) return
    const next = toFailure(cause)
    if (sessions.value.length === 0) {
      failure.value = next
      status.value = 'failed'
    } else {
      // 已有内容：保留它，只挂一条"这次刷新没成功"。
      staleFailure.value = next
      status.value = 'ready'
    }
  }
}

async function loadMore(): Promise<void> {
  if (loadingMore.value || exhausted.value) return
  const requestedKind = activeKind.value
  loadingMore.value = true
  moreFailure.value = null
  try {
    const result = await listSessions({
      deleted: true,
      kind: requestedKind,
      limit: PAGE_SIZE,
      offset: sessions.value.length,
    })
    if (activeKind.value !== requestedKind) return
    sessions.value = [...sessions.value, ...result.sessions]
    exhausted.value = result.sessions.length < PAGE_SIZE
  } catch (cause) {
    if (activeKind.value !== requestedKind) return
    moreFailure.value = toFailure(cause)
  } finally {
    loadingMore.value = false
  }
}

async function restore(session: SessionSummary): Promise<void> {
  if (busyId.value !== '') return
  busyId.value = session.session_id
  delete rowErrors.value[session.session_id]
  try {
    await restoreSession(session.session_id)
    sessions.value = sessions.value.filter((row) => row.session_id !== session.session_id)
    notify(CHAT.restored, 'success')
  } catch (cause) {
    rowErrors.value = {
      ...rowErrors.value,
      [session.session_id]: messageFor(cause, CHAT.failedToRestore),
    }
  } finally {
    busyId.value = ''
  }
}

async function purge(session: SessionSummary): Promise<void> {
  if (busyId.value !== '') return
  const confirmed = await confirmAction({
    title: CHAT.purgeTitle,
    body: CHAT.purgeBody,
    note: CHAT.purgeNote,
    confirmLabel: CHAT.purgeConfirm,
    danger: true,
  })
  if (!confirmed) return

  busyId.value = session.session_id
  delete rowErrors.value[session.session_id]
  try {
    await purgeSession(session.session_id)
    sessions.value = sessions.value.filter((row) => row.session_id !== session.session_id)
    notify(CHAT.purged, 'success')
  } catch (cause) {
    rowErrors.value = {
      ...rowErrors.value,
      [session.session_id]: messageFor(cause, CHAT.failedToPurge),
    }
  } finally {
    busyId.value = ''
  }
}

/**
 * 行上的失败。连接类的问题说"网络 / 稍后再试"，业务拒绝（404、409 之类）
 * 说这一行没完成 —— 后端那些英文 message 不直接摆给用户。
 */
function messageFor(cause: unknown, fallback: string): string {
  const next = toFailure(cause)
  if (next.kind === 'network' || next.kind === 'rate_limited' || next.kind === 'unauthorized') {
    return describeFailure(next)
  }
  return fallback
}

onMounted(() => {
  if (canSurface('chat') === false) {
    status.value = 'ready'
    return
  }
  void load()
})
</script>

<template>
  <AppShell>
    <SurfaceGate surface="chat">
      <div class="page">
        <div class="page__inner">
          <PageHeader :title="CHAT.recycleBin" :description="CHAT.recycleBinDescription">
            <template #actions>
              <RouterLink class="back" :to="{ name: homeName }">{{ backLabel }}</RouterLink>
            </template>
          </PageHeader>

          <!-- 对话、写作、路径对话与题目问答各有一份回收站：分区在删除之后也成立。 -->
          <div class="kinds" role="group" :aria-label="CHAT.kindTabsLabel">
            <button
              type="button"
              class="kind"
              :class="{ 'kind--on': activeKind === 'chat' }"
              :aria-pressed="activeKind === 'chat'"
              @click="switchKind('chat')"
            >
              {{ CHAT.kindChat }}
            </button>
            <button
              type="button"
              class="kind"
              :class="{ 'kind--on': activeKind === 'writing' }"
              :aria-pressed="activeKind === 'writing'"
              @click="switchKind('writing')"
            >
              {{ CHAT.kindWriting }}
            </button>
            <button
              type="button"
              class="kind"
              :class="{ 'kind--on': activeKind === 'mastery' }"
              :aria-pressed="activeKind === 'mastery'"
              @click="switchKind('mastery')"
            >
              {{ CHAT.kindMastery }}
            </button>
            <button
              type="button"
              class="kind"
              :class="{ 'kind--on': activeKind === 'question_bank' }"
              :aria-pressed="activeKind === 'question_bank'"
              @click="switchKind('question_bank')"
            >
              {{ CHAT.kindQuestionBank }}
            </button>
          </div>

          <SkeletonBlock v-if="status === 'loading'" :lines="6" :line-height="42" />

          <FailureState
            v-else-if="status === 'failed' && failure"
            :failure="failure"
            title="回收站没有读出来"
            @retry="load"
          />

          <EmptyState
            v-else-if="sessions.length === 0"
            :title="emptyTitle"
            :hint="emptyHint"
            :action-label="backLabel"
            @action="$router.push({ name: homeName })"
          />

          <template v-else>
            <p v-if="staleFailure" class="stale">
              回收站没有刷新成功，显示的是上次读到的内容。
              <button type="button" class="stale__retry" @click="load">重试</button>
            </p>

            <ul class="bin">
              <li v-for="session in sessions" :key="session.session_id" class="bin__row">
                <div class="bin__main">
                  <p class="bin__title">{{ sessionLabel(session) }}</p>
                  <p class="bin__meta">
                    {{ CHAT.deletedAt }}：{{ formatDateTime(session.deleted_at) }}
                  </p>
                  <p v-if="rowErrors[session.session_id]" class="bin__error">
                    {{ rowErrors[session.session_id] }}
                  </p>
                </div>

                <div class="bin__actions">
                  <button
                    type="button"
                    class="bin__restore"
                    :disabled="busyId !== ''"
                    @click="restore(session)"
                  >
                    {{ CHAT.restore }}
                  </button>
                  <button
                    type="button"
                    class="bin__purge"
                    :disabled="busyId !== ''"
                    @click="purge(session)"
                  >
                    {{ CHAT.purgeSession }}
                  </button>
                </div>
              </li>
            </ul>

            <LoadMore
              v-if="sessions.length > 0"
              :loading="loadingMore"
              :exhausted="exhausted"
              :failure="moreFailure"
              @load="loadMore"
            />
          </template>
        </div>
      </div>
    </SurfaceGate>
  </AppShell>
</template>

<style scoped>
.page {
  height: 100%;
  overflow-y: auto;
  background: var(--surface-sunken);
}

.page__inner {
  max-width: 960px;
  margin: 0 auto;
  padding: 28px 32px 48px;
}

.back {
  padding: 5px 12px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  font-size: 12px;
  text-decoration: none;
}

.back:hover {
  border-color: var(--accent);
  color: var(--accent);
}

.kinds {
  display: flex;
  gap: 4px;
  margin-top: 16px;
}

.kind {
  padding: 5px 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text-muted);
  font-size: 12px;
  cursor: pointer;
}

.kind:hover {
  border-color: var(--border-strong);
  color: var(--text);
}

.kind--on {
  border-color: var(--accent);
  background: var(--accent-soft);
  color: var(--accent);
}

.stale {
  display: flex;
  gap: 10px;
  align-items: center;
  margin: 14px 0 0;
  padding: 7px 12px;
  border: 1px solid var(--warning);
  border-radius: var(--radius-sm);
  background: var(--warning-soft);
  font-size: 12px;
}

.stale__retry {
  margin-left: auto;
  padding: 2px 8px;
  border: 1px solid var(--border-strong);
  border-radius: 4px;
  background: transparent;
  color: var(--text);
  font-size: 11px;
  cursor: pointer;
}

.bin {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin: 16px 0 0;
  padding: 0;
  list-style: none;
}

.bin__row {
  display: flex;
  gap: 16px;
  align-items: flex-start;
  justify-content: space-between;
  padding: 13px 16px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
}

.bin__main {
  min-width: 0;
}

.bin__title {
  overflow: hidden;
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.bin__meta {
  margin: 3px 0 0;
  color: var(--text-faint);
  font-size: 11px;
}

.bin__error {
  margin: 6px 0 0;
  color: var(--danger);
  font-size: 12px;
}

.bin__actions {
  display: flex;
  flex: none;
  gap: 8px;
}

.bin__restore,
.bin__purge {
  padding: 5px 12px;
  border-radius: var(--radius-sm);
  font-size: 12px;
  cursor: pointer;
}

.bin__restore {
  border: 1px solid var(--accent);
  background: var(--surface);
  color: var(--accent);
}

.bin__purge {
  border: 1px solid var(--border-strong);
  background: var(--surface);
  color: var(--text-muted);
}

.bin__purge:hover {
  border-color: var(--danger);
  color: var(--danger);
}

.bin__restore:disabled,
.bin__purge:disabled {
  cursor: default;
  opacity: 0.55;
}
</style>
