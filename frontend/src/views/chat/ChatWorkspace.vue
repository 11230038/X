<script setup lang="ts">
/**
 * 对话工作台：列表栏 + 主区 + 右侧面板 + 输入区。
 *
 * 对话页、写作页与路径对话页是同一套工作台，只在五件事上不同：
 *
 * 1. **列哪一种会话** —— `kind` 决定列表只读 `chat`、`writing` 还是 `mastery`；
 *    路径对话的二级页不画这一列（`showList=false`），它的一级页就是历史；
 * 2. **用哪一种能力** —— 给了 `capability` 就固定用它、不画选择器（路径对话页
 *    固定 `mastery_path`）；不给则按清单默认，写作页就是 `chat`；
 * 3. **用哪个人格** —— 给了 `personaLock` 就每一轮都用它（题目问答页锁死
 *    「题目问答伙伴」）；`showPersona=false` 时既不给选择器也不带人格（路径
 *    对话页）；不给则自由选择；
 * 4. **首页是什么** —— 由 `#home` 插槽给出（对话页是起始提示，写作页是准备表，
 *    路径对话页是想学什么的入口）；
 * 5. **会话地址** —— `sessionRoute` 决定点开一行、新建一条之后往哪个路由名去。
 *
 * 这样各页面不必复制输入区、状态条、引用面板与断线处理：那些行为一旦分成几
 * 份，迟早只有一边跟上后端的协议变化。
 *
 * 权限面仍然是 `chat`：写作与路径对话都是用法，不是新的授权面。
 */

