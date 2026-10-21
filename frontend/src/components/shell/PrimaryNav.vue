<script setup lang="ts">
/**
 * 一级导航。`设计说明书.md` §2.2 的九个分组，§3.1 的两条形态要求。
 *
 * 这一版把一级入口收成六个：学习、对话、掌握路径、题库、数据库、其他。学习
 * 首页是日常起点，置顶；写作挂在对话下面，学习地图挂在掌握路径下面，练习挂在
 * 题库下面，活书与沉浸阅读挂在数据库下面，作为二级入口；原来的其余分组暂时收进
 * 「其他」，作为它的二级入口 —— 这是前端实现上的临时归并，不改路由，也不
 * 改权限面。**不按权限隐藏入口** ——
 * 进得去却看到"没有权限"比导航里少一项更容易理解，而且权限快照可能还没到。
 *
 * 管理台不在导航里：它不是日常入口，放在头像菜单里，且只有管理员看得到。
 *
 * 图标是内联 SVG，没有引图标库。§2.2 要求**图标必须配文字**，所以图标是
 * 辅助，文字才是主体 —— 收合态才只剩图标，且悬停出名称（§3.1）。
 */

import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { currentAccount, clearSession } from '../../session'
import { logout } from '../../api/auth'
import { canSurface, isAdmin, resetPermissions } from '../../auth/permissions'
import { resetCapabilities } from '../../shared/capabilities'

interface NavEntry {
  key: string
  label: string
  to: string
}

/**
 * 一级入口。顺序按产品这一版的要求：学习置顶，其后是对话、掌握路径、题库、
 * 数据库，「其他」收在末尾。
 */
const primaryEntries: readonly NavEntry[] = [
  { key: 'learning', label: '学习', to: '/learning' },
  { key: 'chat', label: '对话', to: '/chat' },
  { key: 'mastery', label: '掌握路径', to: '/mastery' },
  { key: 'questions', label: '题库', to: '/questions' },
  { key: 'materials', label: '数据库', to: '/knowledge-bases' },
]

/**
 * 对话下的二级入口。写作页自己就是工作台：没有 sessionId 时是准备表，有
 * sessionId 时是那段写作，所以和对话放在一组。
 */
const chatEntries: readonly NavEntry[] = [
  { key: 'writing', label: '写作', to: '/co-writer' },
]

/**
 * 题库下的二级入口。练习已经有了自己的页面外壳（`views/PracticePage.vue`），
 * 不再借用学习外壳。题目问答是题库自己的对话页：选一道题，就这道题提问，
 * 历史按 `kind=question_bank` 与普通对话分开。
 */
const questionEntries: readonly NavEntry[] = [
  { key: 'question-chat', label: '题目问答', to: '/questions/chat' },
  { key: 'practice', label: '练习', to: '/practice' },
]

/**
 * 掌握路径下的二级入口。路径对话是一段独立的工作台：能力固定
 * `mastery_path`，会话按 `kind=mastery` 与对话页分区；学习地图看的是同一条
 * 路径上的知识点分布。
 */
const masteryEntries: readonly NavEntry[] = [
  { key: 'mastery-chat', label: '路径对话', to: '/mastery/chat' },
  { key: 'learning-map', label: '学习地图', to: '/learning-map' },
]

/**
 * 数据库下的二级入口。活书与沉浸阅读的正文都来自知识库里的文档，所以收在
 * 数据库下，而不是散在「其他」里。
 */
const materialEntries: readonly NavEntry[] = [
  { key: 'books', label: '活书', to: '/books' },
  { key: 'reading', label: '沉浸阅读', to: '/reading' },
]

/** 「其他」下的二级入口：还没有归到别处的一级分组加设置，暂时全部收在这里。 */
const otherEntries: readonly NavEntry[] = [
  { key: 'watching', label: '视频学习', to: '/watching' },
  { key: 'memory', label: '记忆', to: '/memory' },
  { key: 'extensions', label: '扩展', to: '/skills' },
  { key: 'companions', label: '伙伴', to: '/partners' },
  { key: 'settings', label: '设置', to: '/settings/account' },
]

/**
 * 图标路径。24×24 描边风格，`stroke` 由样式给，所以这里只有 `d`。
 *
 * 是常量不是响应式状态，所以不用 `ref`：放进 `reactive` 只会让每次渲染都去
 * 代理一遍一个永远不变的对象。
 */
