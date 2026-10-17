<script setup lang="ts">
/** 内容页的标题区。动作放在右侧，说明最多两行。 */

withDefaults(
  defineProps<{
    title: string
    description?: string
  }>(),
  { description: '' },
)
</script>

<template>
  <header class="page-header">
    <div class="page-header__text">
      <h1 class="page-header__title">{{ title }}</h1>
      <p v-if="description" class="page-header__description">{{ description }}</p>
    </div>
    <div v-if="$slots.actions" class="page-header__actions">
      <slot name="actions"></slot>
    </div>
  </header>
</template>

<style scoped>
.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  padding-bottom: 16px;
  border-bottom: 1px solid var(--border);
}

.page-header__text {
  min-width: 0;
}

.page-header__title {
  margin: 0;
  color: var(--text);
  font-size: 18px;
  font-weight: 650;
  line-height: 1.35;
}

.page-header__description {
  max-width: 60em;
  margin: 4px 0 0;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.6;
}

.page-header__actions {
  display: flex;
  flex: none;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}
</style>