import { computed, useTemplateRef } from 'vue'
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
import { useChatPersona, type PersonaLock } from './useChatPersona'
import { useChatSessions } from './useChatSessions'
import { useChatSources } from './useChatSources'
import { useMasteryQuestion } from './useMasteryQuestion'
import AppIcon, { type AppIconName } from '../../components/common/AppIcon.vue'

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
    /**
     * 顶栏左侧的返回上一级链接。给了它就在模式胶囊之前画一枚回到
     * `homeRoute` 的链接；只有二级页面（路径对话）需要，别处留空。
     */
    backLabel?: string
    /** 能力选择器。写作页不提供：写作只用 `chat` 能力。 */
    showCapability?: boolean
    /** 固定使用的能力名。给了它就隐藏选择器，每一轮都用它。 */
    capability?: string
    /**
     * 锁死的人格。给了它，每一轮都带上这一个人格，输入区不提供切换；
     * 账号里没有同名人格时按这份内容建一个。题目问答页用它。
     */
    personaLock?: PersonaLock
    /**
     * 这一页是否用得到人格。路径对话不需要：不给选择器，也不把人格带进任何
     * 一轮（连人格列表都不读）。
     */
    showPersona?: boolean
    /** 首页是否也显示输入区。对话页要（可以直接问），写作页不要（先填准备表）。 */
    composerOnHome?: boolean
    /**
     * 是否画工作台自带的会话列表。路径对话不要：它的一级页就是对话历史，
     * 二级再列一遍就是同一份列表出现两次。
     */
    showList?: boolean
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
    backLabel: '',
    showCapability: true,
    capability: '',
    showPersona: true,
    showList: true,
    composerOnHome: true,
    placeholder: CHAT.placeholder,
    listTitle: '历史会话',
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
  /**
   * 已选中一段还没有消息的会话时画什么。路径对话放进它的开场提示，
   * 与真正的新会话是同一种开始；别的页面用默认的空状态。
   */
  empty(props: { send: (text: string) => void }): unknown
  /** 输入区工具行里的页面私有按钮（题目问答的「选择题目」）。 */
  'composer-tools'(props: {
    send: (text: string) => void
    setComposerText: (text: string) => void
    getComposerText: () => string
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

const persona = useChatPersona(route, props.personaLock, props.showPersona)
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
 * 输入区的两个把手，给页面私有的工具按钮用。
 *
 * 题目问答的「选择题目」在输入区的工具行里：选中的题要写进输入框，用户能
 * 看见、能接着改。`startSession` 也一并暴露，是因为从练习页/题库页带题号
 * 进来时要直接发，不必先经过输入框。
 */
const composerRef = useTemplateRef<InstanceType<typeof Composer>>('composer')

function setComposerText(text: string): void {
  composerRef.value?.replace(text)
}

function getComposerText(): string {
  return composerRef.value?.value() ?? ''
}

defineExpose({ startSession, setComposerText, getComposerText })

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

/**
 * 对话顶栏的模式胶囊，照演示稿的 `.chat-modes`：通用辅导 / 写作教练 /
 * 题目精解。三个都是真页面（对话、协同写作、题目问答），所以这里用链接而不是
 * 一个假的切换开关 —— 点一下真的换到那一套工作台。
 */
const MODES: readonly { kind: string; label: string; icon: AppIconName; to: RouteLocationRaw }[] = [
  { kind: 'chat', label: '通用辅导', icon: 'chat', to: { name: 'chat' } },
  { kind: 'writing', label: '写作教练', icon: 'practice', to: { name: 'co-writer' } },
  { kind: 'question_bank', label: '题目精解', icon: 'puzzle', to: { name: 'question-chat' } },
]

/** 顶栏人格下拉。模板里不做类型断言，取值的活放在这里。 */
function onPersonaPick(event: Event): void {
  selectPersona((event.target as HTMLSelectElement).value)
}

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

/**
 * 会话流里不画"阶段第几步"（`✓ exploring 第 1/2 步` 那种）：它是**动作**，不是
 * 内容，右侧「活动」时间线已经逐条列着。两个视图仍读同一份 `allBlocks` —— 只是
 * 流这边把阶段摘掉，面板那边不动。
 *
 * 过滤放在这一层而不是让 `ConversationStream` 视而不见：块还在数组里的话，那个
 * `<li>` 仍会占掉一行 24px 的行距，流里会凭空多出几段空隙。
 */
const streamBlocks = computed(() => allBlocks.value.filter((block) => block.kind !== 'stage'))

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
      <template v-if="showList" #list>
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

      <div class="main">
        <header class="main__head">
          <div class="main__head-left">
            <!--
              二级页面的返回入口。它是导航不是动作：回到上一级的列表，不删除
              这段会话。一级页面没有上一级，`backLabel` 为空时不画。
            -->
            <RouterLink v-if="backLabel" class="main__back" :to="homeRoute">
              <span aria-hidden="true">←</span>
              {{ backLabel }}
            </RouterLink>

            <!--
              模式胶囊，照演示稿的 `.chat-modes`：通用辅导 / 写作教练 / 题目精解。
              三个都是真页面，所以是链接，不是同一个页面里的开关。
            -->
            <nav v-if="kind !== 'mastery'" class="chat-modes" aria-label="对话模式">
              <RouterLink
                v-for="mode in MODES"
                :key="mode.kind"
                class="mode-btn"
                :class="{ 'mode-btn--active': kind === mode.kind }"
                :to="mode.to"
              >
                <AppIcon :name="mode.icon" /> {{ mode.label }}
              </RouterLink>
            </nav>

            <!--
              路径对话固定用 `mastery_path`，不提供模式切换：换模式就不是路径对话
              了。演示稿在这里也是一枚只读的能力标签。
            -->
            <span v-else class="chat-mode-tag">{{ capabilityName }}</span>
          </div>

          <div class="main__head-right">
            <!--
              人格选择在顶栏，照演示稿的 `.partner-selector`：它是"这段对话用哪种
              说法"，选了通常连着用几轮，所以不放在输入框里当一轮的材料。
              题目问答锁死人格时就退成一枚只读标签。
            -->
            <template v-if="showPersona">
              <span
                v-if="personaLock !== undefined"
                class="partner-selector partner-selector--locked"
              >
                <span class="partner-avatar" aria-hidden="true"><AppIcon name="partners" tone="ink" /></span>
                <span class="partner-name">{{ personaLock.name }}</span>
              </span>
              <label v-else class="partner-selector">
                <span class="partner-avatar" aria-hidden="true"><AppIcon name="partners" tone="ink" /></span>
                <select
                  class="partner-name partner-select"
                  :value="personaSelected"
                  :disabled="personaLoading"
                  aria-label="人格"
                  @change="onPersonaPick"
                >
                  <option value="">
                    {{ personaLoading ? '正在读取人格…' : '不指定人格' }}
                  </option>
                  <option v-for="item in personaItems" :key="item.id" :value="item.id">
                    {{ item.label }}
                  </option>
                </select>
                <button
                  type="button"
                  class="partner-manage"
                  :title="'管理人格'"
                  @click="router.push({ name: 'partners' })"
                >
                  ⋯
                </button>
              </label>
            </template>

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
          </div>
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
            :blocks="streamBlocks"
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
            :manage-personas="() => router.push({ name: 'partners' })"
          />

          <PendingSkeleton v-else-if="historyLoading" />

          <slot v-else name="empty" :send="onSend">
            <EmptyState
              :title="emptyTitle"
              :hint="emptyHint"
              :action-label="emptyActionLabel"
              @action="router.push(homeRoute)"
            />
          </slot>
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
          ref="composer"
          :running="running && connectionStatus === 'open'"
          :waiting="waiting"
          :disabled="activeId !== null && connectionStatus !== 'open'"
          :placeholder="waiting ? CHAT.replyPlaceholder : placeholder"
          :attachments="attachmentChips"
          :knowledge-bases="kbChips"
          :selection="selectionChip"
          :draft="draft"
          @send="onSend"
          @stop="onStop"
          @open-attachments="openPicker('attachments')"
          @open-knowledge-bases="openPicker('knowledgeBases')"
          @remove-attachment="toggleFile"
          @remove-knowledge-base="toggleKb"
          @clear-selection="selection = null"
        >
          <template #tools>
            <!--
              能力选择。它在这里不是装饰：**阶段名与阶段总数由所选能力决定**
              （§4.2），选项整个来自 `/capabilities`，这里不筛也不排。

              它排在输入框上方而不是顶栏：演示稿的顶栏只装得下模式胶囊和人格选择，
              再塞一枚能力选择就会把人格的名字挤掉。这三样都是"这一轮怎么问"，
              和附件、知识库同一排也说得通。
            -->
            <label
              v-if="showCapability && capability === '' && capabilityList.length > 1"
              class="cap-tool"
            >
              <span class="cap-tool__label">能力</span>
              <select
                v-model="capabilityName"
                class="cap-tool__select"
                :aria-label="CHAT.capabilityLabel"
              >
                <option v-for="manifest in capabilityList" :key="manifest.name" :value="manifest.name">
                  {{ manifest.name }}
                </option>
              </select>
            </label>

            <slot
              name="composer-tools"
              :send="onSend"
              :set-composer-text="setComposerText"
              :get-composer-text="getComposerText"
            />
          </template>
        </Composer>
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
