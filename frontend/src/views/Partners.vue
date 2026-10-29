<script setup lang="ts">
/**
 * 伙伴。
 *
 * 「伙伴」和「人格」原来分两页：一页只选人，一页只编辑。它们读的是同一份数据，
 * 用户看两次同一张名单却只能在其中一页做一件事，所以合成了一页 —— 一个伙伴
 * 就是一份保存下来的人格设定，选它、改它、删它都在这里。
 *
 * 选择发生在对话输入区，所以每张卡片带一个「开始对话」的出口，而不是在这里
 * 复制一套发送框；删除只删这一行，不影响已经发生的对话 —— 对话里本来就没有
 * 存人格，只存说过的话。系统人格（写作教练那类）锁在自己那一页上，这里不给
 * 出口，只标出来。
 *
 * 人格是内容，不是权限。页面把这句话写在标题下面，因为名字很容易让人以为
 * 它换了身份。
 */

import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import type { ApiFailure } from '../api/client'
import { describeFailure } from '../api/errors'
import { deletePersona, type Persona } from '../api/personas'
import { canSurface } from '../auth/permissions'
import EmptyState from '../components/common/EmptyState.vue'
import FailureState from '../components/common/FailureState.vue'
import PartialBanner from '../components/common/PartialBanner.vue'
import SkeletonBlock from '../components/common/SkeletonBlock.vue'
import SurfaceGate from '../components/common/SurfaceGate.vue'
import AppShell from '../components/shell/AppShell.vue'
import PageHeader from '../components/shell/PageHeader.vue'
import { PERSONAS, isSystemPersona } from '../copy/personas'
import { confirmAction } from '../shared/confirm'
import { formatDateTime } from '../shared/format'
import { toFailure } from '../shared/resource'
import { notify } from '../shared/toast'

import PersonaEditor from './personas/PersonaEditor.vue'
import { usePersonaList } from './personas/usePersonaList'

const router = useRouter()

const { items, status, failure, load } = usePersonaList()
const actionFailure = ref<ApiFailure | null>(null)
const deletingId = ref('')

const editorOpen = ref(false)
const editing = ref<Persona | null>(null)

/**
 * 只看系统人格、只看自己建的，还是全看。
 *
 * 默认「全部」：这一页首先是"我有哪些伙伴"，筛选是后加的视角，不该一开始就
 * 藏掉东西。它只筛显示的卡片，不改数据 —— 计数跟着筛后那几条走。
 */
type PersonaFilter = 'all' | 'system' | 'user'
const filter = ref<PersonaFilter>('all')

const visible = computed(() =>
  filter.value === 'all'
    ? items.value
    : items.value.filter(
        (persona) => isSystemPersona(persona.name) === (filter.value === 'system'),
      ),
)

function openCreate(): void {
  editing.value = null
  editorOpen.value = true
}

function openEdit(persona: Persona): void {
  editing.value = persona
  editorOpen.value = true
}

function closeEditor(): void {
  editorOpen.value = false
  editing.value = null
}

/** 保存成功后不回读列表：返回的就是这一行，直接并进去，顺序照服务端的名字序。 */
function onSaved(saved: Persona): void {
  closeEditor()
  const exists = items.value.some((item) => item.persona_id === saved.persona_id)
  items.value = (
    exists
      ? items.value.map((item) => (item.persona_id === saved.persona_id ? saved : item))
      : [...items.value, saved]
  ).sort((left, right) => left.name.localeCompare(right.name, 'zh-CN'))
  status.value = 'ready'
}

async function remove(persona: Persona): Promise<void> {
  const confirmed = await confirmAction({
    title: PERSONAS.removeTitle(persona.name),
    body: PERSONAS.removeBody,
    note: PERSONAS.removeNote,
    confirmLabel: PERSONAS.removeConfirm,
    danger: true,
  })
  if (!confirmed) return

  deletingId.value = persona.persona_id
  actionFailure.value = null
  try {
    await deletePersona(persona.persona_id)
    items.value = items.value.filter((item) => item.persona_id !== persona.persona_id)
    notify(PERSONAS.removed, 'success')
  } catch (cause) {
    const failed = toFailure(cause)
    // 404 的意思是"它已经不在了"；想要的结果已经达成，不当成失败。
    if (failed.status === 404) {
      items.value = items.value.filter((item) => item.persona_id !== persona.persona_id)
      notify(PERSONAS.removed, 'success')
    } else {
      actionFailure.value = failed
    }
  } finally {
    deletingId.value = ''
  }
}

