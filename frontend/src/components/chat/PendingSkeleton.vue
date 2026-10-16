<script setup lang="ts">
/**
 * 内容区在等一次读回来时的骨架屏。
 *
 * 它**不是**空态，两者不能长得像（§6.2 硬规则 1）：空态是一个可点的动作，这
 * 一条只是"还在读"。所以它没有文字、`aria-hidden`，对读屏软件等于不存在。
 */
</script>

<template>
  <div class="pending" aria-hidden="true">
    <span class="pending__bar" />
    <span class="pending__bar pending__bar--short" />
  </div>
</template>

<style scoped>
.pending {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 24px;
}

.pending__bar {
  height: 14px;
  border-radius: var(--radius-sm);
  background: linear-gradient(90deg, var(--surface-sunken), #eceef2, var(--surface-sunken));
  background-size: 200% 100%;
  animation: pending-sheen 1.4s ease-in-out infinite;
}

.pending__bar--short {
  width: 55%;
}

@keyframes pending-sheen {
  from {
    background-position: 200% 0;
  }

  to {
    background-position: -200% 0;
  }
}

@media (prefers-reduced-motion: reduce) {
  .pending__bar {
    animation: none;
  }
}
</style>
