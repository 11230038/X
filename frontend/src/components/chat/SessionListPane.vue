<script setup lang="ts">
/**
 * 对话列表栏：搜索、新建、重命名、软删除、回收站入口。
 *
 * 行上的操作不占常驻空间：它们在一个 `details` 菜单里，点开才出现。搜索仍然
 * 由这一层过滤（`ListPane` 把这件事判给了使用方）。
 *
 * 重命名是**就地编辑**，不用一个弹窗再弹一个输入框：`PATCH /sessions/:id` 只
 * 改标题，弹窗那一步没有别的东西可确认。
 */

import { computed, nextTick, ref } from 'vue'

import type { SessionSummary } from '../../api/sessions'
import { CHAT } from '../../copy/chat'
import ListPane from '../shell/ListPane.vue'
import { sessionLabel } from './session-label'

const props = withDefaults(
  defineProps<{
    sessions: readonly SessionSummary[]
    loading: boolean
    /** 列表本身没载进来时的那句话。整栏的错误，不是某一行的。 */
    error: string | null
    /** 当前打开的那个会话。 */
    activeId?: string | null
    /** 还有下一页。 */
    hasMore?: boolean
    loadingMore?: boolean
    /** 列表栏标题。写作页要的是「写作」，不是「对话」。 */
    title?: string
    /** 空态那个动作的文案。 */
    emptyAction?: string
    /** 新建按钮的可访问名。 */
    newLabel?: string
    /** 点开一行时用的路由名。写作页的行属于 `co-writer`。 */
    rowRouteName?: string
  }>(),
  {
    title: '历史会话',
    emptyAction: '开始一段对话',
    newLabel: '新建会话',
    rowRouteName: 'conversation',
  },
)

const emit = defineEmits<{
  (event: 'new-session'): void
  (event: 'load-more'): void
  (event: 'rename', sessionId: string, title: string): void
  (event: 'remove', session: SessionSummary): void
  (event: 'retry'): void
}>()

const query = ref('')
const renamingId = ref('')
const renameText = ref('')
const renameField = ref<HTMLInputElement | null>(null)

const filtered = computed(() => {
  const needle = query.value.trim().toLowerCase()
  if (needle === '') {
    return props.sessions
  }
  return props.sessions.filter((session) => session.title.toLowerCase().includes(needle))
})

async function beginRename(session: SessionSummary): Promise<void> {
  renamingId.value = session.session_id
  renameText.value = session.title
  await nextTick()
  renameField.value?.focus()
  renameField.value?.select()
}

function cancelRename(): void {
  renamingId.value = ''
  renameText.value = ''
}

function commitRename(): void {
  const id = renamingId.value
  const title = renameText.value.trim()
  if (id === '' || title === '') return
  emit('rename', id, title)
  cancelRename()
}
</script>

<template>
  <ListPane
    :title="title"
    v-model:query="query"
    :empty-action="emptyAction"
    :loading="loading"
    :empty="filtered.length === 0"
    @empty-action="emit('new-session')"
  >
    <template #actions>
      <RouterLink class="icon-button" to="/chat/recycle-bin" :title="CHAT.recycleBin">
        {{ CHAT.recycleBin }}
      </RouterLink>
      <button type="button" class="icon-button" :aria-label="newLabel" @click="emit('new-session')">
        ＋
      </button>
    </template>

    <li v-for="session in filtered" :key="session.session_id">
      <div class="session" :class="{ 'session--active': session.session_id === activeId }">
        <template v-if="renamingId === session.session_id">
          <form class="rename" @submit.prevent="commitRename">
            <input
              ref="renameField"
              v-model="renameText"
              class="rename__input"
              :aria-label="CHAT.renameLabel"
              :placeholder="CHAT.renamePlaceholder"
              @keydown.esc.stop="cancelRename"
            />
            <button type="submit" class="rename__save" :disabled="renameText.trim() === ''">
              保存
            </button>
            <button type="button" class="rename__cancel" @click="cancelRename">取消</button>
          </form>
        </template>

        <template v-else>
          <RouterLink
            class="session__link"
            :to="{ name: rowRouteName, params: { sessionId: session.session_id } }"
          >
            <span class="session__title" :class="{ 'session__title--untitled': session.title === '' }">
              {{ sessionLabel(session) }}
            </span>
          </RouterLink>

          <details class="menu">
            <summary class="menu__toggle" :aria-label="CHAT.sessionActions">⋯</summary>
            <div class="menu__body">
              <button type="button" class="menu__item" @click="beginRename(session)">
                {{ CHAT.renameSession }}
              </button>
              <button type="button" class="menu__item menu__item--danger" @click="emit('remove', session)">
                {{ CHAT.removeSession }}
              </button>
            </div>
          </details>
        </template>
      </div>
    </li>

    <li v-if="hasMore" class="more">
      <button type="button" class="more__button" :disabled="loadingMore" @click="emit('load-more')">
        {{ loadingMore ? '正在读取…' : '加载更多' }}
      </button>
    </li>

    <li v-if="error" class="session__error">
      <span>{{ error }}</span>
      <button type="button" class="session__retry" @click="emit('retry')">重试</button>
    </li>
  </ListPane>