function useInChat(persona: Persona): void {
  void router.push({ name: 'chat', query: { persona: persona.persona_id } })
}

onMounted(() => {
  // 权限明确为 false 时不发请求；`SurfaceGate` 会画拒绝页。
  if (canSurface('chat') === false) return
  void load()
})
</script>

<template>
  <AppShell>
    <SurfaceGate surface="chat">
      <div class="page">
        <div class="page__inner">
          <PageHeader :title="PERSONAS.title" :description="PERSONAS.description">
            <template #actions>
              <button type="button" class="ghost" @click="router.push({ name: 'chat' })">
                {{ PERSONAS.toChat }}
              </button>
              <button type="button" class="primary" @click="openCreate">
                {{ PERSONAS.newPersona }}
              </button>
            </template>
          </PageHeader>

          <PartialBanner
            v-if="failure && items.length > 0"
            :failure="failure"
            :text="PERSONAS.refreshFailedKept"
            @retry="load"
          />

          <div v-if="actionFailure" class="action-error" role="alert">
            <span>
              {{ PERSONAS.actionFailed }}：{{ describeFailure(actionFailure) }}
            </span>
            <button type="button" class="ghost" @click="actionFailure = null">
              {{ PERSONAS.dismiss }}
            </button>
          </div>

          <SkeletonBlock
            v-if="status === 'loading' && items.length === 0"
            :lines="4"
            :line-height="46"
          />

          <FailureState
            v-else-if="status === 'failed' && failure && items.length === 0"
            :failure="failure"
            :title="PERSONAS.loadFailed"
            @retry="load"
          />

          <EmptyState
            v-else-if="items.length === 0"
            :title="PERSONAS.emptyTitle"
            :hint="PERSONAS.emptyHint"
            :action-label="PERSONAS.emptyAction"
            @action="openCreate"
          />

          <template v-else>
            <div class="list-head">
              <div class="count">{{ PERSONAS.count(visible.length) }}</div>
              <label class="filter">
                <span class="filter__label">{{ PERSONAS.filterLabel }}</span>
                <select v-model="filter" class="filter__select">
                  <option value="all">{{ PERSONAS.filterAll }}</option>
                  <option value="system">{{ PERSONAS.filterSystem }}</option>
                  <option value="user">{{ PERSONAS.filterUser }}</option>
                </select>
              </label>
            </div>

            <p v-if="visible.length === 0" class="hint">{{ PERSONAS.filterEmpty }}</p>

            <ul v-else class="list">
              <li v-for="persona in visible" :key="persona.persona_id" class="card">
                <header class="card__head">
                  <div class="card__title-wrap">
                    <h2 class="card__name">
                      {{ persona.name }}
                      <span v-if="isSystemPersona(persona.name)" class="card__badge">
                        {{ PERSONAS.systemBadge }}
                      </span>
                    </h2>
                    <span class="card__date">
                      {{ PERSONAS.updatedAt(formatDateTime(persona.updated_at)) }}
                    </span>
                  </div>
                  <div class="card__actions">
                    <button
                      v-if="!isSystemPersona(persona.name)"
                      type="button"
                      class="ghost"
                      @click="useInChat(persona)"
                    >
                      {{ PERSONAS.useThisPersona }}
                    </button>
                    <button type="button" class="ghost" @click="openEdit(persona)">
                      {{ PERSONAS.edit }}
                    </button>
                    <button
                      type="button"
                      class="ghost ghost--danger"
                      :disabled="deletingId === persona.persona_id"
                      @click="remove(persona)"
                    >
                      {{
                        deletingId === persona.persona_id
                          ? PERSONAS.deleting
                          : PERSONAS.remove
                      }}
                    </button>
                  </div>
                </header>

                <p v-if="persona.description" class="card__description">
                  {{ persona.description }}
                </p>

                <details class="card__prompt">
                  <summary class="card__summary">{{ PERSONAS.showContent }}</summary>
                  <pre class="card__content">{{ persona.content }}</pre>
                </details>
              </li>
            </ul>
          </template>
        </div>
      </div>
    </SurfaceGate>

    <PersonaEditor
      :open="editorOpen"
      :persona="editing"
      @close="closeEditor"
      @saved="onSaved"
    />
  </AppShell>
