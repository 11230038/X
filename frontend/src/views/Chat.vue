<script setup lang="ts">
/**
 * 对话页。
 *
 * 工作台本身（列表栏、状态条、输入区、右侧面板）在 `chat/ChatWorkspace.vue`
 * 里，写作页用的是同一份。这里只说三件事：这一页列 `chat` 会话、会话地址是
 * `conversation`、首页摆几条起始提示。
 */

import StarterPrompts from '../components/chat/StarterPrompts.vue'
import ChatWorkspace from './chat/ChatWorkspace.vue'
</script>

<template>
  <ChatWorkspace
    kind="chat"
    :home-route="{ name: 'chat' }"
    session-route="conversation"
  >
    <template #home="{ startSession }">
      <!-- 对话首页：没有会话，就摆几条真能点的问题（§7.1 第 3 条）。 -->
      <StarterPrompts @pick="startSession" />
    </template>
  </ChatWorkspace>
</template>
