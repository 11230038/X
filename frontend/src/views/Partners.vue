<script setup lang="ts">
/**
 * 伙伴：人格的使用面。
 *
 * 数据与「人格」页是同一份，这里只做选人开始对话，不重复编辑与删除 —— 否则
 * 同一行数据会有两套写入路径。点"开始对话"落到 `/chat?persona=…`，输入区
 * 会在连接就绪后把人格 id 带进 `start_turn`。
 *
 * 页面把"伙伴就是人格"写在标题下面：它不是一个账号，也没有独立的共享上下
 * 文。这一版没有的，不画成有的。
 */

import { onMounted } from 'vue'
import { useRouter } from 'vue-router'

import { canSurface } from '../auth/permissions'
import EmptyState from '../components/common/EmptyState.vue'
import FailureState from '../components/common/FailureState.vue'
import PartialBanner from '../components/common/PartialBanner.vue'
import SkeletonBlock from '../components/common/SkeletonBlock.vue'
import SurfaceGate from '../components/common/SurfaceGate.vue'
import AppShell from '../components/shell/AppShell.vue'
import PageHeader from '../components/shell/PageHeader.vue'
import { PARTNERS } from '../copy/collaboration'
import { formatDateTime } from '../shared/format'

import { usePersonaList } from './personas/usePersonaList'

const router = useRouter()
const { items, status, failure, load } = usePersonaList()

function start(personaId: string): void {
  void router.push({ name: 'chat', query: { persona: personaId } })
}

onMounted(() => {
  if (canSurface('chat') === false) return
  void load()
})
</script>

<template>
  <AppShell>
    <SurfaceGate surface="chat">
      <div class="page">
        <div class="page__inner">
          <PageHeader :title="PARTNERS.title" :description="PARTNERS.description">
            <template #actions>
              <button type="button" class="ghost" @click="router.push({ name: 'personas' })">
                {{ PARTNERS.manage }}
              </button>
            </template>
          </PageHeader>

          <PartialBanner
            v-if="failure && items.length > 0"
            :failure="failure"
            :text="PARTNERS.refreshFailedKept"
            @retry="load"
          />

          <SkeletonBlock
            v-if="status === 'loading' && items.length === 0"
            :lines="4"
            :line-height="46"
          />

          <FailureState
            v-else-if="status === 'failed' && failure && items.length === 0"
            :failure="failure"
            :title="PARTNERS.loadFailed"
            @retry="load"
          />

          <EmptyState
            v-else-if="items.length === 0"
            :title="PARTNERS.emptyTitle"
            :hint="PARTNERS.emptyHint"
            :action-label="PARTNERS.emptyAction"
            @action="router.push({ name: 'personas' })"
          />

          <template v-else>
            <div class="count">{{ PARTNERS.count(items.length) }}</div>

            <ul class="grid">
              <li v-for="persona in items" :key="persona.persona_id" class="card">
                <header class="card__head">
                  <h2 class="card__name">{{ persona.name }}</h2>
                  <span class="card__date">
                    {{ formatDateTime(persona.updated_at) }}
                  </span>
                </header>

                <p v-if="persona.description" class="card__description">
                  {{ persona.description }}
                </p>
                <p v-else class="card__description card__description--muted">
                  没有写说明；设定本身还在，可以点开看。
                </p>

                <details class="card__prompt">
                  <summary class="card__summary">{{ PARTNERS.showPrompt }}</summary>
                  <pre class="card__content">{{ persona.content }}</pre>
                </details>

                <div class="card__actions">
                  <button
                    type="button"
                    class="primary"
                    @click="start(persona.persona_id)"
                  >
                    {{ PARTNERS.start }}
                  </button>
                  <button
                    type="button"
                    class="ghost"
                    @click="router.push({ name: 'personas' })"
                  >
                    {{ PARTNERS.manage }}
                  </button>
                </div>
              </li>
            </ul>
          </template>
        </div>
      </div>
    </SurfaceGate>
  </AppShell>
</template>

<style scoped>
.page__inner {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.ghost {
  padding: 6px 12px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text-muted);
  font-size: 12px;
  cursor: pointer;
}

.ghost:hover {
  border-color: var(--accent);
  color: var(--accent);
}

.primary {
  padding: 6px 14px;
  border: 1px solid var(--accent);
  border-radius: var(--radius-sm);
  background: var(--accent);
  color: #fff;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
}

.count {
  color: var(--text-faint);
  font-size: 11px;
}

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 10px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.card {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 13px 15px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
}

.card__head {
  display: flex;
  gap: 10px;
  align-items: baseline;
  justify-content: space-between;
}

.card__name {
  margin: 0;
  font-size: 14px;
  font-weight: 650;
}

.card__date {
  flex: none;
  color: var(--text-faint);
  font-size: 10px;
}

.card__description {
  flex: 1;
  margin: 0;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.6;
}

.card__description--muted {
  color: var(--text-faint);
}

.card__summary {
  color: var(--accent);
  font-size: 11px;
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
  font-size: 11px;
  line-height: 1.7;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.card__actions {
  display: flex;
  gap: 6px;
}
</style>
