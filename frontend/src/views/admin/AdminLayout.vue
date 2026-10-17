<script setup lang="ts">
/** 管理台整组都在 `admin` 面上，权限门放在这一层。 */

import { RouterView } from 'vue-router'

import SurfaceGate from '../../components/common/SurfaceGate.vue'
import AppShell from '../../components/shell/AppShell.vue'
import PageHeader from '../../components/shell/PageHeader.vue'
import SubNav from '../../components/shell/SubNav.vue'

const items = [
  { label: '用户与授权', to: '/admin/users' },
  { label: '审计', to: '/admin/audit' },
] as const
</script>

<template>
  <AppShell>
    <SurfaceGate surface="admin">
      <div class="page">
        <div class="page__inner">
          <PageHeader title="管理台" description="用户、授权与审计。所有改动立即生效，并记入审计。" />
          <SubNav :items="items" />
          <RouterView />
        </div>
      </div>
    </SurfaceGate>
  </AppShell>
</template>