</template>

<style scoped>
.page__inner {
  display: flex;
  flex-direction: column;
  gap: 18px;
}

.ghost {
  padding: 7px 15px;
  border: 1px solid rgb(0 0 0 / 8%);
  border-radius: 999px;
  background: rgb(255 255 255 / 78%);
  color: var(--text-muted);
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
}

.ghost:hover {
  border-color: var(--accent);
  background: #fff;
  color: var(--accent-ink);
}

.ghost:disabled {
  cursor: default;
  opacity: 0.55;
}

.ghost--danger:hover {
  border-color: var(--danger);
  color: var(--danger);
}

.primary {
  padding: 8px 18px;
  border: 1px solid transparent;
  border-radius: 999px;
  background: var(--accent-gradient);
  color: #fff;
  font-size: 14px;
  font-weight: 700;
  cursor: pointer;
  box-shadow: 0 4px 12px rgb(177 140 255 / 28%);
}

.list-head {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  align-items: center;
  justify-content: space-between;
}

.count {
  color: var(--text-faint);
  font-size: 14px;
}

.filter {
  display: flex;
  gap: 8px;
  align-items: center;
}

.filter__label {
  color: var(--text-faint);
  font-size: 14px;
}

.filter__select {
  padding: 6px 12px;
  border: 1px solid rgb(0 0 0 / 6%);
  border-radius: 999px;
  background: rgb(255 255 255 / 70%);
  color: var(--text);
  font: inherit;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
}

.filter__select:focus {
  border-color: var(--accent);
  outline: none;
}

.hint {
  margin: 0;
  color: var(--text-faint);
  font-size: 14px;
  line-height: 1.6;
}

.action-error {
  display: flex;
  gap: 14px;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  border: 1px solid var(--danger);
  border-radius: var(--radius-sm);
  background: var(--danger-soft);
  color: var(--text);
  font-size: 14px;
}

.list {
  display: flex;
  flex-direction: column;
  gap: 14px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.card {
  padding: 20px;
  border: 1px solid var(--glass-border);
  border-radius: var(--radius-card);
  background: var(--glass-bg);
  box-shadow: var(--glass-shadow), var(--inset-light);
  backdrop-filter: blur(20px);
  -webkit-backdrop-filter: blur(20px);
}

.card__head {
  display: flex;
  flex-wrap: wrap;
  gap: 14px;
  align-items: flex-start;
  justify-content: space-between;
}

.card__title-wrap {
  min-width: 0;
}

.card__name {
  margin: 0;
  font-size: 15px;
  font-weight: 650;
}

.card__badge {
  margin-left: 8px;
  padding: 2px 9px;
  border: 1px solid rgb(177 140 255 / 32%);
  border-radius: 999px;
  background: rgb(177 140 255 / 10%);
  color: var(--accent-ink);
  font-size: 13px;
  font-weight: 600;
  vertical-align: 1px;
}

.card__date {
  display: block;
  margin-top: 3px;
  color: var(--text-faint);
  font-size: 14px;
}

.card__actions {
  display: flex;
  flex: none;
  flex-wrap: wrap;
  gap: 6px;
}

.card__description {
  margin: 8px 0 0;
  color: var(--text-muted);
  font-size: 14px;
  line-height: 1.6;
}

.card__prompt {
  margin-top: 8px;
}

.card__summary {
  color: var(--accent);
  font-size: 14px;
  cursor: pointer;
}

.card__content {
  margin: 8px 0 0;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface-sunken);
  color: var(--text);
  font-family: var(--font-mono);
  font-size: 14px;
  line-height: 1.7;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
