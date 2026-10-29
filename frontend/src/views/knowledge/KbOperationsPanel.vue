<script setup lang="ts">
/**
 * 右侧「知识库操作」。演示稿把这一页的说明放在 `.panel` 里（`.panel-title`
 * 加一段话），动作就跟着收进来：
 *
 * - 目录页：只有「新建知识库」——还没有哪个库可以被操作；
 * - 详情页：加入文档 / 设为默认 / 删除知识库。
 *
 * 收合之后留一条 40px 的竖条，标题竖着写，和对话页的 `SidePanel` 一致。
 */

import { KNOWLEDGE } from '../../copy/knowledge'
import AppIcon from '../../components/common/AppIcon.vue'

defineProps<{
  collapsed: boolean
  mode: 'directory' | 'detail'
  /** 详情还没读到时是 `null`：不急着把「设为默认」摆出来，免得默认库上闪一下。 */
  isDefault: boolean | null
  busy: boolean
}>()

const emit = defineEmits<{
  toggle: []
  create: []
  add: []
  setDefault: []
  remove: []
}>()
</script>

<template>
  <button
    v-if="collapsed"
    type="button"
    class="rail"
    aria-label="展开知识库操作"
    @click="emit('toggle')"
  >
    <span class="rail__icon" aria-hidden="true"><AppIcon name="settings" /></span>
    <span class="rail__text">{{ KNOWLEDGE.operations }}</span>
  </button>

  <aside v-else class="ops" aria-label="知识库操作">
    <header class="ops__head">
      <h2 class="ops__title">
        <AppIcon name="settings" />
        {{ KNOWLEDGE.operations }}
      </h2>
      <button type="button" class="ops__collapse" aria-label="收合右侧面板" @click="emit('toggle')">
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M10 6l6 6-6 6" /></svg>
      </button>
    </header>

    <div class="ops__body">
      <template v-if="mode === 'directory'">
        <p class="ops__hint">{{ KNOWLEDGE.operationsDirectoryHint }}</p>
        <button type="button" class="btn btn-primary ops__action" @click="emit('create')">
          {{ KNOWLEDGE.create }}
        </button>
      </template>

      <template v-else>
        <p class="ops__hint">{{ KNOWLEDGE.operationsHint }}</p>
        <button
          type="button"
          class="btn btn-outline ops__action"
          :disabled="busy"
          @click="emit('add')"
        >
          {{ KNOWLEDGE.addDocument }}
        </button>
        <button
          v-if="isDefault === false"
          type="button"
          class="btn btn-outline ops__action"
          :disabled="busy"
          @click="emit('setDefault')"
        >
          {{ KNOWLEDGE.setDefault }}
        </button>
        <button
          type="button"
          class="btn ops__action ops__action--danger"
          :disabled="busy"
          @click="emit('remove')"
        >
          {{ KNOWLEDGE.delete }}
        </button>
      </template>
    </div>
  </aside>
</template>

<style scoped>
.ops {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
  border: 1px solid var(--glass-border);
  border-radius: var(--radius-card);
  background: var(--glass-bg);
  box-shadow: var(--glass-shadow), var(--inset-light);
  backdrop-filter: blur(24px);
  -webkit-backdrop-filter: blur(24px);
}

.ops__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 20px 14px 12px 20px;
  border-bottom: 1px solid rgb(28 28 30 / 5%);
}

.ops__title {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0;
  color: var(--text);
  font-size: 18px;
  font-weight: 800;
}

.ops__collapse {
  display: grid;
  place-items: center;
  padding: 5px;
  border: 0;
  border-radius: var(--radius-sm);
  background: none;
  color: var(--text-faint);
  cursor: pointer;
}

.ops__collapse:hover {
  background: var(--surface-sunken);
  color: var(--text);
}

.ops__collapse svg {
  width: 16px;
  height: 16px;
  fill: none;
  stroke: currentcolor;
  stroke-width: 1.8;
  stroke-linecap: round;
  stroke-linejoin: round;
}

.ops__body {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 16px;
  padding: 16px 20px;
  overflow-y: auto;
}

/* 与对话页面板同口径：标题 18px/800，正文与提示 15px。 */
.ops__hint {
  margin: 0 0 6px;
  color: var(--text-muted);
  font-size: 15px;
  line-height: 1.75;
}

.ops__action {
  width: 100%;
  padding: 12px 20px;
  font-size: 15px;
}

.ops__action:disabled {
  color: var(--text-faint);
  cursor: default;
}

/* 演示稿的删除按钮：红字、浅红底、红边。 */
.ops__action--danger {
  border: 1px solid rgb(192 57 43 / 30%);
  background: var(--danger-soft);
  color: var(--danger);
}

.ops__action--danger:hover:not(:disabled) {
  border-color: var(--danger);
}

.rail {
  display: flex;
  flex-direction: column;
  gap: 10px;
  align-items: center;
  width: 100%;
  height: 100%;
  padding: 18px 0;
  border: 1px solid var(--glass-border);
  border-radius: var(--radius-card);
  background: var(--glass-bg);
  box-shadow: var(--glass-shadow);
  backdrop-filter: blur(24px);
  -webkit-backdrop-filter: blur(24px);
  cursor: pointer;
}

.rail:hover {
  background: var(--glass-bg-strong);
}

.rail__icon {
  font-size: 16px;
  line-height: 1;
}

.rail__text {
  color: var(--text-muted);
  font-size: 12px;
  letter-spacing: 0.08em;
  writing-mode: vertical-rl;
}
</style>
