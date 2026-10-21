<script setup lang="ts">
/**
 * 视频学习的独立页面外壳。
 *
 * 页面本体 `learning/Watching.vue` 没有自己的骨架，原来由 `LearningLayout`
 * 提供；视频学习从学习分组里独立出来后自己带 `AppShell` 与页面留白，内容
 * 仍然复用同一个组件。
 */

import AppShell from '../components/shell/AppShell.vue'
import Watching from './learning/Watching.vue'
</script>

<template>
  <AppShell>
    <div class="page">
      <div class="page__inner">
        <Watching />
      </div>
    </div>
  </AppShell>
</template>