const ICON_PATHS: Record<string, string> = {
  chat: 'M4 5h16v10H9l-5 4V5z',
  mastery: 'M4 20h4v-5h4V9h4V4h4',
  'mastery-chat': 'M4 5h16v11H10l-6 4V5zM8 10h8M8 13h5',
  learning: 'M4 5h7v14H4zM13 5h7v14h-7z',
  watching: 'M4 5h16v14H4zM10 9l5 3-5 3V9z',
  'learning-map': 'M4 5h16v14H4zM4 10h16M10 10v9M16 10v9',
  reading: 'M4 5h7a3 3 0 013 3v11a3 3 0 00-3-3H4zM20 5h-7a3 3 0 00-3 3v11a3 3 0 013-3h7z',
  books: 'M4 5h6v14H4zM11 7l4-2 5 12-4 2z',
  materials: 'M12 4l8 4-8 4-8-4 8-4zM4 12l8 4 8-4M4 16l8 4 8-4',
  memory: 'M12 4a4 4 0 014 4 4 4 0 010 8 4 4 0 01-8 0 4 4 0 010-8 4 4 0 014-4z',
  writing: 'M5 19l1-4L16 5l3 3L9 18l-4 1z',
  questions: 'M5 4h14v16H5zM8 9h8M8 13h8M8 17h4',
  'question-chat': 'M4 5h16v11H10l-6 4V5zM9 8v2M9 12h.01M12 8h4M12 12h3',
  practice: 'M5 5h14v14H5zM8 12l2 2 4-4',
  extensions: 'M10 4h4v4h4v4h-4v4h-4v-4H6V8h4z',
  companions:
    'M9 5a3 3 0 013 3 3 3 0 01-6 0 3 3 0 013-3zM4 19c0-3 2-5 5-5s5 2 5 5M16 7a2 2 0 010 4M15 19c0-2 1-4 3-4',
  settings:
    'M12 9a3 3 0 100 6 3 3 0 000-6zM12 3v2M12 19v2M4.2 7.2l1.4 1.4M18.4 15.4l1.4 1.4M3 12h2M19 12h2M4.2 16.8l1.4-1.4M18.4 8.6l1.4-1.4',
  other: 'M5 12h.01M12 12h.01M19 12h.01',
  chevronDown: 'M6 9l6 6 6-6',
  chevronLeft: 'M14 6l-6 6 6 6',
  chevronRight: 'M10 6l6 6-6 6',
}

const props = defineProps<{ collapsed: boolean }>()
const emit = defineEmits<{ (event: 'toggle'): void }>()

const router = useRouter()
const route = useRoute()
const menuOpen = ref(false)
const signingOut = ref(false)
const otherOpen = ref(false)

const username = computed(() => currentAccount.value?.username ?? '')

/**
 * 入口是否高亮。按路由名或路径前缀判断，不依赖 `RouterLink` 自带的
 * `active-class`：一级入口的归属由这张表决定，而不是由路由记录的父子关系
 * 决定。
 */
function isActive(entry: NavEntry): boolean {
  switch (entry.key) {
    case 'chat':
      return (
        route.path === '/chat' ||
        route.path.startsWith('/chat/') ||
        route.path.startsWith('/co-writer')
      )
    case 'mastery':
      return route.name === 'mastery'
    case 'mastery-chat':
      return route.name === 'mastery-chat'
    case 'questions':
      return route.name === 'questions'
    case 'question-chat':
      return route.name === 'question-chat'
    case 'practice':
      return route.name === 'practice'
    case 'learning':
      return route.name === 'learning'
    case 'watching':
      return route.name === 'watching'
    case 'learning-map':
      return route.name === 'learning-map'
    case 'reading':
      return route.name === 'reading'
    case 'books':
      return route.name === 'books' || route.name === 'book'
    case 'materials':
      return route.path.startsWith('/knowledge-bases') || route.path.startsWith('/materials')
    case 'memory':
      return route.path.startsWith('/memory')
    case 'writing':
      return route.path.startsWith('/co-writer')
    case 'extensions':
      return route.path === '/skills' || route.path === '/tools'
    case 'companions':
      return route.path === '/partners'
    case 'settings':
      return route.path.startsWith('/settings')
    default:
      return false
  }
}

/** 一级入口的高亮。题库在它自己的二级入口上也要亮。 */
function isPrimaryActive(entry: NavEntry): boolean {
  if (entry.key === 'questions') {
    return (
      route.name === 'questions' || route.name === 'practice' || route.name === 'question-chat'
    )
  }
  if (entry.key === 'mastery') {
    return (
      route.name === 'mastery' || route.name === 'mastery-chat' || route.name === 'learning-map'
    )
  }
  if (entry.key === 'materials') {
    return isActive(entry) || route.name === 'books' || route.name === 'book' || route.name === 'reading'
  }
  return isActive(entry)
}

