<script setup lang="ts">
/**
 * 对话。`设计说明书.md` §4.2 的两条路径（`/chat` 与 `/chat/:sessionId`）都由
 * 这一个组件承担 —— 它们共用同一套四段式骨架，差别只在"有没有选中一个会话"。
 *
 * 这个组件是**协调者**：它持有会话列表、能力清单、当前那条连接，把渲染交给
 * 子组件。会话流、输入区、活动时间线、列表栏、起始问题各自只认识自己那一小块。
 *
 * 一条贯穿的规则：**界面上出现的东西都来自服务端**。阶段名与阶段总数来自
 * `/capabilities`，会话标题来自 `/sessions`（而且是**第一轮回答结束之后**才有
 * 的，所以新建的会话会先有一段无标题的时间），回答文字来自事件流。这里没有
 * 任何"默认先显示成什么"的编造。
 */

import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import ActivityTimeline from '../components/chat/ActivityTimeline.vue'
import AppShell from '../components/shell/AppShell.vue'
import Composer from '../components/chat/Composer.vue'
import ConversationStream from '../components/chat/ConversationStream.vue'
import PendingSkeleton from '../components/chat/PendingSkeleton.vue'
import SessionListPane from '../components/chat/SessionListPane.vue'
import { sessionLabel } from '../components/chat/session-label'
import SidePanel from '../components/shell/SidePanel.vue'
import StarterPrompts from '../components/chat/StarterPrompts.vue'

import { isApiFailure } from '../api/client'
import { describeFailure } from '../api/errors'
import { listCapabilities, type CapabilityManifest } from '../api/capabilities'
import { createSession, listMessages, listSessions, type SessionSummary } from '../api/sessions'
import type { Block } from '../ws/blocks'
import { createConversation, type Conversation } from '../ws/socket'
import { clearSession } from '../session'

const route = useRoute()
const router = useRouter()

/* ── 会话列表 ─────────────────────────────────────────────────────────── */

const sessions = ref<SessionSummary[]>([])
const sessionsLoading = ref(true)
const sessionListError = ref<string | null>(null)
/** 输入区上方那一句失败提示（建会话失败、连接没就绪）。 */
const composerError = ref<string | null>(null)

async function loadSessions(): Promise<void> {
  try {
    sessions.value = (await listSessions({ limit: 50 })).sessions
  } catch (cause) {
    if (handleCredentialLoss(cause)) {
      return
    }
    // 网络断了、被限流了、服务端给了个码 —— 各是各的话。这里不写一句"载入
    // 失败"把它们盖掉：被限流时用户该做的是等，断网时该做的是看连接。
    sessionListError.value = isApiFailure(cause)
      ? describeFailure(cause.failure)
      : '会话列表没能载入，请重试'
  } finally {
    sessionsLoading.value = false
  }
}

/* ── 能力 ────────────────────────────────────────────────────────────── */

const capabilities = ref<CapabilityManifest[]>([])
const capabilityName = ref('')

const activeCapability = computed(
  () => capabilities.value.find((manifest) => manifest.name === capabilityName.value) ?? null,
)

/** 阶段的顺序表。放进连接是为了把 `stage_start` 渲染成"第几步 / 共几步"。 */
const stagePlan = computed<readonly string[]>(() => activeCapability.value?.stages ?? [])

async function loadCapabilities(): Promise<void> {
  try {
    capabilities.value = (await listCapabilities()).capabilities
    // 默认落在 `chat` 上；没有就叫第一个。**这不是把阶段写死** —— 阶段的名单
    // 仍然整个来自上面这一份响应，这里只是挑一个起点。
    capabilityName.value =
      capabilities.value.find((manifest) => manifest.name === 'chat')?.name ??
      capabilities.value[0]?.name ??
      ''
  } catch (cause) {
    handleCredentialLoss(cause)
  }
}

/* ── 这条连接 ────────────────────────────────────────────────────────── */

const conversation = shallowRef<Conversation | null>(null)
const historyLoading = ref(false)
/** 会话建立后马上要发出去的那一句。见 `startSession`。 */
let queued: string | null = null

