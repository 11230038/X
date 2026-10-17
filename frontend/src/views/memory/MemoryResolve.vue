<script setup lang="ts">
/**
 * 去重与清理。
 *
 * 两个动作都是**先预览、再确认**：
 * - 合并把同一 fingerprint 下的行标成"已被合并"，不可逆；
 * - 清理直接删行，删除之后不能恢复。
 *
 * 提交失败时结果是不确定的（请求可能已经落地），所以这里不自动重试、也不
 * 保留"再点一次提交"的旧预览 —— 让用户重新预览，看清楚现在的库再决定。
 */

import { computed, ref } from 'vue'

import type { ApiFailure } from '../../api/client'
import { describeFailure } from '../../api/errors'
import {
  cleanupFacts,
  dedupeFacts,
  type CleanupReportView,
  type DedupeReportView,
  type FactStatus,
  type FactView,
  type MemorySurface,
} from '../../api/memory'
import { MEMORY } from '../../copy/memory'
import { confirmAction } from '../../shared/confirm'
import { formatDateTime } from '../../shared/format'
import { toFailure } from '../../shared/resource'
import { notify } from '../../shared/toast'

const surfaces: MemorySurface[] = ['mastery', 'reading', 'practice', 'user']
const statusOptions: FactStatus[] = ['active', 'retracted', 'superseded']

/* ---------------- 去重 ---------------- */

const dedupePreview = ref<DedupeReportView | null>(null)
const dedupeResult = ref<DedupeReportView | null>(null)
const dedupePreviewing = ref(false)
const dedupeCommitting = ref(false)
const dedupeFailure = ref<ApiFailure | null>(null)
const dedupeFailureKind = ref<'preview' | 'commit'>('preview')
const dedupeStale = ref(false)

const canDedupeCommit = computed(
  () =>
    dedupePreview.value !== null &&
    dedupePreview.value.groups.length > 0 &&
    !dedupeStale.value &&
    !dedupeCommitting.value,
)

async function previewDedupe(): Promise<void> {
  dedupePreviewing.value = true
  dedupeFailure.value = null
  dedupeResult.value = null
  try {
    dedupePreview.value = await dedupeFacts({ dry_run: true })
    dedupeStale.value = false
  } catch (cause) {
    dedupeFailureKind.value = 'preview'
    dedupeFailure.value = toFailure(cause)
  } finally {
    dedupePreviewing.value = false
  }
}

async function commitDedupe(): Promise<void> {
  const preview = dedupePreview.value
  if (!preview || preview.groups.length === 0) return
  const confirmed = await confirmAction({
    title: MEMORY.dedupeConfirmTitle,
    body: MEMORY.dedupeConfirmBody(preview.superseded),
    note: MEMORY.dedupeConfirmNote,
    confirmLabel: MEMORY.dedupeCommit,
    danger: true,
  })
  if (!confirmed) return

  dedupeCommitting.value = true
  dedupeFailure.value = null
  dedupeResult.value = null
  try {
    const report = await dedupeFacts({ dry_run: false })
    dedupeResult.value = report
    dedupePreview.value = null
    notify(MEMORY.dedupeDone(report.superseded), 'success')
  } catch (cause) {
    dedupeFailureKind.value = 'commit'
    dedupeFailure.value = toFailure(cause)
    dedupeStale.value = true
  } finally {
    dedupeCommitting.value = false
  }
}

/* ---------------- 清理 ---------------- */

const cleanupBefore = ref('')
const cleanupSurface = ref<'' | MemorySurface>('')
const cleanupStatuses = ref<FactStatus[]>([])
const cleanupPreview = ref<CleanupReportView | null>(null)
const cleanupResult = ref<CleanupReportView | null>(null)
const cleanupPreviewing = ref(false)
const cleanupCommitting = ref(false)
const cleanupFailure = ref<ApiFailure | null>(null)
const cleanupFailureKind = ref<'preview' | 'commit'>('preview')
const cleanupStale = ref(false)

const cleanupInvalidTime = computed(
  () => cleanupBefore.value.trim() !== '' && toRfc3339(cleanupBefore.value) === null,
)
const canCleanupPreview = computed(
  () => toRfc3339(cleanupBefore.value) !== null && !cleanupPreviewing.value,
)
const canCleanupCommit = computed(
  () =>
    cleanupPreview.value !== null &&
    !cleanupStale.value &&
    !cleanupCommitting.value,
)

