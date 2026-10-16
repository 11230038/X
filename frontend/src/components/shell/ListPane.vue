<script setup lang="ts">
/**
 * 列表栏。`设计说明书.md` §3.2：会话列表、知识库列表、题库列表**共用同一套
 * 形态**，做成一个可复用组件 —— 标题行 + 搜索 + 新建按钮 + 分行列表。
 *
 * stage 0 只被会话列表用上，但分界线是按文档划的：这一层管**形态**（高度、
 * 边框、空态、收合），不管**内容**（一行是什么、悬停出哪些操作）。所以行是
 * 插槽，不是 props —— 会话行上那些操作（重命名、软删除、回收站、分叉）与
 * 知识库行上的完全不是一回事，一个 props 接口会立刻变成两种形态的并集。
 */

defineProps<{
  title: string
  /** 交给搜索框。由使用方持有，因为过滤在它那边。 */
  query: string
  /** 空态时给的**动作**，不是一段解释（§7.1 第 2 条）。 */
  emptyAction?: string
  loading?: boolean
  empty?: boolean
}>()

const emit = defineEmits<{
  (event: 'update:query', value: string): void
  (event: 'empty-action'): void
}>()
</script>

<template>
  <aside class="list">
    <header class="list__head">
      <h2 class="list__title">{{ title }}</h2>
      <div class="list__actions">
        <slot name="actions" />
      </div>
    </header>

    <div class="list__search">
      <input
        class="list__input"
        type="search"
        :value="query"
        :aria-label="`在${title}里搜索`"
        @input="emit('update:query', ($event.target as HTMLInputElement).value)"
      />
    </div>

    <div class="list__body">
      <!--
        加载态与空态是两种东西，且§6.2 要求它们**不能长得像**：加载中是骨架
        屏（不确定进度），空态是一个可点的动作。
      -->
      <div v-if="loading" class="list__skeleton" aria-hidden="true">
        <span v-for="n in 5" :key="n" class="list__skeleton-row" />
      </div>

      <div v-else-if="empty" class="list__empty">
        <button v-if="emptyAction" type="button" class="list__empty-action" @click="emit('empty-action')">
          {{ emptyAction }}
        </button>
      </div>

      <ul v-else class="list__rows">
        <slot />
      </ul>
    </div>
  </aside>
</template>

<style scoped>
.list {
  display: flex;
  flex-direction: column;
  height: 100%;
  border-right: 1px solid var(--border);
  background: var(--surface);
}

.list__head {
  display: flex;
  gap: 8px;
  align-items: center;
  justify-content: space-between;
  padding: 14px 12px 8px;
}

.list__title {
  font-size: 15px;
  font-weight: 650;
}

.list__actions {
  display: flex;
  gap: 4px;
  align-items: center;
}

.list__search {
  padding: 0 12px 8px;
}

.list__input {
  width: 100%;
  padding: 6px 9px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface-sunken);
  font-size: 13px;
}

.list__input:focus {
  border-color: var(--accent);
  background: var(--surface);
}

.list__body {
  flex: 1;
  overflow-y: auto;
  padding: 0 6px 12px;
}

.list__rows {
  margin: 0;
  padding: 0;
  list-style: none;
}

.list__skeleton {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 6px;
}

.list__skeleton-row {
  height: 34px;
  border-radius: var(--radius-sm);
  background: linear-gradient(90deg, var(--surface-sunken), #eceef2, var(--surface-sunken));
  background-size: 200% 100%;
  animation: list-sheen 1.4s ease-in-out infinite;
}

@keyframes list-sheen {
  from {
    background-position: 200% 0;
  }

  to {
    background-position: -200% 0;
  }
}

.list__empty {
  display: grid;
  place-items: center;
  padding: 40px 12px;
}

.list__empty-action {
  padding: 10px 16px;
  border: 1px dashed var(--border-strong);
  border-radius: var(--radius);
  background: var(--surface-sunken);
  color: var(--accent);
  font-weight: 600;
  cursor: pointer;
}

.list__empty-action:hover {
  border-color: var(--accent);
}

/* 前庭功能敏感的用户会被无限循环的骨架屏伤到（§7.4）。 */
@media (prefers-reduced-motion: reduce) {
  .list__skeleton-row {
    animation: none;
  }
}
</style>
