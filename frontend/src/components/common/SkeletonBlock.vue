<script setup lang="ts">
/**
 * 首次加载的骨架。只用于**没有任何旧数据**的时候；刷新时保留旧内容，改用
 * `PartialBanner`。加载与失败必须长得不一样 —— 骨架会动，失败不会。
 */

withDefaults(
  defineProps<{
    lines?: number
    /** 每一行的高度，单位 px。 */
    lineHeight?: number
    /** 最后一行按比例缩短，读起来像段落。 */
    compact?: boolean
  }>(),
  { lines: 3, lineHeight: 14, compact: false },
)
</script>

<template>
  <div class="skeleton" role="status" aria-live="polite" aria-label="正在加载">
    <span
      v-for="index in lines"
      :key="index"
      class="skeleton__line"
      :class="{ 'skeleton__line--short': compact && index === lines }"
      :style="{ height: `${lineHeight}px` }"
    ></span>
  </div>
</template>

<style scoped>
.skeleton {
  display: flex;
  flex-direction: column;
  gap: 10px;
  width: 100%;
  padding: 4px 0;
}

.skeleton__line {
  display: block;
  width: 100%;
  border-radius: 4px;
  background: linear-gradient(
    90deg,
    var(--surface-sunken) 0%,
    var(--surface-raised) 45%,
    var(--surface-sunken) 90%
  );
  background-size: 200% 100%;
  animation: skeleton-shimmer 1.4s ease-in-out infinite;
}

.skeleton__line--short {
  width: 62%;
}

@keyframes skeleton-shimmer {
  0% {
    background-position: 140% 0;
  }
  100% {
    background-position: -40% 0;
  }
}

@media (prefers-reduced-motion: reduce) {
  .skeleton__line {
    animation: none;
  }
}
</style>
