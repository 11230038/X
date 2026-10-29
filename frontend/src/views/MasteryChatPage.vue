<script setup lang="ts">
/**
 * 掌握路径的对话页（二级）。第一级是 `MasteryConversationsPage.vue`：对话历史，
 * 每行标出它创建的路径。
 *
 * 与写作页同一套工作台（`chat/ChatWorkspace.vue`），不同的只有五处：列的是
 * `kind=mastery` 的会话、每一轮固定用 `mastery_path` 能力、**不用人格**（路径
 * 的引导语由路径本身决定，不给选择器，也不把人格带进任何一轮）、首页是想学
 * 什么的入口、以及**不画工作台自带的会话列表**——那一列已经是第一级的对话
 * 历史，二级只留这一段对话。新建路径不是这一页的按钮：一段没有路径的对话在
 * 这里发出第一轮时，后端会替它建一条（`mastery.Open`）。
 */

import ChatWorkspace from './chat/ChatWorkspace.vue'
import MasteryChatHome from './mastery/MasteryChatHome.vue'
import { MASTERY } from '../copy/mastery'
</script>

<template>
  <ChatWorkspace
    kind="mastery"
    capability="mastery_path"
    :home-route="{ name: 'mastery-conversations' }"
    session-route="mastery-chat"
    :home-title="MASTERY.chatTitle"
    :back-label="MASTERY.chatBackToHistory"
    :show-capability="false"
    :show-persona="false"
    :show-list="false"
    :placeholder="MASTERY.chatPlaceholder"
    :list-title="MASTERY.chatListTitle"
    :list-empty-action="MASTERY.chatListEmptyAction"
    :list-new-label="MASTERY.chatNewLabel"
    :empty-title="MASTERY.chatEmptySession"
    :empty-hint="MASTERY.chatEmptySessionHint"
    :empty-action-label="MASTERY.chatEmptySessionAction"
  >
    <!--
      新对话由掌握路径页或学习地图建好会话再进来（`shared/masteryChat.ts`），
      所以这一页没有「未选中会话」的首页；还没有消息时把同样的开场提示摆出来，
      第一句直接落进这段会话。
    -->
    <template #empty="{ send }">
      <MasteryChatHome @pick="send" />
    </template>
  </ChatWorkspace>
</template>
