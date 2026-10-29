<script setup lang="ts">
/**
 * 额度用尽。与失败分开：它不是一个"重试就会好"的错误，重试只会浪费一次
 * 往返；页面上也不画升级入口 —— 这个版本没有购买或升级路径。
 */

import { formatDateTime } from '../../shared/format'

withDefaults(
  defineProps<{
    resetAt?: string
    detail?: string
  }>(),
  { resetAt: '', detail: '' },
)

defineEmits<{
  back: []
}>()
</script>

<template>
  <div class="quota" role="status">
    <p class="quota__title">本期额度已用完</p>
    <p v-if="detail" class="quota__detail">{{ detail }}</p>
    <p v-if="resetAt" class="quota__reset">额度将在 {{ formatDateTime(resetAt) }} 重置</p>
    <p v-else class="quota__reset">额度重置时间由管理员设定。</p>
    <button type="button" class="quota__back" @click="$emit('back')">返回</button>
  </div>
</template>

<style scoped>
.quota {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 5px;
  padding: 18px;
  border: 1px solid var(--warning);
  border-radius: var(--radius);
  background: var(--warning-soft);
}

.quota__title {
  margin: 0;
  color: var(--text);
  font-size: 15px;
  font-weight: 600;
}

.quota__detail,
.quota__reset {
  margin: 0;
  color: var(--text-muted);
  font-size: 14px;
  line-height: 1.6;
}

.quota__back {
  margin-top: 6px;
  padding: 7px 16px;
  border: 1px solid rgb(0 0 0 / 8%);
  border-radius: 999px;
  background: rgb(255 255 255 / 78%);
  color: var(--text);
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.2s ease;
}

.quota__back:hover {
  border-color: var(--accent);
  background: #fff;
}
</style>
