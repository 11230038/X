<script setup lang="ts">
/**
 * 权限拒绝。
 *
 * 三件事必须说清楚，缺一件人就会去问"为什么打不开"：
 * 1. **缺哪一项**（面或具体项目）；
 * 2. **哪一层拒的** —— `preset`（预设没开放）和 `guardian`（监护人关掉）
 *    是同一个 false，但完全是两件事；
 * 3. **一句原因**，用后端给的稳定 `reason` 码查表，查不到就显示原码。
 *
 * 不重定向：把人悄悄送回首页会让人以为页面不存在。
 */

import { computed } from 'vue'
import {
  LAYER_LABELS,
  PERMISSION_KIND_LABELS,
  REASON_LABELS,
  SURFACE_LABELS,
} from '../../copy/permissions'

const props = withDefaults(
  defineProps<{
    surface?: string
    kind?: string
    itemId?: string
    layer?: string
    reason?: string
  }>(),
  { surface: '', kind: '', itemId: '', layer: '', reason: '' },
)

defineEmits<{
  back: []
}>()

const surfaceLabel = computed(() => {
  if (!props.surface) return ''
  return SURFACE_LABELS[props.surface] ?? props.surface
})

const target = computed(() => {
  if (surfaceLabel.value) return `「${surfaceLabel.value}」`
  if (props.itemId) return `「${props.itemId}」`
  return ''
})

const layerLabel = computed(() => {
  if (!props.layer) return ''
  return LAYER_LABELS[props.layer] ?? props.layer
})

const reasonLabel = computed(() => {
  if (!props.reason) return ''
  return REASON_LABELS[props.reason] ?? props.reason
})

const kindLabel = computed(() => {
  if (!props.kind) return ''
  return PERMISSION_KIND_LABELS[props.kind] ?? props.kind
})
</script>

<template>
  <div class="denied" role="alert">
    <p class="denied__title">没有访问{{ target }}的权限</p>
    <p v-if="layerLabel" class="denied__layer">
      这一项由<strong>{{ layerLabel }}</strong>决定。
    </p>
    <p v-if="reasonLabel" class="denied__reason">原因：{{ reasonLabel }}</p>
    <p v-if="kindLabel && itemId" class="denied__item">
      项目：{{ kindLabel }} · <code>{{ itemId }}</code>
    </p>
    <button type="button" class="denied__back" @click="$emit('back')">返回</button>

    <details class="denied__why">
      <summary>如何调整</summary>
      <p>
        权限由账号角色、预设、单独授权和监护关系共同决定。管理台可以逐项打开或关闭；
        其中标记为底线的项目不能被关闭。如果这不是你能改的，请联系管理员。
      </p>
    </details>
  </div>
</template>

<style scoped>
.denied {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
  max-width: 520px;
  padding: 20px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius);
  background: var(--surface-sunken);
}

.denied__title {
  margin: 0;
  color: var(--text);
  font-size: 15px;
  font-weight: 600;
}

.denied__layer,
.denied__reason,
.denied__item {
  margin: 0;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.6;
}

.denied__item code {
  font-family: var(--font-mono);
}

.denied__back {
  margin-top: 8px;
  padding: 5px 12px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  font-size: 12px;
  cursor: pointer;
}

.denied__why {
  margin-top: 4px;
  color: var(--text-muted);
  font-size: 12px;
}

.denied__why summary {
  cursor: pointer;
  color: var(--text-muted);
}

.denied__why p {
  margin: 6px 0 0;
  line-height: 1.65;
}
</style>
