<script setup lang="ts">
/**
 * 首次加载失败。
 *
 * 三类信息缺一不可：**哪一类失败**（网络 / 服务端拒绝 / 读不懂）、**怎么
 * 继续**（重试、出口）、**怎么报障**（`request_id` 可复制）。`request_id`
 * 是后端定位一次请求的唯一线索，没有它，"刚才那一下失败了" 无法追查。
 */

import { computed } from 'vue'
import type { ApiFailure } from '../../api/client'
import { notify } from '../../shared/toast'

const props = withDefaults(
  defineProps<{
    failure: ApiFailure
    title?: string
    retryLabel?: string
    exitLabel?: string
  }>(),
  { title: '', retryLabel: '重试', exitLabel: '' },
)

defineEmits<{
  retry: []
  exit: []
}>()

const description = computed(() => {
  switch (props.failure.kind) {
    case 'network':
      return '网络不可达，请检查连接后重试。'
    case 'rate_limited':
      return props.failure.retryAfterSeconds
        ? `请求太频繁，请等 ${props.failure.retryAfterSeconds} 秒。`
        : '请求太频繁，请稍候再试。'
    case 'quota':
      return '本期可用额度已经用完，等额度重置后再试。'
    case 'malformed':
      return '服务端返回了无法解析的响应。这通常是版本不一致。'
    case 'unauthorized':
      return '登录状态已失效，请重新登录。'
    default:
      return props.failure.message
  }
})

const detail = computed(() => {
  const parts: string[] = []
  if (props.failure.code) parts.push(props.failure.code)
  if (props.failure.status) parts.push(`HTTP ${props.failure.status}`)
  return parts.join(' · ')
})

async function copyRequestId(): Promise<void> {
  if (!props.failure.requestId) return
  try {
    await navigator.clipboard.writeText(props.failure.requestId)
    notify('请求编号已复制', 'success')
  } catch {
    notify('复制失败，请手动选择文本', 'error')
  }
}
</script>

<template>
  <div class="failure" role="alert">
    <div class="failure__mark" aria-hidden="true">!</div>
    <div class="failure__body">
      <p class="failure__title">{{ title || '加载失败' }}</p>
      <p class="failure__description">{{ description }}</p>
      <p v-if="detail" class="failure__detail">{{ detail }}</p>
      <p v-if="failure.requestId" class="failure__request">
        <span class="failure__request-label">请求编号</span>
        <code>{{ failure.requestId }}</code>
        <button type="button" class="failure__copy" @click="copyRequestId">复制</button>
      </p>
      <div class="failure__actions">
        <button type="button" class="failure__retry" @click="$emit('retry')">
          {{ retryLabel }}
        </button>
        <button
          v-if="exitLabel"
          type="button"
          class="failure__exit"
          @click="$emit('exit')"
        >
          {{ exitLabel }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.failure {
  display: flex;
  gap: 10px;
  padding: 18px;
  border: 1px solid var(--danger);
  border-radius: var(--radius);
  background: var(--danger-soft);
}

.failure__mark {
  flex: none;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  background: var(--danger);
  color: #fff;
  font-size: 13px;
  font-weight: 700;
  line-height: 20px;
  text-align: center;
}

.failure__body {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.failure__title {
  margin: 0;
  color: var(--text);
  font-size: 14px;
  font-weight: 600;
}

.failure__description {
  margin: 0;
  color: var(--text);
  font-size: 13px;
  line-height: 1.6;
}

.failure__detail {
  margin: 0;
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: 11px;
}

.failure__request {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 2px 0 0;
  font-size: 11px;
}

.failure__request-label {
  color: var(--text-faint);
}

.failure__request code {
  color: var(--text-muted);
  font-family: var(--font-mono);
  user-select: all;
}

.failure__copy {
  padding: 1px 6px;
  border: 1px solid var(--border-strong);
  border-radius: 4px;
  background: transparent;
  color: var(--text-muted);
  font-size: 11px;
  cursor: pointer;
}

.failure__actions {
  display: flex;
  gap: 8px;
  margin-top: 8px;
}

.failure__retry {
  padding: 5px 12px;
  border: 1px solid var(--accent);
  border-radius: var(--radius-sm);
  background: var(--accent);
  color: #fff;
  font-size: 12px;
  cursor: pointer;
}

.failure__exit {
  padding: 5px 12px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text-muted);
  font-size: 12px;
  cursor: pointer;
}
</style>
