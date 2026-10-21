<script setup lang="ts">
/**
 * 对话工作台：列表栏 + 主区 + 右侧面板 + 输入区。
 *
 * 对话页、写作页与路径对话页是同一套工作台，只在四件事上不同：
 *
 * 1. **列哪一种会话** —— `kind` 决定列表只读 `chat`、`writing` 还是 `mastery`；
 * 2. **用哪一种能力** —— 给了 `capability` 就固定用它、不画选择器（路径对话页
 *    固定 `mastery_path`）；不给则按清单默认，写作页就是 `chat`；
 * 3. **首页是什么** —— 由 `#home` 插槽给出（对话页是起始提示，写作页是准备表，
 *    路径对话页是想学什么的入口）；
 * 4. **会话地址** —— `sessionRoute` 决定点开一行、新建一条之后往哪个路由名去。
 *
 * 这样各页面不必复制输入区、状态条、引用面板与断线处理：那些行为一旦分成几
 * 份，迟早只有一边跟上后端的协议变化。
 *
 * 权限面仍然是 `chat`：写作与路径对话都是用法，不是新的授权面。
 */

import { useTemplateRef } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { capabilityList } from '../../shared/capabilities'
import { CHAT } from '../../copy/chat'
import { CO_WRITER } from '../../copy/collaboration'

import AppShell from '../../components/shell/AppShell.vue'
import SidePanel from '../../components/shell/SidePanel.vue'
import SurfaceGate from '../../components/common/SurfaceGate.vue'
import EmptyState from '../../components/common/EmptyState.vue'
import FailureState from '../../components/common/FailureState.vue'
import PartialBanner from '../../components/common/PartialBanner.vue'
import PendingSkeleton from '../../components/chat/PendingSkeleton.vue'
import ClarifyDialog from '../../components/chat/ClarifyDialog.vue'
import MasteryQuestionDialog from '../../components/chat/MasteryQuestionDialog.vue'
import ChatNotices from '../../components/chat/ChatNotices.vue'
import SessionListPane from '../../components/chat/SessionListPane.vue'
import ConversationStream from '../../components/chat/ConversationStream.vue'
import ActivityTimeline from '../../components/chat/ActivityTimeline.vue'
import Composer, { type PersonaChoice } from '../../components/chat/Composer.vue'
import MaterialPicker from '../../components/chat/MaterialPicker.vue'
import SourcesPanel from '../../components/chat/SourcesPanel.vue'
import SourcePreview from '../../components/chat/SourcePreview.vue'

import type { SessionKind } from '../../api/sessions'
import type { RouteLocationRaw } from 'vue-router'

import { useChatCapabilities } from './useChatCapabilities'
import { useChatClarify } from './useChatClarify'
import { useChatConnection } from './useChatConnection'
import { useChatMaterials } from './useChatMaterials'
import { useChatPage } from './useChatPage'
import { useChatPersona } from './useChatPersona'
import { useChatSessions } from './useChatSessions'
import { useChatSources } from './useChatSources'
import { useMasteryQuestion } from './useMasteryQuestion'

const props = withDefaults(
  defineProps<{
    /** 这个工作台只读写哪一种会话。 */
    kind: SessionKind
    /** 新建会话、删掉当前会话之后回到哪个地址。 */
    homeRoute: RouteLocationRaw
    /** 打开一个会话时用的路由名。 */
    sessionRoute: string
    /** 首页（没有选中会话时）标题行的文字。 */
    homeTitle?: string
    /** 能力选择器。写作页不提供：写作只用 `chat` 能力。 */
    showCapability?: boolean
    /** 固定使用的能力名。给了它就隐藏选择器，每一轮都用它。 */
    capability?: string
    /** 首页是否也显示输入区。对话页要（可以直接问），写作页不要（先填准备表）。 */
    composerOnHome?: boolean
    /** 输入框占位文案。 */
    placeholder?: string
    /** 列表栏标题与空态文案。写作页要的是「写作」那一套。 */
    listTitle?: string
    listEmptyAction?: string
    listNewLabel?: string
    /** 打开一条没有消息的会话时那几句话。写作页不能自称「对话」。 */
    emptyTitle?: string
    emptyHint?: string
    emptyActionLabel?: string
  }>(),
  {
    homeTitle: '',
    showCapability: true,
    capability: '',
    composerOnHome: true,
    placeholder: CHAT.placeholder,
    listTitle: '对话',
    listEmptyAction: '开始一段对话',
    listNewLabel: '新建会话',
    emptyTitle: '这个对话还没有消息',
    emptyHint: '输入一句话，或者回到首页换一个开始。',
    emptyActionLabel: '回到首页',
  },
)

