<script setup lang="ts">
/**
 * 路由面的权限门。
 *
 * 三态：
 * - `true` / `'unknown'` → 渲染页面；
 * - `false` → 渲染 `PermissionDenied`，**不重定向**；
 * - 快照取失败（`'unknown'` 且 status=failed）→ 页面照常可用，顶部挂一条
 *   "权限信息暂不可用"的部分失败条。后端每个请求仍会判一次，前端不越权替
 *   它关页面。
 */

import { computed } from 'vue'
import { useRouter } from 'vue-router'

import { canSurface, permissionFailure, permissionStatus, surfaceDecision } from '../../auth/permissions'
import PartialBanner from './PartialBanner.vue'
import PermissionDenied from './PermissionDenied.vue'

const props = defineProps<{ surface: string }>()

const router = useRouter()

const allowed = computed(() => canSurface(props.surface))
const decision = computed(() => surfaceDecision(props.surface))
const showUnknown = computed(
  () => allowed.value === 'unknown' && permissionStatus.value === 'failed',
)

function goBack(): void {
  if (window.history.length > 1) {
    router.back()
  } else {
    void router.push({ name: 'chat' })
  }
}
</script>

<template>
  <PermissionDenied
    v-if="allowed === false"
    :surface="surface"
    :layer="decision?.layer"
    :reason="decision?.reason"
    @back="goBack"
  />
  <div v-else class="gate" :class="{ 'gate--stacked': showUnknown }">
    <PartialBanner
      v-if="showUnknown"
      :failure="permissionFailure"
      text="权限信息暂不可用，部分入口可能隐藏"
      :retryable="false"
      tone="muted"
    />
    <slot></slot>
  </div>
</template>

<style scoped>
/* `display: contents` 让被包的页面仍然是父容器的直接布局子项；只有真的
   要放一条提示时才变成纵向堆叠。 */
.gate {
  display: contents;
}

.gate--stacked {
  display: flex;
  flex-direction: column;
  gap: 10px;
  min-height: 0;
  padding: 10px 16px 0;
}
</style>
