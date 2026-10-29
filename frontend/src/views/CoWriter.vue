<script setup lang="ts">
/**
 * 协同写作。
 *
 * 这一页**不跳去对话页**：它自己就是工作台，只是左边列的是写作会话
 * （`kind=writing`），首页是准备表。写作的来回、引用、断线重连与对话页完全
 * 同一套实现 —— 都在 `chat/ChatWorkspace.vue` 里。
 *
 * **不选人格**：写作页固定用「写作教练」（`personaLock`），准备表不再画选择器，
 * 输入区顶栏退成一枚只读的名字。这个系统人格只属于这一页，别的对话的选择器
 * 不列它（`copy/personas.ts` 的 `isSystemPersona`）；账号里还没有它时，工作台
 * 按 `WRITING_COACH_PERSONA` 的种子建一个。
 *
 * 没有独立的文稿存储：写下来的内容就是会话消息。
 */

import ChatWorkspace from './chat/ChatWorkspace.vue'
import CoWriterSetup from './co-writer/CoWriterSetup.vue'
import { CO_WRITER, WRITING_COACH_PERSONA } from '../copy/collaboration'
</script>

<template>
  <ChatWorkspace
    kind="writing"
    :persona-lock="WRITING_COACH_PERSONA"
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
    <template #home="{ startSession, starting, startError }">
      <CoWriterSetup
        :starting="starting"
        :start-error="startError"
        @start="startSession"
      />
    </template>
  </ChatWorkspace>
</template>