const otherActive = computed(() => otherEntries.some(isActive))

// 进到「其他」里的任意页面时把分组展开，否则二级入口收着，用户看不出自己
// 当前在哪一项。
watch(
  otherActive,
  (active) => {
    if (active) otherOpen.value = true
  },
  { immediate: true },
)

/**
 * 「其他」是分组标题，不是一条路由。收合态下点它先把导航展开 —— 只有图标
 * 时无处安放二级入口。
 */
function toggleOther(): void {
  if (props.collapsed) {
    emit('toggle')
    otherOpen.value = true
    return
  }
  otherOpen.value = !otherOpen.value
}

/**
 * 管理台入口只在"角色是管理员"且权限快照没有明确拒绝 `admin` 面时出现。
 * 快照未到时按可见处理 —— 点进去会被 `SurfaceGate` 挡住，这比让管理员在
 * 网络慢的时候找不到入口好。
 */
const showAdmin = computed(() => isAdmin.value && canSurface('admin') !== false)

/**
 * 登出。**不是一个本地清理**：它会把账号的所有设备一起下线，服务端不补发
 * token（`设计说明书.md` §4.1）。所以顺序是先请求、后清本地 —— 反过来的话
 * 请求会因为已经没有 token 而拿到 401，服务端那边什么也没发生。
 *
 * 已经建好的 socket **不会因为登出自己断**，得由界面上持有它的那一处显式拆
 * 掉（《前端设计注意事项》§1.3）。这一步在 `Chat.vue` 的卸载里做。
 */
async function signOut(): Promise<void> {
  signingOut.value = true
  try {
    await logout()
  } catch {
    // 请求失败也照样往下走。凭据可能已经被撤回了，这时再报一次错只是把用户
    // 卡在一个他出不去的地方；而本地那一份清掉之后，第一个真请求会给出准确
    // 的结论。
  } finally {
    signingOut.value = false
    menuOpen.value = false
    clearSession()
    // 权限与能力都按人算，下一个人不能看到上一个人的。
    resetPermissions()
    resetCapabilities()
    void router.push({ name: 'login' })
  }
}
</script>

