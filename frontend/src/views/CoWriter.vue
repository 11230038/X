<script setup lang="ts">
/**
 * 协同写作。
 *
 * 这一页**不跳去对话页**：它自己就是工作台，只是左边列的是写作会话
 * （`kind=writing`），首页是准备表。写作的来回、引用、断线重连与对话页完全
 * 同一套实现 —— 都在 `chat/ChatWorkspace.vue` 里。
 *
 * 没有独立的文稿存储：写下来的内容就是会话消息。
 */

import ChatWorkspace from './chat/ChatWorkspace.vue'
import CoWriterSetup from './co-writer/CoWriterSetup.vue'
import { CO_WRITER } from '../copy/collaboration'
</script>

<template>
  <ChatWorkspace
    kind="writing"
    :home-route="{ name: 'co-writer' }"
    session-route="co-writer"
    :home-title="CO_WRITER.title"
    :show-capability="false"
    :composer-on-home="false"
    :placeholder="CO_WRITER.composerPlaceholder"
    :list-title="CO_WRITER.listTitle"
    :list-empty-action="CO_WRITER.listEmptyAction"
    :list-new-label="CO_WRITER.listNewLabel"
    :empty-title="CO_WRITER.emptySession"
    :empty-hint="CO_WRITER.emptySessionHint"
    :empty-action-label="CO_WRITER.emptySessionAction"
  >
    <template
      #home="{
        startSession,
        starting,
        startError,
        personaItems,
        personaId,
        personaLoading,
        personaFailure,
        selectPersona,
        retryPersonas,
        managePersonas,
      }"
    >
      <CoWriterSetup
        :persona-items="personaItems"
        :persona-id="personaId"
        :persona-loading="personaLoading"
        :persona-failure="personaFailure"
        :starting="starting"
        :start-error="startError"
        @select-persona="selectPersona"
        @retry-personas="retryPersonas"
        @manage-personas="managePersonas"
        @start="startSession"
      />
    </template>
  </ChatWorkspace>
</template>
