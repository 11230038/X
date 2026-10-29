<script setup lang="ts">
/**
 * 列表底部的分页控件。
 *
 * 三种形状分开：还能加载（按钮）、正在加载（按钮变文字，不再可点）、
 * 加载失败（就地重试，已经拿到的行不消失）。"没有更多了"只在确实取到过
 * 数据时显示 —— 空列表用 `EmptyState`，不是这里。
 */

import type { ApiFailure } from '../../api/client'

withDefaults(
  defineProps<{
    loading?: boolean
    exhausted?: boolean
    failure?: ApiFailure | null
    label?: string
  }>(),
  { loading: false, exhausted: false, failure: null, label: '加载更多' },
)

defineEmits<{
  load: []
}>()
</script>

<template>
  <div class="more">
    <span v-if="loading" class="more__loading">正在加载…</span>
    <template v-else-if="failure">
      <span class="more__failed">加载失败</span>
      <button type="button" class="more__button" @click="$emit('load')">重试</button>
    </template>
    <span v-else-if="exhausted" class="more__exhausted">没有更多了</span>
    <button v-else type="button" class="more__button" @click="$emit('load')">{{ label }}</button>
  </div>
</template>

<style scoped>
.more {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 10px 0;
  font-size: 14px;
  color: var(--text-muted);
}

.more__button {
  padding: 6px 16px;
  border: 1px solid rgb(0 0 0 / 8%);
  border-radius: 999px;
  background: rgb(255 255 255 / 78%);
  color: var(--text);
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.2s ease;
}

.more__button:hover {
  border-color: var(--accent);
  background: #fff;
}

.more__failed {
  color: var(--danger);
}
</style>
