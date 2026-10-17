<script setup lang="ts">
/**
 * 一级导航。`设计说明书.md` §2.2 的九个分组，§3.1 的两条形态要求。
 *
 * 九个分组现在都有落点：有后端的落到真页面，没有后端的落到各自的占位页
 * （写作 / 扩展 / 伙伴）。**不按权限隐藏分组** —— 进得去却看到"没有权限"比
 * 导航里少一项更容易理解，而且权限快照可能还没到。
 *
 * 管理台不进九组：它不是日常入口，放在头像菜单里，且只有管理员看得到。
 *
 * 图标是内联 SVG，没有引图标库。§2.2 要求**图标必须配文字**，所以图标是
 * 辅助，文字才是主体 —— 收合态才只剩图标，且悬停出名称（§3.1）。
 */

import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'

import { currentAccount, clearSession } from '../../session'
import { logout } from '../../api/auth'
import { canSurface, isAdmin, resetPermissions } from '../../auth/permissions'
import { resetCapabilities } from '../../shared/capabilities'

interface NavEntry {
  key: string
  label: string
  to: string
}

/** §2.2 的九个分组，顺序照那张表。 */
const entries: readonly NavEntry[] = [
  { key: 'chat', label: '对话', to: '/chat' },
  { key: 'learning', label: '学习', to: '/learning' },
  { key: 'materials', label: '材料', to: '/knowledge-bases' },
  { key: 'memory', label: '记忆', to: '/memory' },
  { key: 'writing', label: '写作', to: '/co-writer' },
  { key: 'questions', label: '题库', to: '/questions' },
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
  learning: 'M4 5h7v14H4zM13 5h7v14h-7z',
  materials: 'M12 4l8 4-8 4-8-4 8-4zM4 12l8 4 8-4M4 16l8 4 8-4',
  memory: 'M12 4a4 4 0 014 4 4 4 0 010 8 4 4 0 01-8 0 4 4 0 010-8 4 4 0 014-4z',
  writing: 'M5 19l1-4L16 5l3 3L9 18l-4 1z',
  questions: 'M5 4h14v16H5zM8 9h8M8 13h8M8 17h4',
  extensions: 'M10 4h4v4h4v4h-4v4h-4v-4H6V8h4z',
  companions:
    'M9 5a3 3 0 013 3 3 3 0 01-6 0 3 3 0 013-3zM4 19c0-3 2-5 5-5s5 2 5 5M16 7a2 2 0 010 4M15 19c0-2 1-4 3-4',
  settings:
    'M12 9a3 3 0 100 6 3 3 0 000-6zM12 3v2M12 19v2M4.2 7.2l1.4 1.4M18.4 15.4l1.4 1.4M3 12h2M19 12h2M4.2 16.8l1.4-1.4M18.4 8.6l1.4-1.4',
  chevronLeft: 'M14 6l-6 6 6 6',
  chevronRight: 'M10 6l6 6-6 6',
}

const props = defineProps<{ collapsed: boolean }>()
const emit = defineEmits<{ (event: 'toggle'): void }>()

const router = useRouter()
const menuOpen = ref(false)
const signingOut = ref(false)

const username = computed(() => currentAccount.value?.username ?? '')

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
      <li v-for="entry in entries" :key="entry.key">
        <RouterLink
          class="nav__item"
          active-class="nav__item--active"
          :to="entry.to"
          :title="props.collapsed ? entry.label : undefined"
        >
          <svg class="nav__icon" viewBox="0 0 24 24" aria-hidden="true">
            <path :d="ICON_PATHS[entry.key]" />
          </svg>
          <span v-if="!props.collapsed" class="nav__label">{{ entry.label }}</span>
          <span v-else class="nav__sr">{{ entry.label }}</span>
        </RouterLink>
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
