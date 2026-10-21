<script setup lang="ts">
/**
 * 学习地图的独立页面外壳。
 *
 * 页面本体 `learning/LearningMap.vue` 没有自己的骨架，原来由
 * `LearningLayout` 提供；从学习分组独立出来后自己带 `AppShell`。
 */

import AppShell from '../components/shell/AppShell.vue'
import LearningMap from './learning/LearningMap.vue'
</script>

<template>
  <AppShell>
    <div class="page">
      <div class="page__inner">
        <LearningMap />
      </div>
    </div>
  </AppShell>
</template>