const blocks = computed<readonly Block[]>(() => conversation.value?.blocks ?? [])
const connectionStatus = computed(() => conversation.value?.status ?? 'idle')
const running = computed(() => conversation.value?.running ?? false)
const waiting = computed(() => conversation.value?.waiting ?? false)

const activeId = computed(() => {
  const id = route.params.sessionId
  return typeof id === 'string' && id !== '' ? id : null
})

const activeSession = computed(
  () => sessions.value.find((session) => session.session_id === activeId.value) ?? null,
)

/** 内容区头部的那一行。没有选中会话时是"对话首页"，所以没有标题。 */
const headerTitle = computed(() =>
  activeSession.value ? sessionLabel(activeSession.value) : '',
)

/**
 * 收合后那条细栏上的一句话。§3.5 要的是"用户能随时知道 AI 还在干活 / 干完了"，
 * 所以它报的是**当前状态**，不是一句说明。
 */
const panelStatus = computed(() => {
  if (running.value) {
    const lastStage = [...blocks.value].reverse().find((block) => block.kind === 'stage')
    return lastStage && lastStage.kind === 'stage' ? lastStage.name : '进行中'
  }
  if (waiting.value) {
    return '等你回答'
  }
  return '空闲'
})

/**
 * 凭据没了：拆连接、清本地、回登录页。
 *
 * `api/client.ts` 把 401 标成 `unauthorized` 就是为了这一处 —— 它与其他失败
 * 的出口不同，必须走这一条。返回 true 表示"这事我处理了，调用方别再报错"。
 */
function handleCredentialLoss(cause: unknown): boolean {
  if (!isApiFailure(cause) || cause.failure.kind !== 'unauthorized') {
    return false
  }
  conversation.value?.close()
  conversation.value = null
  clearSession()
  void router.replace({ name: 'login' })
  return true
}

/**
 * 换一条连接。**旧的必须先拆**：登出、切换会话、离开页面都不会让服务端那条
 * 连接自己断（《前端设计注意事项》§1.3），留着它就是一个还在收事件、还在占用
 * 服务端订阅的幽灵。
 */
function openConversation(sessionId: string): void {
  conversation.value?.close()
  // 能力与阶段表是**取值函数**，不是快照：界面上那个选择器改了之后，下一轮就
  // 该用新的那个，不必先重连一次（见 `socket.ts` 的 `TurnContext`）。
  const next = createConversation(sessionId, {
    capability: () => capabilityName.value,
    stages: () => stagePlan.value,
  })
  conversation.value = next

  void next.connect().then(() => {
    if (queued !== null && next.status === 'open') {
      const text = queued
      queued = null
      next.send(text)
    }
  })
}

async function loadHistory(sessionId: string): Promise<void> {
  historyLoading.value = true
  try {
    const { messages } = await listMessages(sessionId)
    // 连着切会话时会有两次读同时在飞，回来的顺序不保证。**只认还在屏幕上的那
    // 一个** —— 否则慢的那次会把已经打开的那个会话的历史盖掉。
    if (activeId.value !== sessionId) {
      return
    }
    // 历史只在**刚打开这个会话**时铺一次。之后的事件往同一份块列表上追加 ——
    // 重新拉一次会把正在流的那一轮冲掉。
    const seeded: Block[] = messages.map((message) =>
      message.role === 'user'
        ? { kind: 'user', text: message.content }
        : { kind: 'answer', text: message.content, complete: true },
    )
    history.value = seeded
  } catch (cause) {
    if (handleCredentialLoss(cause)) {
      return
    }
    if (activeId.value === sessionId) {
      history.value = []
    }
  } finally {
    if (activeId.value === sessionId) {
      historyLoading.value = false
    }
  }
}

/**
 * 切到某个会话：先读历史，再建连接。
 *
 * **读回来之后要确认还在这个会话上**：读的路上用户可能又换了一个（或者离开了
 * 这一页），那是另一次 `watch` 的活。不加这一句就会给一个已经不在屏幕上的会话
 * 建一条连接 —— 服务端多一个订阅者，而用户看不到任何东西。
 */
