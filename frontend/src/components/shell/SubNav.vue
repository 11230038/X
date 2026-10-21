<script setup lang="ts">
/**
 * 页内子导航（标签式）。
 *
 * 一级导航只有六个入口；某个入口下的页面靠这一条切换。用 RouterLink 而不是标签
 * 按钮：每个子页都有独立地址，刷新与深链都落得回去。
 */

import { RouterLink } from 'vue-router'

defineProps<{
  items: ReadonlyArray<{ label: string; to: string }>
}>()
</script>

<template>
  <nav class="subnav" aria-label="页内导航">
    <RouterLink
      v-for="item in items"
      :key="item.to"
      class="subnav__item"
      active-class="subnav__item--active"
      :to="item.to"
    >
      {{ item.label }}
    </RouterLink>
  </nav>
</template>

<style scoped>
.subnav {
  display: flex;
  flex-wrap: wrap;
  gap: 2px;
  padding-bottom: 10px;
  border-bottom: 1px solid var(--border);
}

.subnav__item {
  padding: 5px 10px;
  border-radius: var(--radius-sm);
  color: var(--text-muted);
  font-size: 12px;
  text-decoration: none;
}

.subnav__item:hover {
  background: var(--surface-sunken);
  color: var(--text);
}

.subnav__item--active {
  background: var(--accent-soft);
  color: var(--accent);
  font-weight: 600;
}
</style>
