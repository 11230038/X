<script setup lang="ts">
/**
 * 活书详情页的独立页面外壳。
 *
 * 页面本体 `learning/Book.vue` 没有自己的骨架，原来由 `LearningLayout`
 * 提供；从学习分组独立出来后自己带 `AppShell`。`bookId` 仍由路由参数经
 * `props: true` 传进来，组件内部继续 `watch` 它。
 */

import AppShell from '../components/shell/AppShell.vue'
import Book from './learning/Book.vue'

defineProps<{ bookId: string }>()
</script>

<template>
  <AppShell>
    <div class="page">
      <div class="page__inner">
        <Book :book-id="bookId" />
      </div>
    </div>
  </AppShell>
</template>