/** `datetime-local` 是本地时间；后端要 RFC3339（带时区）。 */
function toRfc3339(value: string): string | null {
  if (value.trim() === '') return null
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return null
  return date.toISOString()
}

function cleanupRequestBody(dryRun: boolean): Parameters<typeof cleanupFacts>[0] | null {
  const before = toRfc3339(cleanupBefore.value)
  if (!before) return null
  return {
    before,
    ...(cleanupSurface.value ? { surface: cleanupSurface.value } : {}),
    ...(cleanupStatuses.value.length > 0 ? { status: [...cleanupStatuses.value] } : {}),
    dry_run: dryRun,
  }
}

async function previewCleanup(): Promise<void> {
  const body = cleanupRequestBody(true)
  if (!body) return
  cleanupPreviewing.value = true
  cleanupFailure.value = null
  cleanupResult.value = null
  try {
    cleanupPreview.value = await cleanupFacts(body)
    cleanupStale.value = false
  } catch (cause) {
    cleanupFailureKind.value = 'preview'
    cleanupFailure.value = toFailure(cause)
  } finally {
    cleanupPreviewing.value = false
  }
}

async function commitCleanup(): Promise<void> {
  const preview = cleanupPreview.value
  if (!preview) return
  const body = cleanupRequestBody(false)
  if (!body) return
  const confirmed = await confirmAction({
    title: MEMORY.cleanupConfirmTitle,
    body: MEMORY.cleanupConfirmBody(preview.facts.length),
    note: MEMORY.cleanupConfirmNote,
    confirmLabel: MEMORY.cleanupCommit,
    danger: true,
  })
  if (!confirmed) return

  cleanupCommitting.value = true
  cleanupFailure.value = null
  cleanupResult.value = null
  try {
    const report = await cleanupFacts(body)
    cleanupResult.value = report
    cleanupPreview.value = null
    notify(MEMORY.cleanupDone(report.purged), 'success')
  } catch (cause) {
    cleanupFailureKind.value = 'commit'
    cleanupFailure.value = toFailure(cause)
    cleanupStale.value = true
  } finally {
    cleanupCommitting.value = false
  }
}

/* ---------------- 展示 ---------------- */

