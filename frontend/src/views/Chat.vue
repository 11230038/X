<script setup lang="ts">
/**
 * 对话。`设计说明书.md` §4.2 的两条路径（`/chat` 与 `/chat/:sessionId`）都由
 * 这一个组件承担 —— 它们共用同一套四段式骨架，差别只在"有没有选中一个会话"。
 *
 * 这个组件是**协调者**：会话列表、能力清单、当前那条连接、材料选择与引用面板
 * 的状态各在自己的 composable 里，渲染交给子组件。
 *
 * 几条贯穿的规则：
 * - **界面上出现的东西都来自服务端**：阶段名与总数来自 `/capabilities`，会话
 *   标题来自 `session_meta`，回答与来源来自事件流。
 * - **401 不在这里处理**：`auth/auth-failure.ts` 是唯一出口（拆连接、清缓存、
 *   回登录页），这里只读状态。
 * - **断线不把这一轮判失败**：已收到的文字留着，重连后 `check_active_turn` 再
 *   决定续传还是重读历史。
 * - **标题不靠重读列表**：`session_meta.content` 到了就就地改这一行。
 */

import { useRoute, useRouter } from 'vue-router'

import { capabilityList } from '../shared/capabilities'
import { CHAT } from '../copy/chat'

import AppShell from '../components/shell/AppShell.vue'
import SidePanel from '../components/shell/SidePanel.vue'
import SurfaceGate from '../components/common/SurfaceGate.vue'
import EmptyState from '../components/common/EmptyState.vue'
import FailureState from '../components/common/FailureState.vue'
import PartialBanner from '../components/common/PartialBanner.vue'
import PendingSkeleton from '../components/chat/PendingSkeleton.vue'
import StarterPrompts from '../components/chat/StarterPrompts.vue'
import SessionListPane from '../components/chat/SessionListPane.vue'
import ConversationStream from '../components/chat/ConversationStream.vue'
import ActivityTimeline from '../components/chat/ActivityTimeline.vue'
import Composer from '../components/chat/Composer.vue'
import MaterialPicker from '../components/chat/MaterialPicker.vue'
import SourcesPanel from '../components/chat/SourcesPanel.vue'
import SourcePreview from '../components/chat/SourcePreview.vue'

import { useChatCapabilities } from './chat/useChatCapabilities'
import { useChatConnection } from './chat/useChatConnection'
import { useChatMaterials } from './chat/useChatMaterials'
import { useChatPage } from './chat/useChatPage'
import { useChatPersona } from './chat/useChatPersona'
import { useChatSessions } from './chat/useChatSessions'
import { useChatSources } from './chat/useChatSources'

const route = useRoute()
const router = useRouter()

const HISTORY_CHUNK = 50

/* ── 各块状态 ────────────────────────────────────────────────────────── */

const sessionsApi = useChatSessions({
  route,
  router,
  // 当前会话被删：旧连接现在就拆，历史清空；路由跳转由会话层负责。
  onActiveRemoved: () => {
    connection.conversation.value?.close()
    connection.conversation.value = null
    connection.history.value = []
  },
})

const caps = useChatCapabilities()

const connection = useChatConnection({
  activeId: sessionsApi.activeId,
  capability: () => caps.capabilityName.value,
  stages: () => caps.stagePlan.value,
})

const persona = useChatPersona(route)
const materials = useChatMaterials(route, persona.selectedId)

const sourcesApi = useChatSources({
  router,
  activeId: sessionsApi.activeId,
  sources: connection.sources,
})

/* ── 行为：发送、建会话、排队、切会话、清理 ──────────────────────────── */

const {
  draft,
  composerError,
  headerTitle,
  panelStatus,
  startSession,
  onSend,
  onStop,
  newSession,
} = useChatPage({ route, sessionsApi, caps, connection, materials, persona })

/* ── 交给模板 ────────────────────────────────────────────────────────── */

