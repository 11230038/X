<script setup lang="ts">
/**
 * 工作区：导出与导入各占一半，各自管理自己的请求与失败。
 */

import WorkspaceExport from './workspace/WorkspaceExport.vue'
import WorkspaceImport from './workspace/WorkspaceImport.vue'
</script>

<template>
  <div class="workspace">
    <WorkspaceExport />
    <WorkspaceImport />
  </div>
</template>

<style scoped src="./SettingsWorkspace.css"></style>
