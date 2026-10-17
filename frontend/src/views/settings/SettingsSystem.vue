<script setup lang="ts">
/**
 * 运行状态与版本。
 *
 * 两块独立取数、各自失败：版本读不出来不影响依赖检查，反之亦然。
 * `unfinished_calls` 缺省时画「没有这个数」，不画 0 —— 读不到和「没有」
 * 是两件事。
 *
 * 本版没有在线升级，所以这里也不放升级按钮。
 */

import { onMounted, ref } from 'vue'

import type { ApiFailure } from '../../api/client'
import {
  getStatus,
  getVersion,
  type SystemStatus,
  type SystemVersion,
} from '../../api/system'
import FailureState from '../../components/common/FailureState.vue'
import PartialBanner from '../../components/common/PartialBanner.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import { SETTINGS } from '../../copy/settings'
import { formatDateTime, formatNumber } from '../../shared/format'
import { toFailure } from '../../shared/resource'

type Status = 'loading' | 'ready' | 'failed'

const version = ref<SystemVersion | null>(null)
const versionStatus = ref<Status>('loading')
const versionFailure = ref<ApiFailure | null>(null)

const status = ref<SystemStatus | null>(null)
const statusStatus = ref<Status>('loading')
const statusFailure = ref<ApiFailure | null>(null)
const statusRefreshFailure = ref<ApiFailure | null>(null)

async function loadVersion(): Promise<void> {
  versionStatus.value = 'loading'
  versionFailure.value = null
  try {
    version.value = await getVersion()
    versionStatus.value = 'ready'
  } catch (cause) {
    versionFailure.value = toFailure(cause)
    versionStatus.value = 'failed'
  }
}

async function loadStatus(): Promise<void> {
  const hadData = status.value !== null
  if (!hadData) {
    statusStatus.value = 'loading'
    statusFailure.value = null
  }
  statusRefreshFailure.value = null
  try {
    status.value = await getStatus()
    statusStatus.value = 'ready'
  } catch (cause) {
    const next = toFailure(cause)
    if (hadData) {
      statusRefreshFailure.value = next
      statusStatus.value = 'ready'
    } else {
      statusFailure.value = next
      statusStatus.value = 'failed'
    }
  }
}

function reload(): void {
  void loadVersion()
  void loadStatus()
}

onMounted(() => {
  void Promise.allSettled([loadVersion(), loadStatus()])
})
</script>

<template>
  <div class="system">
    <div class="toolbar">
      <button type="button" class="ghost" @click="reload">重新读取</button>
    </div>

    <!-- 版本 -->
    <section class="card">
      <h2 class="card__title">{{ SETTINGS.about }}</h2>
      <SkeletonBlock v-if="versionStatus === 'loading'" :lines="3" :line-height="24" />
      <FailureState
        v-else-if="versionStatus === 'failed' && versionFailure"
        :failure="versionFailure"
        title="版本没能读出来"
        @retry="loadVersion"
      />
      <dl v-else-if="version" class="meta">
        <dt>{{ SETTINGS.version }}</dt>
        <dd>{{ version.version }}</dd>
        <dt>{{ SETTINGS.goVersion }}</dt>
        <dd>{{ version.go_version }}</dd>
        <dt>{{ SETTINGS.migration }}</dt>
        <dd>{{ version.migration_version }}</dd>
        <template v-if="version.build_time">
          <dt>{{ SETTINGS.buildTime }}</dt>
          <dd>{{ formatDateTime(version.build_time) }}</dd>
        </template>
      </dl>
      <p class="hint">{{ SETTINGS.noUpdate }}</p>
    </section>

    <!-- 运行状态 -->
    <section class="card">
      <h2 class="card__title">{{ SETTINGS.system }}</h2>

      <PartialBanner
        v-if="statusRefreshFailure"
        :failure="statusRefreshFailure"
        text="这次没有刷新成功，下面仍是上一次的状态"
        @retry="loadStatus"
      />

      <SkeletonBlock v-if="statusStatus === 'loading'" :lines="4" :line-height="24" />
      <FailureState
        v-else-if="statusStatus === 'failed' && statusFailure"
        :failure="statusFailure"
        title="运行状态没能读出来"
        @retry="loadStatus"
      />

      <template v-else-if="status">
        <dl class="meta">
          <dt>环境</dt>
          <dd>{{ status.env || '—' }}</dd>
          <dt>{{ SETTINGS.activeTurns }}</dt>
          <dd>{{ formatNumber(status.active_turns) }}</dd>
          <dt>{{ SETTINGS.indexing }}</dt>
          <dd>{{ status.indexing ? '正在建立索引' : '没有正在进行的索引任务' }}</dd>
          <dt>{{ SETTINGS.unfinishedCalls }}</dt>
          <dd>
            <template v-if="status.unfinished_calls !== undefined">
              {{ formatNumber(status.unfinished_calls) }}
            </template>
            <template v-else>服务端没有提供这个数</template>
          </dd>
        </dl>

        <h3 class="subtitle">{{ SETTINGS.checks }}</h3>
        <ul v-if="status.checks.length > 0" class="checks">
          <li v-for="check in status.checks" :key="check.name" class="check">
            <span class="check__mark" :class="check.ok ? 'check__mark--ok' : 'check__mark--bad'">
              {{ check.ok ? '正常' : '异常' }}
            </span>
            <span class="check__name">{{ check.name }}</span>
          </li>
        </ul>
        <p v-else class="hint">服务端没有给出依赖检查项。</p>
      </template>
    </section>
  </div>
</template>

<style scoped>
.system {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.toolbar {
  display: flex;
  justify-content: flex-end;
}

.card {
  padding: 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
}

.card__title {
  margin: 0 0 10px;
  font-size: 13px;
  font-weight: 650;
}

.meta {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 5px 14px;
  margin: 0;
  font-size: 12px;
}

.meta dt {
  color: var(--text-faint);
}

.meta dd {
  margin: 0;
  color: var(--text);
  font-family: var(--font-mono);
  font-size: 11px;
  word-break: break-all;
}

.subtitle {
  margin: 16px 0 7px;
  font-size: 12px;
  font-weight: 600;
}

.checks {
  display: flex;
  flex-direction: column;
  gap: 5px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.check {
  display: flex;
  gap: 8px;
  align-items: baseline;
  font-size: 12px;
}

.check__mark {
  padding: 0 6px;
  border: 1px solid var(--border-strong);
  border-radius: 8px;
  font-size: 9px;
  line-height: 16px;
}

.check__mark--ok {
  border-color: var(--success);
  color: var(--success);
}

.check__mark--bad {
  border-color: var(--danger);
  color: var(--danger);
}

.check__name {
  color: var(--text);
  font-family: var(--font-mono);
  font-size: 11px;
}

.hint {
  margin: 8px 0 0;
  color: var(--text-muted);
  font-size: 11px;
  line-height: 1.6;
}

.ghost {
  padding: 4px 11px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text-muted);
  font-size: 11px;
  cursor: pointer;
}
</style>
