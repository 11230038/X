<script setup lang="ts">
/**
 * 429 的专用形态：**倒计时，不登出**。
 *
 * 429 与 401 的区别是整条鉴权设计的核心之一：429 说明服务端认识你，只是让
 * 你慢一点；把 429 画成"登录失效"会让人在明明只需等两秒的时候丢掉当前
 * 页面。倒计时归零后发一次 `expired`，由父组件决定重发哪个请求 —— 这个
 * 组件自己不发请求，避免不同的页面在这里分叉。
 */

import { onBeforeUnmount, onMounted, ref, watch } from 'vue'

const props = withDefaults(
  defineProps<{
    retryAfterSeconds: number
    label?: string
  }>(),
  { label: '请求太频繁' },
)

const emit = defineEmits<{
  expired: []
}>()

const remaining = ref(Math.max(0, Math.ceil(props.retryAfterSeconds)))
let timer: number | null = null
let fired = false

function stop(): void {
  if (timer !== null) {
    window.clearInterval(timer)
    timer = null
  }
}

function tick(): void {
  if (remaining.value > 0) {
    remaining.value -= 1
  }
  if (remaining.value <= 0 && !fired) {
    fired = true
    stop()
    emit('expired')
  }
}

function start(): void {
  stop()
  fired = false
  remaining.value = Math.max(0, Math.ceil(props.retryAfterSeconds))
  if (remaining.value <= 0) {
    fired = true
    emit('expired')
    return
  }
  timer = window.setInterval(tick, 1000)
}

watch(() => props.retryAfterSeconds, start)
onMounted(start)
onBeforeUnmount(stop)
</script>

<template>
  <div class="limited" role="status">
    <span class="limited__mark" aria-hidden="true">⏳</span>
    <div class="limited__body">
      <p class="limited__title">{{ label }}</p>
      <p class="limited__count">
        <span class="limited__number">{{ remaining }}</span> 秒后自动重试
      </p>
      <p class="limited__hint">这不是登录失效，页面内容不会丢失。</p>
    </div>
  </div>
</template>

<style scoped>
.limited {
  display: flex;
  gap: 14px;
  padding: 14px 16px;
  border: 1px solid var(--warning);
  border-radius: var(--radius);
  background: var(--warning-soft);
}

.limited__mark {
  font-size: 16px;
  line-height: 1.3;
}

.limited__body {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.limited__title {
  margin: 0;
  color: var(--text);
  font-size: 14px;
  font-weight: 600;
}

.limited__count {
  margin: 0;
  color: var(--text);
  font-size: 14px;
}

.limited__number {
  font-family: var(--font-mono);
  font-weight: 700;
}

.limited__hint {
  margin: 0;
  color: var(--text-muted);
  font-size: 14px;
}
</style>
