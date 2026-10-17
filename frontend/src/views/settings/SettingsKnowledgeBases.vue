<script setup lang="ts">
/**
 * 默认知识库。
 *
 * 这里只决定"新对话默认带哪个库"，不承担建库、加文档、向量状态那些事 ——
 * 它们在「材料 / 知识库」页。所以选中之后不去复制那一页的控件。
 */

import { onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'

import type { ApiFailure } from '../../api/client'
import {
  listKnowledgeBases,
  setDefaultKnowledgeBase,
  type KnowledgeBase,
} from '../../api/knowledgeBases'
import EmptyState from '../../components/common/EmptyState.vue'
import FailureState from '../../components/common/FailureState.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import { SETTINGS } from '../../copy/settings'
import { toFailure } from '../../shared/resource'
import { notify } from '../../shared/toast'

const items = ref<KnowledgeBase[]>([])
const status = ref<'loading' | 'ready' | 'failed'>('loading')
const failure = ref<ApiFailure | null>(null)
const busyId = ref('')
const actionFailure = ref<ApiFailure | null>(null)

async function load(): Promise<void> {
  status.value = 'loading'
  failure.value = null
  try {
    const result = await listKnowledgeBases()
    items.value = result.knowledge_bases
    status.value = 'ready'
  } catch (cause) {
    failure.value = toFailure(cause)
    status.value = 'failed'
  }
}

async function makeDefault(kb: KnowledgeBase): Promise<void> {
  busyId.value = kb.kb_id
  actionFailure.value = null
  try {
    const result = await setDefaultKnowledgeBase(kb.kb_id)
    items.value = items.value.map((item) =>
      item.kb_id === result.knowledge_base.kb_id
        ? result.knowledge_base
        : { ...item, is_default: false },
    )
    notify(`${kb.name} 已设为默认知识库`, 'success')
  } catch (cause) {
    actionFailure.value = toFailure(cause)
  } finally {
    busyId.value = ''
  }
}

onMounted(() => {
  void load()
})
</script>

<template>
  <div class="kbs">
    <SkeletonBlock v-if="status === 'loading'" :lines="4" :line-height="34" />

    <FailureState
      v-else-if="status === 'failed' && failure"
      :failure="failure"
      title="知识库列表没能读出来"
      @retry="load"
    />

    <EmptyState
      v-else-if="items.length === 0"
      :title="SETTINGS.noKnowledgeBases"
      hint="知识库与文档都在「材料」里创建。"
      :action-label="SETTINGS.knowledgeBases"
      @action="$router.push({ name: 'knowledge-bases' })"
    />

    <template v-else>
      <p class="hint">
        新对话默认带上的知识库。这里只改默认值，不改变各库里的文档。
      </p>

      <p v-if="actionFailure" class="error">
        没能设为默认：{{ actionFailure.message }}
      </p>

      <ul class="list">
        <li v-for="kb in items" :key="kb.kb_id" class="item">
          <div class="item__text">
            <p class="item__name">
              {{ kb.name }}
              <span v-if="kb.is_default" class="chip">{{ SETTINGS.defaultKnowledgeBase }}</span>
            </p>
            <p v-if="kb.description" class="item__description">{{ kb.description }}</p>
            <p class="item__meta">
              {{ kb.documents }} 份文档 · {{ kb.ready_documents }} 份已就绪
            </p>
          </div>
          <div class="item__actions">
            <RouterLink class="link" :to="{ name: 'knowledge-base', params: { kbId: kb.kb_id } }">
              打开
            </RouterLink>
            <button
              v-if="!kb.is_default"
              type="button"
              class="ghost"
              :disabled="busyId === kb.kb_id"
              @click="makeDefault(kb)"
            >
              {{ busyId === kb.kb_id ? '正在设置…' : SETTINGS.setDefault }}
            </button>
          </div>
        </li>
      </ul>
    </template>
  </div>
</template>

<style scoped>
.kbs {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.hint {
  margin: 0;
  color: var(--text-muted);
  font-size: 11px;
  line-height: 1.6;
}

.list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.item {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  align-items: flex-start;
  justify-content: space-between;
  padding: 11px 13px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
}

.item__text {
  min-width: 0;
}

.item__name {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: baseline;
  margin: 0;
  font-size: 13px;
  font-weight: 600;
}

.item__description {
  margin: 3px 0 0;
  color: var(--text-muted);
  font-size: 11px;
  line-height: 1.6;
}

.item__meta {
  margin: 4px 0 0;
  color: var(--text-faint);
  font-size: 10px;
}

.item__actions {
  display: flex;
  flex: none;
  gap: 8px;
  align-items: center;
}

.chip {
  padding: 0 7px;
  border: 1px solid var(--success);
  border-radius: 9px;
  color: var(--success);
  font-size: 9px;
  font-weight: 400;
  line-height: 17px;
}

.link {
  color: var(--accent);
  font-size: 11px;
  text-decoration: none;
}

.ghost {
  padding: 4px 10px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text-muted);
  font-size: 11px;
  cursor: pointer;
}

.ghost:disabled {
  cursor: default;
  opacity: 0.55;
}

.error {
  margin: 0;
  color: var(--danger);
  font-size: 11px;
}
</style>
