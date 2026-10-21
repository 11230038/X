<script setup lang="ts">
/**
 * 练习的独立页面外壳。
 *
 * 页面本体 `learning/Practice.vue` 没有自己的骨架，原来由 `LearningLayout`
 * 提供；练习归到题库下之后不再挂在学习下，这一层只补回 `AppShell` 与页面
 * 留白，内容仍然复用同一个组件。
 */

import AppShell from '../components/shell/AppShell.vue'
import Practice from './learning/Practice.vue'
</script>

<template>
  <AppShell>
    <div class="page">
      <div class="page__inner">
        <Practice />
      </div>
    </div>
  </AppShell>
</template>