<template>
  <nav class="nav" :class="{ 'nav--collapsed': props.collapsed }" aria-label="一级导航">
    <div class="nav__brand">
      <span class="nav__mark" aria-hidden="true">D</span>
      <span v-if="!props.collapsed" class="nav__wordmark">DeepTutor</span>
    </div>

    <ul class="nav__list">
      <li v-for="entry in primaryEntries" :key="entry.key">
        <RouterLink
          class="nav__item"
          :class="{ 'nav__item--active': isPrimaryActive(entry) }"
          :to="entry.to"
          :title="props.collapsed ? entry.label : undefined"
        >
          <svg class="nav__icon" viewBox="0 0 24 24" aria-hidden="true">
            <path :d="ICON_PATHS[entry.key]" />
          </svg>
          <span v-if="!props.collapsed" class="nav__label">{{ entry.label }}</span>
          <span v-else class="nav__sr">{{ entry.label }}</span>
        </RouterLink>

        <ul v-if="entry.key === 'chat' && !props.collapsed" class="nav__sublist">
          <li v-for="sub in chatEntries" :key="sub.key">
            <RouterLink
              class="nav__subitem"
              :class="{ 'nav__subitem--active': isActive(sub) }"
              :to="sub.to"
            >
              <svg class="nav__icon nav__icon--sub" viewBox="0 0 24 24" aria-hidden="true">
                <path :d="ICON_PATHS[sub.key]" />
              </svg>
              <span class="nav__label">{{ sub.label }}</span>
            </RouterLink>
          </li>
        </ul>

        <ul v-if="entry.key === 'questions' && !props.collapsed" class="nav__sublist">
          <li v-for="sub in questionEntries" :key="sub.key">
            <RouterLink
              class="nav__subitem"
              :class="{ 'nav__subitem--active': isActive(sub) }"
              :to="sub.to"
            >
              <svg class="nav__icon nav__icon--sub" viewBox="0 0 24 24" aria-hidden="true">
                <path :d="ICON_PATHS[sub.key]" />
              </svg>
              <span class="nav__label">{{ sub.label }}</span>
            </RouterLink>
          </li>
        </ul>

        <ul v-if="entry.key === 'mastery' && !props.collapsed" class="nav__sublist">
          <li v-for="sub in masteryEntries" :key="sub.key">
            <RouterLink
              class="nav__subitem"
              :class="{ 'nav__subitem--active': isActive(sub) }"
              :to="sub.to"
            >
              <svg class="nav__icon nav__icon--sub" viewBox="0 0 24 24" aria-hidden="true">
                <path :d="ICON_PATHS[sub.key]" />
              </svg>
              <span class="nav__label">{{ sub.label }}</span>
            </RouterLink>
          </li>
        </ul>

        <ul v-if="entry.key === 'materials' && !props.collapsed" class="nav__sublist">
          <li v-for="sub in materialEntries" :key="sub.key">
            <RouterLink
              class="nav__subitem"
              :class="{ 'nav__subitem--active': isActive(sub) }"
              :to="sub.to"
            >
              <svg class="nav__icon nav__icon--sub" viewBox="0 0 24 24" aria-hidden="true">
                <path :d="ICON_PATHS[sub.key]" />
              </svg>
              <span class="nav__label">{{ sub.label }}</span>
            </RouterLink>
          </li>
        </ul>
      </li>

      <li class="nav__group">
        <button
          type="button"
          class="nav__item nav__item--button nav__group-toggle"
          :class="{ 'nav__item--active': otherActive }"
          :aria-expanded="!props.collapsed && otherOpen"
          aria-controls="nav-other"
          :title="props.collapsed ? '其他' : undefined"
          @click="toggleOther"
        >
          <svg class="nav__icon" viewBox="0 0 24 24" aria-hidden="true">
            <path :d="ICON_PATHS.other" />
          </svg>
          <span v-if="!props.collapsed" class="nav__label">其他</span>
          <span v-else class="nav__sr">其他</span>
          <svg
            v-if="!props.collapsed"
            class="nav__chevron"
            :class="{ 'nav__chevron--open': otherOpen }"
            viewBox="0 0 24 24"
            aria-hidden="true"
          >
            <path :d="ICON_PATHS.chevronDown" />
          </svg>
        </button>

        <ul v-if="otherOpen && !props.collapsed" id="nav-other" class="nav__sublist">
          <li v-for="entry in otherEntries" :key="entry.key">
            <RouterLink
              class="nav__subitem"
              :class="{ 'nav__subitem--active': isActive(entry) }"
              :to="entry.to"
            >
              <svg class="nav__icon nav__icon--sub" viewBox="0 0 24 24" aria-hidden="true">
                <path :d="ICON_PATHS[entry.key]" />
              </svg>
              <span class="nav__label">{{ entry.label }}</span>
            </RouterLink>
          </li>
        </ul>
      </li>
    </ul>

    <div class="nav__foot">
      <button
        type="button"
        class="nav__item nav__item--button"
        :aria-expanded="menuOpen"
        @click="menuOpen = !menuOpen"
      >
        <span class="nav__avatar" aria-hidden="true">{{ username.slice(0, 1).toUpperCase() }}</span>
        <span v-if="!props.collapsed" class="nav__label">{{ username }}</span>
      </button>

      <div v-if="menuOpen" class="nav__menu" role="menu">
        <!-- 个人资料没有页面；设置与（管理员可见的）管理台都是真路由。 -->
        <RouterLink class="nav__menu-item" to="/settings/account" @click="menuOpen = false">
          设置
        </RouterLink>
        <RouterLink
          v-if="showAdmin"
          class="nav__menu-item"
          to="/admin/users"
          @click="menuOpen = false"
        >
          管理台
        </RouterLink>
        <button type="button" class="nav__menu-item" :disabled="signingOut" @click="signOut">
          {{ signingOut ? '正在退出…' : '退出登录' }}
        </button>
        <!-- §4.1 要求说清这是**所有设备**一起下线，且措辞落在"需要重新登录"
             上、不落在"已经断开"上：正在生成的那一轮不会因为登出当场停下。 -->
        <p class="nav__menu-note">所有设备都需要重新登录</p>
      </div>
    </div>

    <button
      type="button"
      class="nav__collapse"
      :aria-label="props.collapsed ? '展开一级导航' : '收合一级导航'"
      @click="emit('toggle')"
    >
      <svg class="nav__icon" viewBox="0 0 24 24" aria-hidden="true">
        <path :d="props.collapsed ? ICON_PATHS.chevronRight : ICON_PATHS.chevronLeft" />
      </svg>
    </button>
  </nav>
</template>

<style scoped>
.nav {
  display: flex;
  flex-direction: column;
  height: 100%;
  padding: 12px 8px;
  border-right: 1px solid var(--border);
  background: var(--surface);
}

