<script setup lang="ts">
/**
 * 关于：版本、构建时间、数据库迁移版本。
 *
 * `build_time` 是可选字段；没有就整行不画，不编一个时间。这个版本没有在线
 * 升级，所以页面也不放升级按钮。
 */

import { onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'

import type { ApiFailure } from '../../api/client'
import { getVersion, type SystemVersion } from '../../api/system'
import FailureState from '../../components/common/FailureState.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import { SETTINGS } from '../../copy/settings'
import { formatDateTime } from '../../shared/format'
import { toFailure } from '../../shared/resource'
import AppIcon from '../../components/common/AppIcon.vue'

const version = ref<SystemVersion | null>(null)
const status = ref<'loading' | 'ready' | 'failed'>('loading')
const failure = ref<ApiFailure | null>(null)

async function load(): Promise<void> {
  status.value = 'loading'
  failure.value = null
  try {
    version.value = await getVersion()
    status.value = 'ready'
  } catch (cause) {
    failure.value = toFailure(cause)
    status.value = 'failed'
  }
}

onMounted(() => {
  void load()
})
</script>

<template>
  <div class="about">
    <section class="set-panel">
      <div class="set-panel__head">
        <span class="set-panel__icon" aria-hidden="true"><AppIcon name="info" /></span>
        <h2 class="set-panel__title">{{ SETTINGS.versionInfo }}</h2>
      </div>

      <SkeletonBlock v-if="status === 'loading'" :lines="4" :line-height="24" />

      <FailureState
        v-else-if="status === 'failed' && failure"
        :failure="failure"
        :title="SETTINGS.about"
        @retry="load"
      />

      <template v-else-if="version">
        <dl class="meta">
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
        <p class="hint">
          依赖是否正常、有没有正在跑的索引，在
          <RouterLink class="link" :to="{ name: 'settings-system' }">
            {{ SETTINGS.system }}
          </RouterLink>
          里。
        </p>
      </template>
    </section>
  </div>
</template>

<style scoped>
.about {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

/* 名值表：与运行状态那一页同一套行距与分隔线。 */
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

.hint {
  margin: 12px 0 0;
  color: var(--text-muted);
  font-size: 14px;
  line-height: 1.6;
}

.link {
  color: var(--accent-ink);
  text-decoration: none;
  font-weight: 600;
}

.link:hover {
  text-decoration: underline;
  text-underline-offset: 2px;
}
</style>
