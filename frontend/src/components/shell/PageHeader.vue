<script setup lang="ts">
/** 内容页的标题区。动作放在右侧，说明最多两行。 */

withDefaults(
  defineProps<{
    title: string
    description?: string
    /** 标题用衬线体。演示稿的错题本页是唯一这样写的一页，题库照它。 */
    serif?: boolean
  }>(),
  { description: '', serif: false },
)
</script>

<template>
  <header class="page-header">
    <div class="page-header__text">
      <h1 class="page-header__title" :class="{ 'page-header__title--serif': serif }">
        {{ title }}
      </h1>
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
  align-items: flex-end;
  justify-content: space-between;
  gap: 20px;
  padding: 4px 0 6px;
}

.page-header__text {
  min-width: 0;
}

.page-header__title {
  margin: 0;
  color: var(--text);
  font-size: 32px;
  font-weight: 800;
  line-height: 1.2;
  letter-spacing: -0.035em;
}

.page-header__title--serif {
  font-family: var(--font-serif);
}

/* 标题与说明之间的间距、说明自身的行距，统一照知识库页：16px + 1.7。
   各页不要再在 scoped 样式里单独覆盖这两个值。 */
.page-header__description {
  max-width: 62em;
  margin: 16px 0 0;
  color: var(--text-faint);
  font-size: 16px;
  font-weight: 500;
  line-height: 1.7;
}

.page-header__actions {
  display: flex;
  flex: none;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  padding-bottom: 4px;
}
</style>