.nav__brand {
  display: flex;
  gap: 10px;
  align-items: center;
  padding: 4px 8px 14px;
}

.nav__mark {
  display: grid;
  flex: none;
  place-items: center;
  width: 26px;
  height: 26px;
  border-radius: var(--radius-sm);
  background: var(--accent);
  color: #fff;
  font-weight: 700;
  font-size: 14px;
}

.nav__wordmark {
  font-weight: 650;
  letter-spacing: -0.01em;
}

.nav__list {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 2px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.nav__item {
  display: flex;
  gap: 10px;
  align-items: center;
  width: 100%;
  padding: 8px;
  border: 0;
  border-radius: var(--radius-sm);
  background: none;
  color: var(--text-muted);
  text-decoration: none;
  cursor: pointer;
}

.nav__item:hover {
  background: var(--surface-sunken);
  color: var(--text);
}

/* §3.1：当前所在分组要能一眼看出。除了底色，左侧还有一条竖线 —— §6.2 要求
   状态不能只靠颜色表达。 */
.nav__item--active {
  position: relative;
  background: var(--accent-soft);
  color: var(--accent);
  font-weight: 600;
}

.nav__item--active::before {
  position: absolute;
  top: 6px;
  bottom: 6px;
  left: -8px;
  width: 3px;
  border-radius: 0 2px 2px 0;
  background: var(--accent);
  content: '';
}

.nav__item--button {
  text-align: left;
}

.nav__group {
  display: flex;
  flex-direction: column;
}

.nav__group-toggle {
  text-align: left;
}

.nav__chevron {
  flex: none;
  width: 14px;
  height: 14px;
  margin-left: auto;
  fill: none;
  stroke: currentcolor;
  stroke-width: 1.8;
  stroke-linecap: round;
  stroke-linejoin: round;
  transition: transform 0.15s ease;
}

.nav__chevron--open {
  transform: rotate(180deg);
}

.nav__sublist {
  display: flex;
  flex-direction: column;
  gap: 1px;
  margin: 2px 0 4px 8px;
  padding: 0 0 0 8px;
  border-left: 1px solid var(--border);
  list-style: none;
}

.nav__subitem {
  display: flex;
  gap: 8px;
  align-items: center;
  padding: 6px 8px;
  border-radius: var(--radius-sm);
  color: var(--text-muted);
  font-size: 13px;
  text-decoration: none;
}

.nav__subitem:hover {
  background: var(--surface-sunken);
  color: var(--text);
}

.nav__subitem--active {
  background: var(--accent-soft);
  color: var(--accent);
  font-weight: 600;
}

.nav__icon--sub {
  width: 16px;
  height: 16px;
}

.nav__icon {
  flex: none;
  width: 18px;
  height: 18px;
  fill: none;
  stroke: currentcolor;
  stroke-width: 1.6;
  stroke-linecap: round;
  stroke-linejoin: round;
}

.nav__label {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.nav__sr {
  position: absolute;
  overflow: hidden;
  clip-path: inset(50%);
  width: 1px;
  height: 1px;
}

.nav__avatar {
  display: grid;
  flex: none;
  place-items: center;
  width: 18px;
  height: 18px;
  border-radius: 50%;
  background: var(--border-strong);
  color: var(--text);
  font-size: 11px;
  font-weight: 600;
}

.nav__foot {
  position: relative;
  padding-top: 8px;
  border-top: 1px solid var(--border);
}

.nav__menu {
  position: absolute;
  right: 0;
  bottom: calc(100% + 6px);
  left: 0;
  z-index: 10;
  padding: 6px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface-raised);
  box-shadow: 0 6px 24px rgb(15 23 42 / 12%);
}

.nav__menu-item {
  display: block;
  width: 100%;
  padding: 7px 8px;
  border: 0;
  border-radius: var(--radius-sm);
  background: none;
  color: var(--text);
  text-align: left;
  text-decoration: none;
  cursor: pointer;
}

.nav__menu-item:hover:not(:disabled) {
  background: var(--surface-sunken);
}

.nav__menu-item:disabled {
  color: var(--text-faint);
  cursor: default;
}

.nav__menu-note {
  padding: 6px 8px 2px;
  color: var(--text-muted);
  font-size: 12px;
}

.nav__collapse {
  display: grid;
  place-items: center;
  margin-top: 8px;
  padding: 6px;
  border: 0;
  border-radius: var(--radius-sm);
  background: none;
  color: var(--text-faint);
  cursor: pointer;
}

.nav__collapse:hover {
  background: var(--surface-sunken);
  color: var(--text);
}
</style>
