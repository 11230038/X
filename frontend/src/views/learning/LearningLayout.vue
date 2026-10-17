<script setup lang="ts">
/**
 * 学习分组的外壳：一条页内导航 + 子页面。
 *
 * 权限不在这层判 —— 七个子页落在六个面上（dashboard / practice / mastery /
 * reading / watching / book），学习地图复用 mastery 面，由子页自己的
 * `SurfaceGate` 处理。
 */

import { RouterView } from 'vue-router'

import AppShell from '../../components/shell/AppShell.vue'
import SubNav from '../../components/shell/SubNav.vue'

const items = [
  { label: '首页', to: '/learning' },
  { label: '练习', to: '/learning/practice' },
  { label: '掌握路径', to: '/learning/mastery' },
  { label: '学习地图', to: '/learning/map' },
  { label: '沉浸阅读', to: '/learning/reading' },
  { label: '视频学习', to: '/learning/watching' },
  { label: '活书', to: '/learning/books' },
] as const
</script>

<template>
  <AppShell>
    <div class="page">
      <div class="page__inner">
        <SubNav :items="items" />
        <RouterView />
      </div>
    </div>
  </AppShell>
</template>
