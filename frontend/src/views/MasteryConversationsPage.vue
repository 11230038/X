<script setup lang="ts">
/**
 * 路径对话的第一级：对话历史。
 *
 * 一行是一段掌握路径对话，行上标出它创建的那条路径。路径与对话是一对一
 * 的——`mastery_paths.owner_session_id` 就是创建它的那段对话——所以这一页
 * 按路径取对话，不按关联表取：一段对话即便切换进过别的路径，也只归它自己
 * 创建的那条，不会让同一条路径出现两遍。
 *
 * 点一行进二级（`MasteryChatPage.vue`）继续那段对话。
 *
 * 这一页是**只读的历史**：新建不在这个页面上。新路径从掌握路径页或学习地图的
 * 「去路径对话新建」直接开一段会话（`shared/masteryChat.ts`），第一句落进去时
 * 后端才建路径（`mastery.Open`）。放在这里会让人以为「新建」和「继续」是同一
 * 件事，而前者其实什么路径都还没有。
 */

import { onMounted, ref } from 'vue'

import type { ApiFailure } from '../api/client'
import { listMasteryConversations, type MasteryConversation } from '../api/mastery'
import EmptyState from '../components/common/EmptyState.vue'
import FailureState from '../components/common/FailureState.vue'
import SkeletonBlock from '../components/common/SkeletonBlock.vue'
import SurfaceGate from '../components/common/SurfaceGate.vue'
import AppShell from '../components/shell/AppShell.vue'
import PageHeader from '../components/shell/PageHeader.vue'
import { MASTERY } from '../copy/mastery'
import { formatRelative } from '../shared/format'
import { toFailure } from '../shared/resource'
import AppIcon, { iconForEmoji } from '../components/common/AppIcon.vue'

type Status = 'idle' | 'loading' | 'ready' | 'failed'

const conversations = ref<MasteryConversation[]>([])
const status = ref<Status>('idle')
const failure = ref<ApiFailure | null>(null)

async function load(): Promise<void> {
  status.value = conversations.value.length === 0 ? 'loading' : status.value
  failure.value = null
  try {
    const loaded = await listMasteryConversations()
    conversations.value = loaded.conversations
    status.value = 'ready'
  } catch (cause) {
    failure.value = toFailure(cause)
    status.value = conversations.value.length === 0 ? 'failed' : 'ready'
  }
}

onMounted(() => {
  void load()
})
</script>

<template>
  <AppShell>
    <SurfaceGate surface="mastery">
      <SurfaceGate surface="chat">
        <div class="page">
          <div class="page__inner">
            <PageHeader
              :title="MASTERY.chatHistoryTitle"
              :description="MASTERY.chatHistoryDescription"
            />

            <FailureState
              v-if="status === 'failed' && failure"
              :failure="failure"
              :title="MASTERY.chatHistoryFailed"
              @retry="load"
            />

            <SkeletonBlock
              v-else-if="status === 'loading' && conversations.length === 0"
              :lines="5"
              :line-height="54"
            />

            <EmptyState
              v-else-if="conversations.length === 0"
              :title="MASTERY.chatHistoryEmpty"
              :hint="MASTERY.chatHistoryEmptyHint"
            />

            <ul v-else class="list">
              <li v-for="conversation in conversations" :key="conversation.session_id">
                <RouterLink
                  class="row"
                  :to="{ name: 'mastery-chat', params: { sessionId: conversation.session_id } }"
                  :aria-label="MASTERY.chatHistoryOpen"
                >
                  <span class="row__emoji" aria-hidden="true">
                    <AppIcon :name="iconForEmoji(conversation.emoji, 'mastery')" />
                  </span>
                  <span class="row__body">
                    <span class="row__title">
                      {{ conversation.title || conversation.goal || MASTERY.chatHistoryUntitled }}
                    </span>
                    <!--
                      路径名与对话标题是同一段交流的两个摘要，新建路径时逐字相同。
                      相同就不再重复一遍；标题为空时上一行已经落到路径名上。
                    -->
                    <span
                      v-if="conversation.goal && conversation.title && conversation.goal !== conversation.title"
                      class="row__path"
                    >
                      <span class="row__path-label">{{ MASTERY.chatHistoryPath }}</span>
                      {{ conversation.goal }}
                    </span>
                  </span>
                  <span class="row__time">{{ formatRelative(conversation.last_seen_at) }}</span>
                </RouterLink>
              </li>
            </ul>
          </div>
        </div>
      </SurfaceGate>
    </SurfaceGate>
  </AppShell>
</template>

<style scoped>
.list {
  display: flex;
  flex-direction: column;
  gap: 12px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.row {
  display: flex;
  gap: 16px;
  align-items: center;
  padding: 18px 22px;
  border: 1px solid #e2e8f0;
  border-radius: 18px;
  background: #fff;
  box-shadow: var(--glass-shadow);
  color: inherit;
  text-decoration: none;
  transition: all 0.2s ease;
}

.row:hover {
  border-color: var(--color-purple);
  box-shadow: 0 16px 32px -10px rgb(51 68 85 / 12%);
  transform: translateY(-2px);
}

.row:focus-visible {
  outline: none;
  border-color: var(--color-purple);
  box-shadow: 0 0 0 4px rgb(177 140 255 / 15%);
}

.row__emoji {
  display: flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 46px;
  height: 46px;
  border-radius: 13px;
  background: #f3e8ff;
  font-size: 23px;
  line-height: 1;
}

.row__body {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 5px;
  min-width: 0;
}

.row__title {
  overflow: hidden;
  color: var(--text);
  font-size: 17px;
  font-weight: 700;
  line-height: 1.35;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.row__path {
  display: flex;
  gap: 8px;
  align-items: baseline;
  min-width: 0;
  color: var(--text-muted);
  font-size: 14px;
  line-height: 1.5;
}

.row__path-label {
  flex: none;
  padding: 1px 7px;
  border-radius: 6px;
  background: var(--surface-sunken, #f1f5f9);
  color: var(--text-faint);
  font-size: 12px;
  font-weight: 700;
}

.row__time {
  flex: none;
  color: var(--text-faint);
  font-size: 13px;
}
</style>