async function openSession(sessionId: string): Promise<void> {
  await loadHistory(sessionId)
  if (activeId.value === sessionId) {
    openConversation(sessionId)
  }
}

/**
 * 已落库的那部分会话流。它与连接上的实时块是**两段**，因为它们的来源不同：
 * 历史来自一次读，实时来自事件。渲染时先历史后实时 —— 一个会话被重新打开时，
 * 实时块从空开始，历史把前面填上。
 */
const history = ref<Block[]>([])

const allBlocks = computed<readonly Block[]>(() => [...history.value, ...blocks.value])

/* ── 动作 ────────────────────────────────────────────────────────────── */

async function startSession(firstMessage: string): Promise<void> {
  try {
    const created = await createSession()
    // 先塞进列表再跳转：列表行在跳过去的那一刻就要在，否则选中的会话会在列表
    // 里缺席几十毫秒。标题是空的，`SessionListPane` 有形状接住它。
    sessions.value = [created, ...sessions.value]
    queued = firstMessage
    await router.push({ name: 'conversation', params: { sessionId: created.session_id } })
  } catch (cause) {
    if (!handleCredentialLoss(cause)) {
      composerError.value = isApiFailure(cause)
        ? describeFailure(cause.failure)
        : '新会话没能建起来，请重试'
    }
  }
}

function onSend(text: string): void {
  composerError.value = null
  if (activeId.value === null) {
    void startSession(text)
    return
  }
  const current = conversation.value
  if (!current || current.status !== 'open') {
    composerError.value = '连接还没建立好，请稍候再试'
    return
  }
  // 服务端在等一个回答时，发出去的是 `submit_reply`。发成新一轮会被拒
  // （`active_turn_conflict`）—— 那不是用户能看懂的失败。
  if (current.waiting) {
    current.reply(text)
    return
  }
  current.send(text)
}

function onStop(): void {
  conversation.value?.cancel()
}

function newSession(): void {
  if (activeId.value === null) {
    return
  }
  void router.push({ name: 'chat' })
}

/* ── 生命周期 ────────────────────────────────────────────────────────── */

onMounted(async () => {
  await Promise.all([loadCapabilities(), loadSessions()])
  if (activeId.value) {
    await openSession(activeId.value)
  }
})

watch(activeId, (id, previous) => {
  if (id === previous) {
    return
  }
  // **旧连接现在就拆**，不能等到 `openConversation` —— 历史要一次往返才回来，
  // 而 `openConversation` 排在它后面，于是中间那段时间 `conversation` 还指着上
  // 一个会话：标题已经换成新的了，正文还是旧的，上一个会话要是还在跑，它的流
  // 还会继续往那儿追加。拆掉之后 `allBlocks` 为空，屏幕上落到骨架条那一支。
  conversation.value?.close()
  conversation.value = null
  history.value = []
  if (id === null) {
    historyLoading.value = false
    return
  }
  void openSession(id)
})

// 离开对话页（比如登出后被推到登录页）要拆掉连接。放在这里而不是 `onUnmounted`
// 是为了让 `keep-alive` 之类将来也不会留下一条活着的连接。
onBeforeUnmount(() => {
  conversation.value?.close()
  conversation.value = null
})

/**
 * 每一轮跑完之后都刷新一次列表 —— 会话的标题是**服务端在这一轮结束之后才生成
 * 的**（§3.2），不重新读就永远看不到它。§3.2 要求"标题到达后就地更新，不要让
 * 整行闪一下重新加载"，所以这里是替换整份列表，而不是先清空再填：清空再填就
 * 是它说的那次闪烁。
 *
 * 盯的是**跑完的轮数**，不是"有没有出现过一轮跑完"。标题只在**跑完**的那一轮
 * 生成（`turn/ending.go`：`titleNeeded && completed`），所以第一轮要是被取消
 * 或失败了，名字是后面某一轮给的。用一个 false→true 的布尔盯，第一轮就把这个
 * 判断耗掉了，后面那轮真正写出标题时不会再触发 —— 列表行会一直停在创建时间
 * 上，直到手动刷新。轮数还会顺带把列表的 `updated_at` 排序带上。
 */
