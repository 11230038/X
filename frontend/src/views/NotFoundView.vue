<script setup lang="ts">
/** 404。不再静默重定向：给两个真出口，让用户自己选。 */

import AppShell from '../components/shell/AppShell.vue'
</script>

<template>
  <AppShell>
    <div class="page">
      <div class="page__inner">
        <div class="notfound">
          <p class="notfound__code">404</p>
          <h1 class="notfound__title">这个地址没有对应页面</h1>
          <p class="notfound__hint">可能是链接写错了，或者这一页已经不在。</p>
          <div class="notfound__links">
            <RouterLink class="notfound__link" to="/chat">去对话</RouterLink>
            <RouterLink class="notfound__link" to="/learning">去学习首页</RouterLink>
          </div>
        </div>
      </div>
    </div>
  </AppShell>
</template>

<style scoped>
.notfound {
  display: flex;
  flex-direction: column;
  gap: 8px;
  align-items: flex-start;
  padding: 32px 0;
}

.notfound__code {
  color: var(--text-faint);
  font-family: var(--font-mono);
  font-size: 15px;
}

.notfound__title {
  margin: 0;
  font-size: 20px;
}

.notfound__hint {
  color: var(--text-muted);
  font-size: 15px;
}

.notfound__links {
  display: flex;
  gap: 8px;
  margin-top: 12px;
}

.notfound__link {
  padding: 7px 16px;
  border: 1px solid rgb(0 0 0 / 8%);
  border-radius: 999px;
  background: rgb(255 255 255 / 78%);
  font-size: 14px;
  font-weight: 600;
  text-decoration: none;
  transition: all 0.2s ease;
}

.notfound__link:hover {
  border-color: var(--accent);
  background: #fff;
}
</style>
