<script setup lang="ts">
/**
 * 240px 二级侧栏，照演示稿的 `.sidebar`：一张玻璃卡，顶部一行标题，下面一组
 * 带图标的条目，当前项是白底。
 *
 * 条目来自 `sections.ts`，按当前路由换。没有分组的页面（会话页、知识库页）
 * 由页面自己的列表占这一列，这里返回 null，`AppShell` 就不会画这一栏。
 *
 * 窗口窄于设计基准 1440 时收成 80px 的图标栏：标题不画、条目只剩图标，样子
 * 与左边的主图标栏一致（60×60、圆角 14、悬停上浮），名字放在 `title` 与
 * `aria-label` 上，悬停仍然读得到。**只收这一栏**，页面自己的列表不收。
 */

import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'

import AppIcon from '../common/AppIcon.vue'
import { sectionFor } from './sections'

const route = useRoute()
const section = computed(() => sectionFor(route))
</script>

<template>
  <aside v-if="section" class="sidebar">
    <div class="sidebar__header">{{ section.title }}</div>
    <nav class="sidebar__list" :aria-label="section.title">
      <RouterLink
        v-for="item in section.items"
        :key="item.to"
        class="sidebar__item"
        :class="{ 'sidebar__item--active': item.isActive(route) }"
        :to="item.to"
        :title="item.label"
        :aria-label="item.label"
      >
        <span class="sidebar__icon"><AppIcon :name="item.icon" /></span>
        <span class="sidebar__label">{{ item.label }}</span>
      </RouterLink>
    </nav>
  </aside>
</template>

<style scoped>
.sidebar {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-width: 0;
  padding: 28px 24px;
  overflow: hidden;
  border: 1px solid var(--glass-border);
  border-radius: var(--radius-card);
  background: var(--glass-bg);
  box-shadow: var(--glass-shadow), var(--inset-light);
  backdrop-filter: blur(24px);
  -webkit-backdrop-filter: blur(24px);
}

/* 字号跟对话页的历史会话栏对齐：标题 21/800，条目 16/500（选中 750）。 */
.sidebar__header {
  margin-bottom: 24px;
  color: var(--text);
  font-size: 21px;
  font-weight: 800;
}

.sidebar__list {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 8px;
  min-height: 0;
  overflow-y: auto;
}

.sidebar__item {
  display: flex;
  gap: 16px;
  align-items: center;
  padding: 12px 16px;
  border-radius: var(--radius-sm);
  color: var(--text-muted);
  font-size: 16px;
  font-weight: 500;
  text-decoration: none;
  transition: all 0.3s cubic-bezier(0.25, 0.8, 0.25, 1);
}

.sidebar__item:hover {
  background: rgb(255 255 255 / 60%);
  color: var(--text);
}

.sidebar__item--active {
  background: #fff;
  color: var(--text);
  font-weight: 750;
  box-shadow: 0 2px 8px rgb(51 68 85 / 6%);
}

/* 图标与一级图标栏同一套（`AppIcon`）：展开时 20px，和 16px 的条目字同高；
   收成图标栏时 30px，两列并排是同一种样子。 */
.sidebar__icon {
  display: flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 22px;
  --icon-size: 20px;
}

/*
 * 窄于设计基准时的图标栏。尺寸照 `PrimaryNav` 的 `.nav__item`：60×60、圆角 14、
 * 30px 的图标、悬停同样上浮 —— 两列并排看是同一套。
 */
@media (max-width: 1439.98px) {
  .sidebar {
    align-items: center;
    padding: 24px 0;
  }

  /* 标题是给整列起名的，收成图标栏之后没有地方放它（主图标栏也没有）。 */
  .sidebar__header {
    display: none;
  }

  .sidebar__list {
    align-items: center;
    gap: 16px;
    width: 100%;
  }

  .sidebar__item {
    justify-content: center;
    width: 60px;
    height: 60px;
    padding: 0;
    border-radius: 14px;
    transition: all 0.4s cubic-bezier(0.34, 1.56, 0.64, 1);
  }

  .sidebar__item:hover {
    transform: translateY(-2px) scale(1.05);
  }

  .sidebar__label {
    display: none;
  }

  .sidebar__icon {
    width: auto;
    --icon-size: 30px;
  }
}
</style>
