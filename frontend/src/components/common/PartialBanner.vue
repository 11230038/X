<script setup lang="ts">
/**
 * 部分失败条。
 *
 * 用在三种场景，共同点是**页面仍然可用**：刷新失败但旧数据还在、子资源失败、
 * 权限快照取不到。它挂在内容上方，不遮挡内容，也不把内容换掉。
 */

import { computed } from 'vue'
import type { ApiFailure } from '../../api/client'

const props = withDefaults(
  defineProps<{
    failure?: ApiFailure | null
    text?: string
    /** 有重试动作时显示按钮；纯提示传 false。 */
    retryable?: boolean
    tone?: 'warning' | 'muted'
  }>(),
  { failure: null, text: '', retryable: true, tone: 'warning' },
)

defineEmits<{
  retry: []
}>()

const message = computed(() => {
  if (props.text) return props.text
  if (props.failure?.kind === 'rate_limited') return '请求太频繁，稍后会重新加载。'
  if (props.failure?.kind === 'quota') return '本期额度已用完，这部分没有更新。'
  return '有一部分没有加载出来。'
})

const requestId = computed(() => props.failure?.requestId ?? '')
</script>

<template>
  <div class="banner" :class="`banner--${tone}`" role="status">
    <span class="banner__text">{{ message }}</span>
    <code v-if="requestId" class="banner__id" :title="requestId">{{ requestId }}</code>
    <button v-if="retryable" type="button" class="banner__retry" @click="$emit('retry')">
      重试
    </button>
  </div>
</template>

<style scoped>
.banner {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 7px 12px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  font-size: 14px;
}

.banner--warning {
  border-color: var(--warning);
  background: var(--warning-soft);
}

.banner--muted {
  background: var(--surface-sunken);
}

.banner__text {
  flex: 1;
  min-width: 0;
  color: var(--text);
}

.banner__id {
  max-width: 160px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-faint);
  font-family: var(--font-mono);
  font-size: 14px;
}

.banner__retry {
  flex: none;
  padding: 2px 8px;
  border: 1px solid var(--border-strong);
  border-radius: 4px;
  background: transparent;
  color: var(--text);
  font-size: 14px;
  cursor: pointer;
}

.banner__retry:hover {
  background: var(--surface-raised);
}
</style>
