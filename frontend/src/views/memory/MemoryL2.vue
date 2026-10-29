<script setup lang="ts">
/**
 * L2 事实。
 *
 * 分页是 `before` 指针，取值是**记录时间**（`created_at`），而排序用的是最近
 * 变更。两者不一致时，一条刚被编辑过的旧事实可能重复出现 —— 页面把这一点写
 * 在游标旁边，而不是假装页码是稳定的。
 *
 * 撤回保留行、只改状态；永久删除只能去「去重与清理」。
 */

import { onMounted, ref, watch } from 'vue'

import type { ApiFailure } from '../../api/client'
import {
  listFacts,
  restoreFact,
  retractFact,
  stateFact,
  type FactStatus,
  type FactView,
  type MemorySurface,
} from '../../api/memory'
import FactCard from '../../components/memory/FactCard.vue'
import FactDetailDialog from '../../components/memory/FactDetailDialog.vue'
import EmptyState from '../../components/common/EmptyState.vue'
import FailureState from '../../components/common/FailureState.vue'
import LoadMore from '../../components/common/LoadMore.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import { MEMORY } from '../../copy/memory'
import { confirmAction } from '../../shared/confirm'
import { toFailure } from '../../shared/resource'
import { notify } from '../../shared/toast'

const statusOptions: FactStatus[] = ['active', 'retracted', 'superseded']
const surfaces: MemorySurface[] = ['mastery', 'reading', 'practice', 'user']
const limits = [30, 50]

const facts = ref<FactView[]>([])
const status = ref<'loading' | 'ready' | 'failed'>('loading')
const failure = ref<ApiFailure | null>(null)
const moreLoading = ref(false)
const moreFailure = ref<ApiFailure | null>(null)
const loadedAll = ref(false)

const selectedStatuses = ref<FactStatus[]>(['active'])
const surface = ref<'' | MemorySurface>('')
const limit = ref(30)

const openFactId = ref('')
const startEditing = ref(false)
const busyId = ref('')

const addDialog = ref<HTMLDialogElement | null>(null)
const addStatement = ref('')
const addScope = ref('')
const addSaving = ref(false)
const addFailure = ref<ApiFailure | null>(null)

function queryOptions(before?: string): Parameters<typeof listFacts>[0] {
  return {
    limit: limit.value,
    ...(selectedStatuses.value.length > 0 ? { status: [...selectedStatuses.value] } : {}),
    ...(surface.value ? { surface: surface.value } : {}),
    ...(before ? { before } : {}),
  }
}

async function load(): Promise<void> {
  status.value = 'loading'
  failure.value = null
  moreFailure.value = null
  loadedAll.value = false
  try {
    const page = await listFacts(queryOptions())
    facts.value = page
    loadedAll.value = page.length < limit.value
    status.value = 'ready'
  } catch (cause) {
    failure.value = toFailure(cause)
    status.value = 'failed'
  }
}

async function loadMore(): Promise<void> {
  const last = facts.value[facts.value.length - 1]
  if (!last || moreLoading.value) return
  moreLoading.value = true
  moreFailure.value = null
  try {
    const page = await listFacts(queryOptions(last.created_at))
    const known = new Set(facts.value.map((fact) => fact.fact_id))
    facts.value = [...facts.value, ...page.filter((fact) => !known.has(fact.fact_id))]
    loadedAll.value = page.length < limit.value
  } catch (cause) {
    moreFailure.value = toFailure(cause)
  } finally {
    moreLoading.value = false
  }
}

function toggleStatus(value: FactStatus): void {
  selectedStatuses.value = selectedStatuses.value.includes(value)
    ? selectedStatuses.value.filter((item) => item !== value)
    : [...selectedStatuses.value, value]
}

function statusLabel(value: string): string {
  return value === 'retracted'
    ? MEMORY.statusRetracted
    : value === 'superseded'
      ? MEMORY.statusSuperseded
      : MEMORY.statusActive
}

function surfaceLabel(value: string): string {
  return MEMORY.surfaces[value] ?? value
}

function openDetail(factId: string, editing = false): void {
  openFactId.value = factId
  startEditing.value = editing
}

async function retract(fact: FactView): Promise<void> {
  const confirmed = await confirmAction({
    title: MEMORY.retractConfirmTitle,
    body: MEMORY.retractConfirmBody,
    note: MEMORY.retractConfirmNote,
    confirmLabel: MEMORY.retract,
    danger: true,
  })
  if (!confirmed) return
  busyId.value = fact.fact_id
  try {
    await retractFact(fact.fact_id)
    await load()
  } catch (cause) {
    notify(`${MEMORY.actionFailed}：${toFailure(cause).message}`, 'error')
  } finally {
    busyId.value = ''
  }
}

