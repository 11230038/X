<script setup lang="ts">
/**
 * 掌握路径的对话页（挂在掌握路径下）。
 *
 * 与写作页同一套工作台（`chat/ChatWorkspace.vue`），不同的只有三处：列的是
 * `kind=mastery` 的会话、每一轮固定用 `mastery_path` 能力、首页是想学什么的
 * 入口。新建路径不是这一页的按钮：一段没有路径的对话在这里发出第一轮时，
 * 后端会替它建一条（`mastery.Open`）。
 */

import ChatWorkspace from './chat/ChatWorkspace.vue'
import MasteryChatHome from './mastery/MasteryChatHome.vue'
import { MASTERY } from '../copy/mastery'
</script>

<template>
  <ChatWorkspace
    kind="mastery"
    capability="mastery_path"
    :home-route="{ name: 'mastery-chat' }"
    session-route="mastery-chat"
    :home-title="MASTERY.chatTitle"
    :show-capability="false"
    :placeholder="MASTERY.chatPlaceholder"
    :list-title="MASTERY.chatListTitle"
    :list-empty-action="MASTERY.chatListEmptyAction"
    :list-new-label="MASTERY.chatNewLabel"
    :empty-title="MASTERY.chatEmptySession"
    :empty-hint="MASTERY.chatEmptySessionHint"
    :empty-action-label="MASTERY.chatEmptySessionAction"
  >
    <template #home="{ startSession }">
      <MasteryChatHome @pick="startSession" />
    </template>
  </ChatWorkspace>
</template>
