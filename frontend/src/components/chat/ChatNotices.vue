<script setup lang="ts">
/**
 * 工作台里输入区上方那一条状态区。
 *
 * 四类提示各说各的，**失败与加载中不能长得像**：连接中/重连/限流共用待定
 * 色，凭据失效与页面级错误用错误色。澄清提问的弹窗被关掉后，这里还留一个
 * 「回答提问」的入口——输入区本身也能回答，所以它不是一个必须走的分支。
 *
 * 掌握路径的出题弹窗收起后同样在这里留一个「继续作答」；它只在路径对话页传入，
 * 别的页面没有这道题。
 */

import { CHAT } from '../../copy/chat'
import { MASTERY } from '../../copy/mastery'
import type { ConnectionStatus } from '../../ws/blocks'

defineProps<{
  connectionStatus: ConnectionStatus
  reconnectAttempt: number
  retryLeft: number | null
  activeElsewhere: boolean
  composerError: string | null
  showComposerError: boolean
  waiting: boolean
  /** 路径对话页收起了出题弹窗，并且题目还没有作答。 */
  masteryQuestionPending?: boolean
}>()

const emit = defineEmits<{
  (event: 'reopen-question'): void
  (event: 'reopen-mastery-question'): void
}>()
</script>

<template>
  <div class="notices">
    <p v-if="connectionStatus === 'connecting'" class="banner banner--pending">
      {{ CHAT.connecting }}
    </p>
    <p v-else-if="connectionStatus === 'reconnecting'" class="banner banner--pending">
      {{
        reconnectAttempt > 0
          ? CHAT.reconnectingAttempt.replace('{n}', String(reconnectAttempt))
          : CHAT.reconnecting
      }}
    </p>
    <p v-else-if="connectionStatus === 'rate_limited'" class="banner banner--pending">
      {{ retryLeft !== null ? CHAT.rateLimited(retryLeft) : CHAT.reconnecting }}
    </p>
    <p v-else-if="connectionStatus === 'credential_lost'" class="banner banner--error">
      {{ CHAT.credentialLost }}
    </p>

    <p v-if="activeElsewhere" class="banner banner--pending">
      {{ CHAT.activeElsewhere }}
    </p>
    <p v-if="composerError && showComposerError" class="banner banner--error">
      {{ composerError }}
    </p>

    <button
      v-if="waiting"
      type="button"
      class="banner banner--action"
      @click="emit('reopen-question')"
    >
      {{ CHAT.questionReopen }}
    </button>

    <!-- 澄清还在等回答时先不抢：那一轮要先答澄清。 -->
    <button
      v-if="masteryQuestionPending && !waiting"
      type="button"
      class="banner banner--action"
      @click="emit('reopen-mastery-question')"
    >
      {{ MASTERY.questionReopen }}
    </button>
  </div>
</template>

<style scoped>
.notices {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 0 28px;
}

.banner {
  margin: 0 0 2px;
  padding: 10px 14px;
  border-radius: var(--radius-sm);
  font-size: 13px;
  font-weight: 500;
}

.banner--pending {
  border: 1px solid rgb(255 255 255 / 70%);
  background: rgb(255 255 255 / 55%);
  color: var(--text-muted);
}

.banner--error {
  border: 1px solid rgb(192 57 43 / 20%);
  background: var(--danger-soft);
  color: var(--danger);
}

/* 可点击的横幅：用来把被关掉的提问弹窗叫回来。 */
.banner--action {
  width: 100%;
  border: 1px solid rgb(177 140 255 / 35%);
  background: rgb(255 255 255 / 75%);
  color: var(--accent-ink);
  font-weight: 600;
  text-align: left;
  cursor: pointer;
}

.banner--action:hover {
  background: #fff;
}
</style>
