<script setup lang="ts">
/**
 * 设置分组。
 *
 * 分区只列**有后端**的那几个。权限明确为 false 的分区整条隐藏（`设计说明书`
 * §7.2：没有入口的能力不画假控件）；权限未知时照常显示 —— 子页自己会处理。
 * 这些分区彼此属于不同的面（system / knowledge_base / guardian），所以权限门
 * 在子页，不在这层。
 */

import { computed } from 'vue'
import { RouterView } from 'vue-router'

import { canSurface, surfaceDecision } from '../../auth/permissions'
import AppShell from '../../components/shell/AppShell.vue'
import PageHeader from '../../components/shell/PageHeader.vue'
import SubNav from '../../components/shell/SubNav.vue'
import { LAYER_LABELS, REASON_LABELS, SURFACE_LABELS } from '../../copy/permissions'
import { SETTINGS } from '../../copy/settings'

const allItems = [
  { label: '账号', to: '/settings/account' },
  { label: '工作区', to: '/settings/workspace', surface: 'system' },
  { label: '用量统计', to: '/settings/usage', surface: 'system' },
  { label: '运行状态', to: '/settings/system', surface: 'system' },
  { label: '默认知识库', to: '/settings/knowledge-bases', surface: 'knowledge_base' },
  { label: '监护', to: '/settings/guardian', surface: 'guardian' },
  { label: '关于', to: '/settings/about' },
] as const

const items = computed(() =>
  allItems
    .filter((item) => !('surface' in item) || canSurface(item.surface) !== false)
    .map((item) => ({ label: item.label, to: item.to })),
)

/**
 * 被权限藏起来的分区。页面**不画假控件**，但要说清是哪一层拒的 ——
 * "预设没开放"和"监护人关掉"是两件不同的事，哪怕在界面上都是"看不到"。
 */
const hidden = computed(() =>
  allItems
    .filter((item) => 'surface' in item && canSurface(item.surface) === false)
    .map((item) => {
      const decision = 'surface' in item ? surfaceDecision(item.surface) : null
      return {
        label: item.label,
        surface: 'surface' in item ? (SURFACE_LABELS[item.surface] ?? item.surface) : '',
        layer: decision ? (LAYER_LABELS[decision.layer] ?? decision.layer) : '',
        reason: decision ? (REASON_LABELS[decision.reason] ?? decision.reason) : '',
      }
    }),
)
</script>

<template>
  <AppShell>
    <div class="page">
      <div class="page__inner">
        <PageHeader
          title="设置"
          description="这里只列当前账号真的有后端接口的设置项。看不到的分区，要么没有接口，要么权限不允许。"
        />
        <SubNav :items="items" />
        <details v-if="hidden.length > 0" class="why">
          <summary class="why__summary">{{ SETTINGS.whyHidden }}</summary>
          <ul class="why__list">
            <li v-for="item in hidden" :key="item.label" class="why__item">
              <strong>{{ item.label }}</strong>
              <span v-if="item.surface">（面：{{ item.surface }}）</span>
              <span v-if="item.layer">被「{{ item.layer }}」拒绝</span>
              <span v-if="item.reason">：{{ item.reason }}</span>
            </li>
          </ul>
        </details>
        <RouterView />
      </div>
    </div>
  </AppShell>
</template>

<style scoped>
.why {
  margin: 10px 0 2px;
  padding: 8px 11px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface-sunken);
}

.why__summary {
  color: var(--text-muted);
  font-size: 11px;
  cursor: pointer;
}

.why__list {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin: 7px 0 0;
  padding-left: 18px;
  color: var(--text-muted);
  font-size: 11px;
  line-height: 1.6;
}

.why__item strong {
  color: var(--text);
  font-weight: 600;
}
</style>
