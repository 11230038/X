<script setup lang="ts">
/**
 * 根组件只做两件事：把当前路由对应的页面挂上去，以及挂载全局单例的
 * 确认框与轻提示。
 *
 * 登录态**不在这里**恢复 —— 见 `main.ts`：它必须在路由守卫第一次跑之前就绪，
 * 而组件 setup 与首次导航谁先谁后是路由内部的事，不该由这里去赌。
 */

import ConfirmDialog from './components/common/ConfirmDialog.vue'
import ToastHost from './components/common/ToastHost.vue'
import { startPresenceHeartbeat } from './presence'

// 在线时长的心跳挂在根上：它跟着登录态起停，和当前是哪一页无关 —— 时长是
// "这个人今天在不在"，不是"这个人开了学习首页"。
startPresenceHeartbeat()
</script>

<template>
  <RouterView />
  <ConfirmDialog />
  <ToastHost />
</template>