defineSlots<{
  home(props: {
    startSession: (text: string) => Promise<void>
    starting: boolean
    startError: string | null
    personaItems: readonly PersonaChoice[]
    personaId: string
    personaLoading: boolean
    personaFailure: boolean
    selectPersona: (id: string) => void
    retryPersonas: () => void
    managePersonas: () => void
  }): unknown
}>()

const route = useRoute()
const router = useRouter()

const HISTORY_CHUNK = 50

/* ── 各块状态 ────────────────────────────────────────────────────────── */

const sessionsApi = useChatSessions({
  route,
  router,
  kind: props.kind,
  homeRoute: props.homeRoute,
  // 当前会话被删：旧连接现在就拆，历史清空；路由跳转由会话层负责。
  onActiveRemoved: () => {
    connection.conversation.value?.close()
    connection.conversation.value = null
    connection.history.value = []
  },
})

const caps = useChatCapabilities(props.capability)

const connection = useChatConnection({
  activeId: sessionsApi.activeId,
  capability: () => caps.capabilityName.value,
  stages: () => caps.stagePlan.value,
})

const persona = useChatPersona(route)
const materials = useChatMaterials(route, persona.selectedId)

/**
 * 掌握路径的出题弹窗。只有路径对话页用它：别的页面即便碰巧跑了
 * `mastery_path` 能力，也不该在对话页弹一道题出来。
 */
const {
  question: masteryQuestion,
  bank: masteryQuestionBank,
  visible: masteryQuestionVisible,
  dismiss: dismissMasteryQuestion,
  reopen: reopenMasteryQuestion,
} = useMasteryQuestion({
  blocks: connection.blocks,
  running: connection.running,
  waiting: connection.waiting,
  activeId: sessionsApi.activeId,
})

const sourcesApi = useChatSources({
  router,
  activeId: sessionsApi.activeId,
  sources: connection.sources,
})

/* ── 行为：发送、建会话、排队、切会话、清理 ──────────────────────────── */

const {
  draft,
  composerError,
  starting,
  headerTitle,
  panelStatus,
  startSession,
  onSend,
  onStop,
  newSession,
} = useChatPage({
  route,
  sessionsApi,
  caps,
  connection,
  materials,
  persona,
  kind: props.kind,
  homeRoute: props.homeRoute,
  sessionRoute: props.sessionRoute,
  draftEnabled: props.kind === 'chat',
})

/**
 * 出题弹窗的答案就是一条普通的用户消息：模型下一轮拿它去 `mastery_grade`
 * 判卷。这里不另开一条接口，与澄清弹窗走输入区是同一条规矩。
 */
function submitMasteryAnswer(answer: string): void {
  dismissMasteryQuestion()
  onSend(answer)
}

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