const finishedTurns = computed(() => blocks.value.filter((block) => block.kind === 'done').length)

watch(finishedTurns, (count, previous) => {
  // 换会话时这个数会掉回 0（`blocks` 是一份新的、空的），那不算跑完了一轮。
  if (count > previous) {
    void loadSessions()
  }
})
</script>

<template>
  <AppShell :busy="running" :panel-status="panelStatus">
    <template #list>
      <SessionListPane
        :sessions="sessions"
        :loading="sessionsLoading"
        :error="sessionListError"
        @new-session="newSession"
      />
    </template>

    <div class="main">
      <header v-if="headerTitle" class="main__head">
        <h1 class="main__title">{{ headerTitle }}</h1>

        <!--
          能力选择。它在这里不是装饰：**阶段名与阶段总数由所选能力决定**（§4.2），
          换一个能力，流里的阶段指示就换一套名字与个数。选项整个来自
          `/capabilities`（今天回六个），这里不筛也不排 —— 服务端注册了哪些，
          选择器里就有哪些。
        -->
        <select v-if="capabilities.length > 1" v-model="capabilityName" class="main__capability" aria-label="本轮用哪个能力">
          <option v-for="manifest in capabilities" :key="manifest.name" :value="manifest.name">
            {{ manifest.name }}
          </option>
        </select>
      </header>

      <div class="main__body">
        <ConversationStream v-if="allBlocks.length > 0" :blocks="allBlocks" />

        <!-- 对话首页：没有会话，就摆几条真能点的问题（§7.1 第 3 条）。 -->
        <StarterPrompts v-else-if="activeId === null" @pick="startSession" />

        <PendingSkeleton v-else-if="historyLoading" />
      </div>

      <!--
        连接状态条。**失败与加载中不能长得像**（§6.2 硬规则 1），而且
        `refused`（没建起来）与 `lost`（连上又断）在这里也不合并成一句。
      -->
      <p v-if="connectionStatus === 'connecting'" class="banner banner--pending">正在连接…</p>
      <p v-else-if="connectionStatus === 'refused'" class="banner banner--error">
        连接没能建立，请稍后重试
      </p>
      <p v-else-if="connectionStatus === 'lost'" class="banner banner--error">
        连接已断开
      </p>
      <p v-else-if="composerError" class="banner banner--error">{{ composerError }}</p>

      <Composer
        :running="running"
        :waiting="waiting"
        :disabled="activeId !== null && connectionStatus !== 'open'"
        :placeholder="waiting ? '回答上面的问题' : '问点什么'"
        @send="onSend"
        @stop="onStop"
      />
    </div>

    <template #panel="{ collapsed, toggle }">
      <SidePanel :collapsed="collapsed" :busy="running" :status="panelStatus" @toggle="toggle">
        <template #activity>
          <ActivityTimeline :blocks="allBlocks" />
        </template>
        <template #sources>
          <!-- §7.1 第 2 条：空状态给动作，不给解释。这一版没有引用可显示，所以
               这里什么都不放 —— 不放一段"本轮还没有引用"。 -->
        </template>
        <template #preview>
          <!-- 同上。文件预览要等材料那条线做出来。 -->
        </template>
      </SidePanel>
    </template>
  </AppShell>
</template>

<style scoped>
.main {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.main__head {
  display: flex;
  gap: 12px;
  align-items: center;
  justify-content: space-between;
  padding: 13px 24px;
  border-bottom: 1px solid var(--border);
  background: var(--surface);
}

.main__title {
  overflow: hidden;
  font-size: 15px;
  font-weight: 650;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.main__capability {
  flex: none;
  padding: 4px 6px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  font-size: 13px;
}

.main__body {
  flex: 1;
  overflow-y: auto;
}

.banner {
  margin: 0 24px 8px;
  padding: 7px 11px;
  border-radius: var(--radius-sm);
  font-size: 13px;
}

.banner--pending {
  background: var(--surface-sunken);
  color: var(--text-muted);
}

.banner--error {
  background: var(--danger-soft);
  color: var(--danger);
}
</style>