</template>

<style scoped>
.icon-button {
  padding: 6px 12px;
  border: 1px solid rgb(0 0 0 / 6%);
  border-radius: 999px;
  background: rgb(255 255 255 / 78%);
  color: var(--text-muted);
  font-size: 14px;
  font-weight: 600;
  text-decoration: none;
  cursor: pointer;
  transition: all 0.2s ease;
}

.icon-button:hover {
  border-color: var(--accent);
  color: var(--accent-ink);
}

/* 演示稿 `.sidebar-item`：12px 16px 内边距、12px 圆角、14px/500。 */
.session {
  display: flex;
  gap: 2px;
  align-items: center;
  border-radius: var(--radius-sm);
  transition: background 0.3s cubic-bezier(0.25, 0.8, 0.25, 1);
}

.session:hover {
  background: rgb(255 255 255 / 60%);
}

.session--active {
  background: #fff;
  box-shadow: 0 2px 8px rgb(51 68 85 / 6%);
}

.session__link {
  flex: 1;
  min-width: 0;
  display: block;
  padding: 14px 18px;
  color: inherit;
  text-decoration: none;
}

.session__title {
  display: block;
  overflow: hidden;
  font-size: 16px;
  font-weight: 500;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.session--active .session__title {
  color: var(--text);
  font-weight: 750;
}

.session__title--untitled {
  color: var(--text-faint);
}

.menu {
  position: relative;
  flex: none;
  margin-right: 8px;
}

.menu__toggle {
  padding: 4px 5px;
  border-radius: 4px;
  color: var(--text-faint);
  cursor: pointer;
  list-style: none;
}

.menu__toggle::-webkit-details-marker {
  display: none;
}

.menu__toggle:hover {
  background: var(--surface);
  color: var(--text);
}

.menu__body {
  position: absolute;
  top: 100%;
  right: 0;
  z-index: 5;
  display: flex;
  flex-direction: column;
  min-width: 130px;
  padding: 5px;
  border: 1px solid var(--glass-border);
  border-radius: var(--radius-sm);
  background: rgb(255 255 255 / 94%);
  box-shadow: var(--glass-shadow-hover);
  backdrop-filter: blur(20px);
  -webkit-backdrop-filter: blur(20px);
}

.menu__item {
  padding: 6px 8px;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: var(--text);
  font-size: 12px;
  text-align: left;
  cursor: pointer;
}

.menu__item:hover {
  background: var(--surface-sunken);
}

.menu__item--danger:hover {
  background: var(--danger-soft);
  color: var(--danger);
}

.rename {
  display: flex;
  gap: 4px;
  align-items: center;
  width: 100%;
  padding: 4px 6px;
}

.rename__input {
  flex: 1;
  min-width: 0;
  padding: 4px 6px;
  border: 1px solid var(--accent);
  border-radius: 4px;
  font-size: 12px;
}

.rename__save,
.rename__cancel {
  flex: none;
  padding: 3px 6px;
  border: 1px solid var(--border);
  border-radius: 4px;
  background: var(--surface);
  font-size: 12px;
  cursor: pointer;
}

.rename__save:disabled {
  color: var(--text-faint);
  cursor: default;
}

.more {
  padding: 6px;
}

.more__button {
  width: 100%;
  padding: 6px;
  border: 1px dashed var(--border-strong);
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text-muted);
  font-size: 12px;
  cursor: pointer;
}

.session__error {
  display: flex;
  gap: 8px;
  align-items: center;
  justify-content: space-between;
  padding: 10px;
  color: var(--danger);
  font-size: 13px;
}

.session__retry {
  flex: none;
  padding: 2px 8px;
  border: 1px solid currentcolor;
  border-radius: 4px;
  background: transparent;
  color: inherit;
  font-size: 12px;
  cursor: pointer;
}
</style>
