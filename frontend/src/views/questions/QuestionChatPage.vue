<script setup lang="ts">
/**
 * 题目问答：挂在题库标题下的对话页。
 *
 * 与写作页、路径对话页同一套工作台（`chat/ChatWorkspace.vue`），不同的只有
 * 三处：列的是 `kind=question_bank` 的会话、每一轮固定用 `chat` 能力、首页是
 * 从题库里选一道题再就它提问。会话与普通对话分区，题库里的提问不会灌进对话页
 * 的历史里，反过来也一样。
 *
 * 能力固定为 `chat` 而不是新写一个：这个页面要的是普通的对话能力，题目本身
 * 已经在第一句话里说清楚了，模型不需要再调工具去题库里查。
 */

import ChatWorkspace from '../chat/ChatWorkspace.vue'
import QuestionChatHome from './QuestionChatHome.vue'
import { QUESTION_CHAT } from '../../copy/questions'
</script>

<template>
  <ChatWorkspace
    kind="question_bank"
    capability="chat"
    :home-route="{ name: 'question-chat' }"
    session-route="question-chat"
    :home-title="QUESTION_CHAT.title"
    :show-capability="false"
    :composer-on-home="false"
    :placeholder="QUESTION_CHAT.placeholder"
    :list-title="QUESTION_CHAT.listTitle"
    :list-empty-action="QUESTION_CHAT.listEmptyAction"
    :list-new-label="QUESTION_CHAT.listNewLabel"
    :empty-title="QUESTION_CHAT.emptySession"
    :empty-hint="QUESTION_CHAT.emptySessionHint"
    :empty-action-label="QUESTION_CHAT.emptySessionAction"
  >
    <template #home="{ startSession, starting, startError }">
      <QuestionChatHome
        :start-session="startSession"
        :starting="starting"
        :start-error="startError"
      />
    </template>
  </ChatWorkspace>
</template>
