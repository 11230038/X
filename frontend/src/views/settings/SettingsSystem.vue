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
import AppIcon from '../../components/common/AppIcon.vue'

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
    <!-- 版本 -->
    <section class="set-panel">
      <div class="set-panel__head">
        <span class="set-panel__icon" aria-hidden="true"><AppIcon name="tag" /></span>
        <h2 class="set-panel__title">{{ SETTINGS.about }}</h2>
      </div>
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
    <section class="set-panel">
      <div class="set-panel__head">
        <span class="set-panel__icon set-panel__icon--ok" aria-hidden="true"><AppIcon name="pulse" /></span>
        <h2 class="set-panel__title">{{ SETTINGS.system }}</h2>
        <!-- 重新读取放在标题行右侧：它是这一页的动作，两块都会重读。 -->
        <div class="set-panel__actions">
          <button type="button" class="set-btn set-btn--ghost set-btn--sm" @click="reload">
            重新读取
          </button>
        </div>
      </div>

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
            <span class="set-chip" :class="check.ok ? 'set-chip--ok' : 'set-chip--bad'">
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
  gap: 20px;
}

/* 名值表：一行一条，行间一条浅线。名字窄、值宽，值用等宽字。 */
.meta {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  gap: 0 20px;
  margin: 0;
  font-size: 14px;
}

.meta dt,
.meta dd {
  padding: 11px 0;
  border-bottom: 1px solid rgb(0 0 0 / 5%);
}

.meta dt {
  color: var(--text-faint);
  font-size: 13px;
  font-weight: 600;
}

.meta dd {
  margin: 0;
  color: var(--text);
  font-family: var(--font-mono);
  font-size: 14px;
  word-break: break-all;
}

.subtitle {
  margin: 20px 0 10px;
  color: var(--text-muted);
  font-size: 13px;
  font-weight: 700;
  letter-spacing: 0.02em;
}

.checks {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.check {
  display: flex;
  gap: 10px;
  align-items: center;
  padding: 7px 10px;
  border-radius: 10px;
  font-size: 14px;
}

.check:hover {
  background: rgb(255 255 255 / 45%);
}

.check__name {
  color: var(--text);
  font-family: var(--font-mono);
  font-size: 14px;
}

.hint {
  margin: 8px 0 0;
  color: var(--text-muted);
  font-size: 14px;
  line-height: 1.6;
}
</style>