function toggleStatus(value: FactStatus): void {
  cleanupStatuses.value = cleanupStatuses.value.includes(value)
    ? cleanupStatuses.value.filter((item) => item !== value)
    : [...cleanupStatuses.value, value]
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

function cleanupFactLine(fact: FactView): string {
  return `${formatDateTime(fact.created_at)} · ${surfaceLabel(fact.surface)}${
    fact.scope ? ` · ${fact.scope}` : ''
  }`
}
</script>

<template>
  <div class="resolve">
    <header class="head">
      <h2 class="head__title">{{ MEMORY.resolveTitle }}</h2>
      <p class="head__description">{{ MEMORY.resolveDescription }}</p>
    </header>

    <!-- 去重 -->
    <section class="card">
      <header class="card__head">
        <div>
          <h3 class="card__title">{{ MEMORY.dedupeTitle }}</h3>
          <p class="card__description">{{ MEMORY.dedupeDescription }}</p>
        </div>
        <div class="card__actions">
          <button
            type="button"
            class="ghost"
            :disabled="dedupePreviewing || dedupeCommitting"
            @click="previewDedupe"
          >
            {{ dedupePreviewing ? MEMORY.dedupePreviewing : MEMORY.dedupePreview }}
          </button>
          <button
            type="button"
            class="danger"
            :disabled="!canDedupeCommit"
            @click="commitDedupe"
          >
            {{ dedupeCommitting ? MEMORY.dedupeCommitting : MEMORY.dedupeCommit }}
          </button>
        </div>
      </header>

      <p v-if="dedupeFailure" class="error">
        {{ dedupeFailureKind === 'commit' ? MEMORY.dedupeCommitFailed : MEMORY.dedupePreviewFailed }}：{{
          describeFailure(dedupeFailure)
        }}
      </p>
      <p v-if="dedupeStale" class="warn">{{ MEMORY.dedupeNeedPreview }}</p>

      <p v-if="dedupeResult" class="result">
        {{ MEMORY.dedupeDone(dedupeResult.superseded) }}
        <span v-if="dedupeResult.result === 'partial'" class="warn">
          {{ MEMORY.dedupePartial }}
        </span>
      </p>

      <template v-if="dedupePreview">
        <p v-if="dedupePreview.groups.length === 0" class="note">
          {{ MEMORY.dedupeEmpty }}
        </p>
        <template v-else>
          <p class="note">{{ MEMORY.dedupeGroups(dedupePreview.groups.length) }}</p>
          <ul class="groups">
            <li v-for="group in dedupePreview.groups" :key="group.fingerprint" class="group">
              <p class="group__statement">{{ group.statement }}</p>
              <p class="group__ids">
                <span class="id-label">{{ MEMORY.dedupeSurvivor }}</span>
                <code>{{ group.survivor }}</code>
              </p>
              <p class="group__ids">
                <span class="id-label">{{ MEMORY.dedupeSuperseded }}</span>
                <code v-for="id in group.superseded" :key="id" class="id">{{ id }}</code>
              </p>
              <p class="group__fingerprint">{{ group.fingerprint }}</p>
            </li>
          </ul>
        </template>
      </template>
    </section>

    <!-- 清理 -->
    <section class="card">
      <header class="card__head">
        <div>
          <h3 class="card__title">{{ MEMORY.cleanupTitle }}</h3>
          <p class="card__description">{{ MEMORY.cleanupDescription }}</p>
        </div>
      </header>

      <div class="filters">
        <label class="field">
          <span class="field__label">{{ MEMORY.cleanupBefore }}</span>
          <input v-model="cleanupBefore" type="datetime-local" class="field__input" />
        </label>

        <label class="field">
          <span class="field__label">{{ MEMORY.cleanupSurface }}</span>
          <select v-model="cleanupSurface" class="field__input">
            <option value="">{{ MEMORY.allSurfaces }}</option>
            <option v-for="value in surfaces" :key="value" :value="value">
              {{ surfaceLabel(value) }}
            </option>
          </select>
        </label>

        <fieldset class="statuses">
          <legend class="field__label">{{ MEMORY.cleanupStatuses }}</legend>
          <label v-for="option in statusOptions" :key="option" class="status">
            <input
              type="checkbox"
              :checked="cleanupStatuses.includes(option)"
              @change="toggleStatus(option)"
            />
            {{ statusLabel(option) }}
          </label>
        </fieldset>
      </div>

      <p v-if="cleanupInvalidTime" class="error">{{ MEMORY.cleanupNeedTime }}</p>
      <p v-else-if="cleanupBefore.trim() === ''" class="note">{{ MEMORY.cleanupNeedTime }}</p>

      <div class="card__actions card__actions--left">
        <button
          type="button"
          class="ghost"
          :disabled="!canCleanupPreview || cleanupCommitting"
          @click="previewCleanup"
        >
          {{ cleanupPreviewing ? MEMORY.cleanupPreviewing : MEMORY.cleanupPreview }}
        </button>
        <button
          type="button"
          class="danger"
          :disabled="!canCleanupCommit"
          @click="commitCleanup"
        >
          {{ cleanupCommitting ? MEMORY.cleanupCommitting : MEMORY.cleanupCommit }}
        </button>
      </div>

      <p v-if="cleanupFailure" class="error">
        {{ cleanupFailureKind === 'commit' ? MEMORY.cleanupCommitFailed : MEMORY.cleanupPreviewFailed }}：{{
          describeFailure(cleanupFailure)
        }}
      </p>
      <p v-if="cleanupStale" class="warn">{{ MEMORY.cleanupNeedPreview }}</p>

      <p v-if="cleanupResult" class="result">
        {{ MEMORY.cleanupDone(cleanupResult.purged) }}
      </p>

      <template v-if="cleanupPreview">
        <p v-if="cleanupPreview.facts.length === 0" class="note">
          {{ MEMORY.cleanupEmpty }}
        </p>
        <template v-else>
          <p class="note">
            {{ MEMORY.cleanupPreviewTitle }} ·
            {{ MEMORY.cleanupPurged(cleanupPreview.facts.length) }}
          </p>
          <ul class="purge-list">
            <li v-for="fact in cleanupPreview.facts" :key="fact.fact_id" class="purge">
              <span class="chip chip--status">{{ statusLabel(fact.status) }}</span>
              <span class="purge__statement">{{ fact.statement }}</span>
              <span class="purge__meta">{{ cleanupFactLine(fact) }}</span>
            </li>
          </ul>
        </template>
      </template>
    </section>
  </div>
</template>

<style scoped src="./MemoryResolve.css"></style>