const {
  sessions,
  sessionsLoading,
  sessionsLoadingMore,
  sessionsHasMore,
  sessionListError,
  activeId,
  loadSessions,
  onRename,
  onRemove,
} = sessionsApi

const { capabilityName } = caps

const {
  conversation,
  history,
  historyLoading,
  historyFailure,
  historyWindow,
  allBlocks,
  connectionStatus,
  running,
  waiting,
  reconnectAttempt,
  activeElsewhere,
  sources,
  citations,
  retryLeft,
  loadHistory,
} = connection

const {
  picker,
  filesLoading,
  filesFailure,
  selectedFileIds,
  kbLoading,
  kbFailure,
  selectedKbIds,
  selection,
  fileItems,
  kbItems,
  attachmentChips,
  kbChips,
  selectionChip,
  loadFiles,
  loadKnowledgeBases,
  openPicker,
  toggleFile,
  toggleKb,
} = materials

const {
  items: personaItems,
  selectedId: personaSelected,
  loading: personaLoading,
  failure: personaFailure,
  load: loadPersonas,
  select: selectPersona,
} = persona

const {
  panelTab,
  activeSource,
  activeMaterialId,
  materialResolving,
  openSource,
  onCitation,
  openReading,
  openFile,
} = sourcesApi
</script>
<template>
  <SurfaceGate surface="chat">
    <AppShell :busy="running || connectionStatus === 'reconnecting'" :panel-status="panelStatus">
      <template #list>
        <SessionListPane
          :sessions="sessions"
          :loading="sessionsLoading"
          :error="sessionListError"
          :active-id="activeId"
          :has-more="sessionsHasMore"
          :loading-more="sessionsLoadingMore"
          @new-session="newSession"
          @load-more="loadSessions(false)"
          @rename="onRename"
          @remove="onRemove"
          @retry="loadSessions(true)"
        />
      </template>

      <div class="main">
        <header class="main__head">
          <h1 class="main__title">{{ headerTitle }}</h1>

          <!--
            能力选择。它在这里不是装饰：**阶段名与阶段总数由所选能力决定**
            （§4.2）。选项整个来自 `/capabilities`，这里不筛也不排。
          -->
          <select
            v-if="capabilityList.length > 1"
            v-model="capabilityName"
            class="main__capability"
            :aria-label="CHAT.capabilityLabel"
          >
            <option v-for="manifest in capabilityList" :key="manifest.name" :value="manifest.name">
              {{ manifest.name }}
            </option>
          </select>
        </header>

        <div class="main__body">
          <!-- 首次失败且没有任何可显示的内容：一个带出口的失败态。 -->
          <FailureState
            v-if="historyFailure && allBlocks.length === 0"
            :failure="historyFailure"
            title="消息没有读出来"
            @retry="activeId && loadHistory(activeId)"
          />

          <!-- 有旧内容时失败只挂一条，不把内容换成错误页。 -->
          <PartialBanner
            v-else-if="historyFailure"
            :failure="historyFailure"
            text="消息没有刷新成功，显示的是上次读到的内容"
            @retry="activeId && loadHistory(activeId, true)"
          />

          <!-- 历史是一次性数组；这一层只控制一次画多少条。 -->
          <div v-if="history.length > historyWindow" class="earlier">
            <button
              type="button"
              class="earlier__button"
              @click="historyWindow = Math.min(history.length, historyWindow + HISTORY_CHUNK)"
            >
              {{ CHAT.loadEarlier }}
            </button>
          </div>

          <ConversationStream
            v-if="allBlocks.length > 0"
            :blocks="allBlocks"
            :citations="citations"
            @citation="onCitation"
            @open-source="openSource"
            @retry-unconfirmed="conversation?.retryUnconfirmed()"
          />

          <!-- 对话首页：没有会话，就摆几条真能点的问题（§7.1 第 3 条）。 -->
          <StarterPrompts v-else-if="activeId === null" @pick="startSession" />

          <PendingSkeleton v-else-if="historyLoading" />

          <EmptyState
            v-else
            title="这个对话还没有消息"
            hint="输入一句话，或者回到对话首页换一个开始。"
            action-label="回到对话首页"
            @action="router.push({ name: 'chat' })"
          />
        </div>

        <!--
          连接状态条。**失败与加载中不能长得像**（§6.2 硬规则 1），而且
          「正在重连」「被限流」「未确认发送」各是一句话，不合并。
        -->
        <div class="notices">
          <p v-if="connectionStatus === 'connecting'" class="banner banner--pending">
            {{ CHAT.connecting }}
          </p>
          <p
            v-else-if="connectionStatus === 'reconnecting'"
            class="banner banner--pending"
          >
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
          <p v-if="composerError" class="banner banner--error">{{ composerError }}</p>
        </div>

        <Composer
          :running="running && connectionStatus === 'open'"
          :waiting="waiting"
          :disabled="activeId !== null && connectionStatus !== 'open'"
          :placeholder="waiting ? CHAT.replyPlaceholder : CHAT.placeholder"
          :attachments="attachmentChips"
          :knowledge-bases="kbChips"
          :selection="selectionChip"
          :personas="personaItems"
          :persona="personaSelected"
          :persona-loading="personaLoading"
          :persona-failure="personaFailure !== null"
          :draft="draft"
          @send="onSend"
          @stop="onStop"
          @select-persona="selectPersona"
          @retry-personas="loadPersonas(true)"
          @manage-personas="router.push({ name: 'personas' })"
          @open-attachments="openPicker('attachments')"
          @open-knowledge-bases="openPicker('knowledgeBases')"
          @remove-attachment="toggleFile"
          @remove-knowledge-base="toggleKb"
          @clear-selection="selection = null"
        />
      </div>

      <template #panel="{ collapsed, toggle }">
        <SidePanel
          v-model:tab="panelTab"
          :collapsed="collapsed"
          :busy="running"
          :status="panelStatus"
          @toggle="toggle"
        >
          <template #activity>
            <ActivityTimeline :blocks="allBlocks" />
          </template>
          <template #sources>
            <SourcesPanel
              :sources="sources"
              @open="openSource"
              @go-files="router.push({ name: 'files' })"
            />
          </template>
          <template #preview>
            <SourcePreview
              :source="activeSource"
              :material-id="activeMaterialId"
              :resolving="materialResolving"
              @open-reading="openReading"
              @open-file="openFile"
              @clear="activeSource = null"
            />
          </template>
        </SidePanel>
      </template>
    </AppShell>

    <MaterialPicker
      :open="picker === 'attachments'"
      :title="CHAT.attachments"
      :hint="CHAT.attachmentsHint"
      :items="fileItems"
      :selected="selectedFileIds"
      :loading="filesLoading"
      :failure="filesFailure"
      :empty-title="CHAT.noParsedFiles"
      :empty-action="CHAT.goToFiles"
      @close="picker = null"
      @toggle="toggleFile"
      @retry="loadFiles(true)"
      @empty-action="picker = null; router.push({ name: 'files' })"
    />

    <MaterialPicker
      :open="picker === 'knowledgeBases'"
      :title="CHAT.knowledgeBases"
      :hint="CHAT.knowledgeBasesHint"
      :items="kbItems"
      :selected="selectedKbIds"
      :loading="kbLoading"
      :failure="kbFailure"
      :empty-title="CHAT.noKnowledgeBases"
      :empty-action="CHAT.goToKnowledgeBases"
      @close="picker = null"
      @toggle="toggleKb"
      @retry="loadKnowledgeBases(true)"
      @empty-action="picker = null; router.push({ name: 'knowledge-bases' })"
    />
  </SurfaceGate>
</template>

<style scoped src="./Chat.css"></style>
