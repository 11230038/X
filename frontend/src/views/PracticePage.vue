<script setup lang="ts">
/**
 * 练习的独立页面外壳。
 *
 * 页面本体 `learning/Practice.vue` 没有自己的骨架，原来由 `LearningLayout`
 * 提供；练习归到题库下之后不再挂在学习下，这一层只补回 `AppShell` 与页面
 * 留白，内容仍然复用同一个组件。
 *
 * 练习统计放进 `AppShell` 的第四列（右侧面板），与对话页、知识库页同一套
 * 四段式骨架；主列只放复习队列。
 *
 * 权限门包住整个骨架而不是只包队列：统计面板也是练习面的一部分，没有这个
 * 面的人不该看到面板，也不该让它去打 `/practice/stats`。这一层与对话页
 * `ChatWorkspace` 同口径 —— 门在外、骨架在内。
 */

import SurfaceGate from '../components/common/SurfaceGate.vue'
import AppShell from '../components/shell/AppShell.vue'
import Practice from './learning/Practice.vue'
import PracticeStatsPanel from './learning/practice/PracticeStatsPanel.vue'
</script>

<template>
  <SurfaceGate surface="practice">
    <AppShell>
      <div class="page">
        <div class="page__inner">
          <Practice />
        </div>
      </div>

      <template #panel="{ collapsed, toggle }">
        <PracticeStatsPanel :collapsed="collapsed" @toggle="toggle" />
      </template>
    </AppShell>
  </SurfaceGate>
</template>
