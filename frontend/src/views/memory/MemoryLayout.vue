<script setup lang="ts">
/**
 * 记忆分组。整组都在 `memory` 这一个面上，所以权限门放在这一层，子页不再重复。
 */

import { RouterView } from 'vue-router'

import SurfaceGate from '../../components/common/SurfaceGate.vue'
import AppShell from '../../components/shell/AppShell.vue'
import PageHeader from '../../components/shell/PageHeader.vue'
import SubNav from '../../components/shell/SubNav.vue'

const items = [
  { label: '工作台', to: '/memory' },
  { label: 'L1 原始轨迹', to: '/memory/l1' },
  { label: 'L2 事实', to: '/memory/l2' },
  { label: 'L3 综合', to: '/memory/l3' },
  { label: '归属分组图', to: '/memory/graph' },
  { label: '去重与清理', to: '/memory/resolve' },
] as const
</script>

<template>
  <AppShell>
    <SurfaceGate surface="memory">
      <div class="page">
        <div class="page__inner">
          <PageHeader
            title="记忆"
            description="L1 是原始轨迹，L2 是从中提炼的事实，L3 是每次现算的综合。三层之间不互相替代。"
          />
          <SubNav :items="items" />
          <RouterView />
        </div>
      </div>
    </SurfaceGate>
  </AppShell>
</template>
