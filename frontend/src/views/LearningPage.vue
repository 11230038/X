<script setup lang="ts">
/**
 * 学习首页的独立页面外壳。
 *
 * 学习分组现在只剩首页；原来由 `LearningLayout` 提供的 `AppShell` 与页面
 * 留白搬到这里。
 */

import AppShell from '../components/shell/AppShell.vue'
import LearningHome from './learning/LearningHome.vue'
</script>

<template>
  <AppShell>
    <div class="page">
      <div class="page__inner">
        <LearningHome />
      </div>
    </div>
  </AppShell>
</template>
