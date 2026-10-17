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
  </div>
</template>

<style scoped>
.about {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
}

.meta {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 6px 16px;
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

.hint {
  margin: 0;
  color: var(--text-muted);
  font-size: 11px;
  line-height: 1.6;
}

.link {
  color: var(--accent);
  text-decoration: none;
}
</style>
