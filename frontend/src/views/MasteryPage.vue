<script setup lang="ts">
/**
 * 掌握路径的独立页面外壳。
 *
 * 页面本体 `learning/Mastery.vue` 没有自己的骨架，原来由 `LearningLayout`
 * 提供；掌握路径升为一级入口后不再挂在学习下，这一层只补回 `AppShell` 与
 * 页面留白，内容仍然复用同一个组件。
 */

import AppShell from '../components/shell/AppShell.vue'
import Mastery from './learning/Mastery.vue'
</script>

<template>
  <AppShell>
    <div class="page">
      <div class="page__inner">
        <Mastery />
      </div>
    </div>
  </AppShell>
</template>