async function restore(fact: FactView): Promise<void> {
  const confirmed = await confirmAction({
    title: MEMORY.restoreConfirmTitle,
    body: MEMORY.restoreConfirmBody,
    note: MEMORY.restoreConfirmNote,
    confirmLabel: MEMORY.restore,
  })
  if (!confirmed) return
  busyId.value = fact.fact_id
  try {
    await restoreFact(fact.fact_id)
    await load()
  } catch (cause) {
    notify(`${MEMORY.actionFailed}：${toFailure(cause).message}`, 'error')
  } finally {
    busyId.value = ''
  }
}

function openAdd(): void {
  addStatement.value = ''
  addScope.value = ''
  addFailure.value = null
  if (!addDialog.value?.open) addDialog.value?.showModal()
}

function closeAdd(): void {
  if (addDialog.value?.open) addDialog.value.close()
}

async function saveAdd(): Promise<void> {
  const statement = addStatement.value.trim()
  if (statement === '') return
  addSaving.value = true
  addFailure.value = null
  try {
    await stateFact({
      statement,
      ...(addScope.value.trim() ? { scope: addScope.value.trim() } : {}),
    })
    closeAdd()
    notify(MEMORY.l2Added, 'success')
    await load()
  } catch (cause) {
    addFailure.value = toFailure(cause)
  } finally {
    addSaving.value = false
  }
}

watch([selectedStatuses, surface, limit], () => {
  void load()
})

onMounted(() => {
  void load()
})
</script>

<template>
  <div class="l2">
    <header class="head">
      <div>
        <h2 class="head__title">{{ MEMORY.l2Title }}</h2>
        <p class="head__description">{{ MEMORY.l2Description }}</p>
      </div>
      <button type="button" class="primary" @click="openAdd">{{ MEMORY.l2Add }}</button>
    </header>

    <div class="filters">
      <fieldset class="statuses">
        <legend class="field__label">{{ MEMORY.status }}</legend>
        <label v-for="option in statusOptions" :key="option" class="status">
          <input
            type="checkbox"
            :checked="selectedStatuses.includes(option)"
            @change="toggleStatus(option)"
          />
          {{ statusLabel(option) }}
        </label>
      </fieldset>

      <label class="field">
        <span class="field__label">{{ MEMORY.surface }}</span>
        <select v-model="surface" class="field__input">
          <option value="">{{ MEMORY.allSurfaces }}</option>
          <option v-for="value in surfaces" :key="value" :value="value">
            {{ surfaceLabel(value) }}
          </option>
        </select>
      </label>

      <label class="field">
        <span class="field__label">{{ MEMORY.l2Limit }}</span>
        <select v-model.number="limit" class="field__input">
          <option v-for="count in limits" :key="count" :value="count">{{ count }}</option>
        </select>
      </label>
    </div>

    <SkeletonBlock v-if="status === 'loading'" :lines="7" :line-height="44" />

    <FailureState
      v-else-if="status === 'failed' && failure"
      :failure="failure"
      :title="MEMORY.l2Failed"
      @retry="load"
    />

    <EmptyState
      v-else-if="facts.length === 0"
      :title="MEMORY.l2Empty"
      :hint="MEMORY.l2EmptyHint"
      :action-label="MEMORY.goChat"
      @action="$router.push({ name: 'chat' })"
    />

    <template v-else>
      <p class="note">
        {{ MEMORY.l2TotalShown(facts.length) }} · {{ MEMORY.l2CursorHint }}
      </p>

      <div class="facts">
        <FactCard
          v-for="fact in facts"
          :key="fact.fact_id"
          :fact="fact"
          :busy="busyId === fact.fact_id"
          @open="openDetail(fact.fact_id)"
          @edit="openDetail(fact.fact_id, true)"
          @retract="retract(fact)"
          @restore="restore(fact)"
        />
      </div>

      <LoadMore
        v-if="!loadedAll"
        :loading="moreLoading"
        :failure="moreFailure"
        :label="MEMORY.l2LoadMore"
        :loading-label="MEMORY.l2LoadingMore"
        @load="loadMore"
        @retry="loadMore"
      />
      <p v-else class="note">{{ MEMORY.l2LoadedAll }}</p>
    </template>

    <FactDetailDialog
      v-if="openFactId"
      :fact-id="openFactId"
      :start-editing="startEditing"
      @close="openFactId = ''"
      @changed="load"
    />

    <!-- 写一条事实 -->
    <dialog ref="addDialog" class="dialog" @cancel.prevent="closeAdd" @close="closeAdd">
      <h2 class="dialog__title">{{ MEMORY.l2AddTitle }}</h2>
      <label class="field field--block">
        <span class="field__label">{{ MEMORY.l2AddStatement }}</span>
        <textarea v-model="addStatement" class="field__area" rows="4"></textarea>
      </label>
      <label class="field field--block">
        <span class="field__label">{{ MEMORY.l2AddScope }}</span>
        <input v-model="addScope" class="field__input" type="text" />
      </label>
      <p v-if="addStatement.trim() === ''" class="field__hint">{{ MEMORY.l2AddEmpty }}</p>
      <p v-if="addFailure" class="field__error">
        {{ MEMORY.l2AddFailed }}：{{ addFailure.message }}
      </p>
      <div class="dialog__actions">
        <button type="button" class="ghost" @click="closeAdd">{{ MEMORY.cancel }}</button>
        <button
          type="button"
          class="primary"
          :disabled="addSaving || addStatement.trim() === ''"
          @click="saveAdd"
        >
          {{ addSaving ? MEMORY.l2AddSaving : MEMORY.l2AddSave }}
        </button>
      </div>
    </dialog>
  </div>