const clarifyDialog = useTemplateRef<InstanceType<typeof ClarifyDialog>>('clarifyDialog')
const { question: pendingQuestion, reopen: reopenQuestion } = useChatClarify(
  connection.blocks,
  clarifyDialog,
)

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
          :title="listTitle"
          :empty-action="listEmptyAction"
          :new-label="listNewLabel"
          :row-route-name="sessionRoute"
          @new-session="newSession"
          @load-more="loadSessions(false)"
          @rename="onRename"
          @remove="onRemove"
          @retry="loadSessions(true)"
        />
      </template>

      <div class="main" :class="{ 'main--writing': kind === 'writing' }">
        <header class="main__head">
          <h1 class="main__title">{{ activeId === null ? homeTitle : headerTitle }}</h1>

          <!--
            能力选择。它在这里不是装饰：**阶段名与阶段总数由所选能力决定**
            （§4.2）。选项整个来自 `/capabilities`，这里不筛也不排。
          -->
          <select
            v-if="showCapability && capability === '' && capabilityList.length > 1"
            v-model="capabilityName"
            class="main__capability"
            :aria-label="CHAT.capabilityLabel"
          >
            <option v-for="manifest in capabilityList" :key="manifest.name" :value="manifest.name">
              {{ manifest.name }}
            </option>
          </select>

          <!--
            写作会话的退出动作：回到准备页再开一段，或从左侧列表换一段。
            它只导航，不删除这条会话——历史留在写作列表里。
          -->
          <button
            v-if="kind === 'writing' && activeId !== null"
            type="button"
            class="main__end"
            @click="router.push(homeRoute)"
          >
            {{ CO_WRITER.endWriting }}
          </button>
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

          <!-- 没有会话时，页面自己的首页。对话页放起始提示，写作页放准备表。 -->
          <slot
            v-else-if="activeId === null"
            name="home"
            :start-session="startSession"
            :starting="starting"
            :start-error="composerError"
            :persona-items="personaItems"
            :persona-id="personaSelected"
            :persona-loading="personaLoading"
            :persona-failure="personaFailure !== null"
            :select-persona="selectPersona"
            :retry-personas="() => loadPersonas(true)"
            :manage-personas="() => router.push({ name: 'personas' })"
          />

          <PendingSkeleton v-else-if="historyLoading" />

          <EmptyState
            v-else
            :title="emptyTitle"
            :hint="emptyHint"
            :action-label="emptyActionLabel"
            @action="router.push(homeRoute)"
          />
        </div>

        <!--
          连接状态条。**失败与加载中不能长得像**（§6.2 硬规则 1），而且
          「正在重连」「被限流」「未确认发送」各是一句话，不合并。
        -->
        <ChatNotices
          :connection-status="connectionStatus"
          :reconnect-attempt="reconnectAttempt"
          :retry-left="retryLeft"
          :active-elsewhere="activeElsewhere"
          :composer-error="composerError"
          :show-composer-error="activeId !== null || composerOnHome"
          :waiting="waiting"
          :mastery-question-pending="
            kind === 'mastery' &&
            masteryQuestion !== null &&
            !masteryQuestionVisible &&
            !running &&
            !waiting
          "
          @reopen-question="reopenQuestion"
          @reopen-mastery-question="reopenMasteryQuestion"
        />

        <Composer
          v-if="activeId !== null || composerOnHome"
          :running="running && connectionStatus === 'open'"
          :waiting="waiting"
          :disabled="activeId !== null && connectionStatus !== 'open'"
          :placeholder="waiting ? CHAT.replyPlaceholder : placeholder"
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

    <!--
      澄清提问。服务端在回合中间停下来时自动弹出；提交走的是与输入区完全
      相同的 `submit_reply`（`onSend` 会看 `waiting`），不另开一条路径。
    -->
    <ClarifyDialog
      ref="clarifyDialog"
      :waiting="waiting"
      :question="pendingQuestion"
      :disabled="connectionStatus !== 'open'"
      @submit="onSend"
    />

    <!--
      出题弹窗。只有路径对话页挂它：`mastery_question` 是掌握路径能力里的
      工具，题目在调用参数里，答案走与输入区相同的发送路径。
    -->
    <MasteryQuestionDialog
      v-if="kind === 'mastery'"
      :question="masteryQuestion"
      :bank="masteryQuestionBank"
      :open="masteryQuestionVisible"
      :disabled="connectionStatus !== 'open'"
      @submit="submitMasteryAnswer"
      @dismiss="dismissMasteryQuestion"
    />
  </SurfaceGate>
</template>

<style scoped src="../Chat.css"></style>
