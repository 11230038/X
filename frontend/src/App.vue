<script setup lang="ts">
/**
 * 根组件只做一件事：把当前路由对应的页面挂上去。
 *
 * 登录态**不在这里**恢复 —— 见 `main.ts`：它必须在路由守卫第一次跑之前就绪，
 * 而组件 setup 与首次导航谁先谁后是路由内部的事，不该由这里去赌。
 */
</script>

<template>
  <RouterView />
</template>