</template>

<style scoped>
.l2 {
  display: flex;
  flex-direction: column;
  gap: 18px;
}

.head {
  display: flex;
  flex-wrap: wrap;
  gap: 14px;
  align-items: flex-start;
  justify-content: space-between;
}

.head__title {
  margin: 0;
  font-size: 15px;
  font-weight: 650;
}

.head__description {
  margin: 4px 0 0;
  color: var(--text-muted);
  font-size: 14px;
  line-height: 1.6;
}

.filters {
  display: flex;
  flex-wrap: wrap;
  gap: 18px;
  align-items: flex-end;
  padding: 16px 18px;
  border: 1px solid var(--glass-border);
  border-radius: var(--radius);
  background: var(--glass-bg);
  box-shadow: var(--glass-shadow), var(--inset-light);
  backdrop-filter: blur(20px);
  -webkit-backdrop-filter: blur(20px);
}

.statuses {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin: 0;
  padding: 0;
  border: 0;
}

.statuses legend {
  margin-bottom: 4px;
}

.status {
  display: inline-flex;
  gap: 4px;
  align-items: center;
  color: var(--text-muted);
  font-size: 14px;
  cursor: pointer;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.field--block {
  margin-bottom: 9px;
}

.field__label {
  color: var(--text-faint);
  font-size: 14px;
}

.field__input,
.field__area {
  padding: 6px 10px;
  border: 1px solid rgb(0 0 0 / 8%);
  border-radius: 10px;
  background: rgb(255 255 255 / 80%);
  font-size: 14px;
  outline: none;
  transition: border-color 0.2s ease, box-shadow 0.2s ease;
}

.field__input:focus,
.field__area:focus {
  border-color: var(--color-purple);
  background: #fff;
  box-shadow: 0 0 0 4px rgb(177 140 255 / 15%);
}

.field__area {
  width: 100%;
  line-height: 1.6;
  resize: vertical;
}

.field__hint {
  margin: 0;
  color: var(--text-faint);
  font-size: 14px;
}

.field__error {
  margin: 0 0 8px;
  color: var(--danger);
  font-size: 14px;
}

.note {
  margin: 0;
  color: var(--text-faint);
  font-size: 14px;
  line-height: 1.6;
}

.facts {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.dialog {
  width: min(520px, calc(100vw - 32px));
  padding: 24px;
  border: 1px solid var(--glass-border);
  border-radius: var(--radius-card);
  background: rgb(255 255 255 / 92%);
  color: var(--text);
  box-shadow: 0 24px 64px -12px rgb(51 68 85 / 20%);
  backdrop-filter: blur(30px);
  -webkit-backdrop-filter: blur(30px);
}

.dialog::backdrop {
  background: rgb(20 26 38 / 25%);
  backdrop-filter: blur(6px);
}

.dialog__title {
  margin: 0 0 10px;
  font-size: 15px;
  font-weight: 650;
}

.dialog__actions {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
  margin-top: 12px;
}

.ghost {
  padding: 5px 14px;
  border: 1px solid rgb(0 0 0 / 8%);
  border-radius: 999px;
  background: rgb(255 255 255 / 78%);
  color: var(--text-muted);
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.2s ease;
}

.ghost:hover:not(:disabled) {
  border-color: var(--accent);
  background: #fff;
  color: var(--text);
}

.primary {
  padding: 6px 16px;
  border: 1px solid transparent;
  border-radius: 999px;
  background: var(--accent-gradient);
  color: #fff;
  font-size: 14px;
  font-weight: 700;
  cursor: pointer;
  box-shadow: 0 4px 12px rgb(177 140 255 / 30%);
  transition: transform 0.2s ease, box-shadow 0.2s ease;
}

.primary:hover:not(:disabled) {
  transform: translateY(-1px);
  box-shadow: 0 8px 20px rgb(177 140 255 / 40%);
}

.primary:disabled {
  cursor: default;
  opacity: 0.55;
}
</style>
