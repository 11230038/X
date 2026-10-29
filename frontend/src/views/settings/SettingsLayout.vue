<script setup lang="ts">
/**
 * 设置分组。
 *
 * 分区只列**有后端**的那几个。权限明确为 false 的分区整条隐藏（`设计说明书`
 * §7.2：没有入口的能力不画假控件）；权限未知时照常显示 —— 子页自己会处理。
 * 这些分区彼此属于不同的面（system / knowledge_base / guardian），所以权限门
 * 在子页，不在这层。
 *
 * 清单与设置侧栏共用 `sections.ts` 的 `SETTINGS_ENTRIES`：技能、工具、记忆
 * 与伙伴也在这张表里，所以"为什么看不到"那一栏同样能解释它们。用户管理与
 * 日志统计不在设置里，它们随个人中心。
 */

import { computed } from 'vue'
import { RouterView, useRoute } from 'vue-router'

import { canSurface, surfaceDecision } from '../../auth/permissions'
import AppShell from '../../components/shell/AppShell.vue'
import PageHeader from '../../components/shell/PageHeader.vue'
import { SETTINGS_ENTRIES } from '../../components/shell/sections'
import { CATALOG } from '../../copy/catalog'
import { LAYER_LABELS, REASON_LABELS, SURFACE_LABELS } from '../../copy/permissions'
import { SETTINGS } from '../../copy/settings'

/**
 * 标题区按当前子页换。原来七个子页共用一句"设置"，进到哪一页都读不出
 * 自己叫什么；这里照个人中心的做法，一页一个标题。
 */
const HEADERS: Record<string, { title: string; description: string }> = {
  'settings-workspace': {
    title: SETTINGS.workspace,
    description: SETTINGS.workspaceDescription,
  },
  'settings-usage': { title: SETTINGS.usage, description: SETTINGS.usageDescription },
  'settings-system': { title: SETTINGS.system, description: SETTINGS.systemDescription },
  'settings-guardian': {
    title: SETTINGS.guardian,
    description: SETTINGS.guardianDescription,
  },
  'settings-about': { title: SETTINGS.about, description: SETTINGS.aboutDescription },
  'settings-skills': { title: CATALOG.skillsTitle, description: CATALOG.skillsDescription },
  'settings-tools': { title: CATALOG.toolsTitle, description: CATALOG.toolsDescription },
}

const route = useRoute()

const header = computed(() => {
  const name = typeof route.name === 'string' ? route.name : ''
  return HEADERS[name] ?? { title: SETTINGS.title, description: '' }
})

/**
 * 被权限藏起来的分区。页面**不画假控件**，但要说清是哪一层拒的 ——
 * "预设没开放"和"监护人关掉"是两件不同的事，哪怕在界面上都是"看不到"。
 */
const hidden = computed(() =>
  SETTINGS_ENTRIES.filter(
    (item) => item.surface !== undefined && canSurface(item.surface) === false,
  ).map((item) => {
    const surface = item.surface ?? ''
    const decision = surface === '' ? null : surfaceDecision(surface)
    return {
      label: item.label,
      surface: SURFACE_LABELS[surface] ?? surface,
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
        <PageHeader :title="header.title" :description="header.description" />
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
/* 被权限藏起来的分区：一条可折叠的浅卡。它是解释，不该比内容更显眼。 */
.why {
  padding: 12px 18px;
  border: 1px solid rgb(255 255 255 / 70%);
  border-radius: 14px;
  background: rgb(255 255 255 / 40%);
}

.why__summary {
  color: var(--text-muted);
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
}

.why__list {
  display: flex;
  flex-direction: column;
  gap: 5px;
  margin: 10px 0 0;
  padding-left: 18px;
  color: var(--text-muted);
  font-size: 14px;
  line-height: 1.6;
}

.why__item strong {
  color: var(--text);
  font-weight: 600;
}
</style>
