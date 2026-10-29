<script setup lang="ts">
/**
 * 沉浸阅读的独立页面外壳。
 *
 * 页面本体 `learning/Reading.vue` 没有自己的骨架，原来由 `LearningLayout`
 * 提供；从学习分组独立出来后自己带 `AppShell`。
 */

import AppShell from '../components/shell/AppShell.vue'
import Reading from './learning/Reading.vue'
</script>

<template>
  <AppShell>
    <div class="page reading-page">
      <div class="page__inner">
        <Reading />
      </div>
    </div>
  </AppShell>
</template>

<style scoped>
/*
 * 阅读页不吃 `.page` 的上下内边距：三栏要跟左侧主侧栏一样，从骨架的顶边
 * 一直铺到底边，而不是上下各空 32px。左右 40px 与其它内容页一致。
 */
.reading-page {
  padding: 0 40px;
}
</style>
