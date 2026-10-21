<script setup lang="ts">
/**
 * 活书书架的独立页面外壳。
 *
 * 页面本体 `learning/Books.vue` 没有自己的骨架，原来由 `LearningLayout`
 * 提供；从学习分组独立出来后自己带 `AppShell`。
 */

import AppShell from '../components/shell/AppShell.vue'
import Books from './learning/Books.vue'
</script>

<template>
  <AppShell>
    <div class="page">
      <div class="page__inner">
        <Books />
      </div>
    </div>
  </AppShell>
</template>
