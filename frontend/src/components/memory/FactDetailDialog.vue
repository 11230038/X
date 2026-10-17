<script setup lang="ts">
/**
 * 一条事实的详情与修改。
 *
 * 证据是这张对话框存在的理由：一条事实如果没有证据，就只是一个断言。修改、
 * 撤回、恢复都在这里发生，并且每一次都告诉读者它会记进审计。
 *
 * 永久删除**不在这里**：它只能通过「去重与清理」的预览确认执行。
 */

import { onMounted, ref } from 'vue'

import type { ApiFailure } from '../../api/client'
import { describeFailure } from '../../api/errors'
import {
  editFact,
  getFact,
  restoreFact,
  retractFact,
  type FactDetailView,
} from '../../api/memory'
import { MEMORY } from '../../copy/memory'
import { confirmAction } from '../../shared/confirm'
import { formatDateTime, formatPercent } from '../../shared/format'
import { toFailure } from '../../shared/resource'
import { notify } from '../../shared/toast'
import FailureState from '../common/FailureState.vue'
import SkeletonBlock from '../common/SkeletonBlock.vue'

const props = defineProps<{ factId: string; startEditing?: boolean }>()

const emit = defineEmits<{
  close: []
  changed: []
}>()

const dialog = ref<HTMLDialogElement | null>(null)
const fact = ref<FactDetailView | null>(null)
const status = ref<'loading' | 'ready' | 'failed'>('loading')
const failure = ref<ApiFailure | null>(null)
const actionFailure = ref<ApiFailure | null>(null)
const busy = ref(false)

const editing = ref(false)
const editStatement = ref('')
const editScope = ref('')

async function load(): Promise<void> {
  status.value = 'loading'
  failure.value = null
  try {
    fact.value = await getFact(props.factId)
    status.value = 'ready'
    if (props.startEditing) beginEdit()
  } catch (cause) {
    failure.value = toFailure(cause)
    status.value = 'failed'
  }
}

function close(): void {
  if (dialog.value?.open) dialog.value.close()
}

function onClosed(): void {
  emit('close')
}

function beginEdit(): void {
  if (!fact.value) return
  editing.value = true
  editStatement.value = fact.value.statement
  editScope.value = fact.value.scope ?? ''
  actionFailure.value = null
}

function cancelEdit(): void {
  editing.value = false
  actionFailure.value = null
}

async function saveEdit(): Promise<void> {
  const statement = editStatement.value.trim()
  if (statement === '') return
  busy.value = true
  actionFailure.value = null
  try {
    fact.value = await editFact(props.factId, {
      statement,
      scope: editScope.value.trim(),
    })
    editing.value = false
    notify(MEMORY.editSaved, 'success')
    emit('changed')
  } catch (cause) {
    actionFailure.value = toFailure(cause)
  } finally {
    busy.value = false
  }
}

async function retract(): Promise<void> {
  const confirmed = await confirmAction({
    title: MEMORY.retractConfirmTitle,
    body: MEMORY.retractConfirmBody,
    note: MEMORY.retractConfirmNote,
    confirmLabel: MEMORY.retract,
    danger: true,
  })
  if (!confirmed) return
  busy.value = true
  actionFailure.value = null
  try {
    fact.value = await retractFact(props.factId)
    emit('changed')
  } catch (cause) {
    actionFailure.value = toFailure(cause)
  } finally {
    busy.value = false
  }
}

async function restore(): Promise<void> {
  const confirmed = await confirmAction({
    title: MEMORY.restoreConfirmTitle,
    body: MEMORY.restoreConfirmBody,
    note: MEMORY.restoreConfirmNote,
    confirmLabel: MEMORY.restore,
  })
  if (!confirmed) return
  busy.value = true
  actionFailure.value = null
  try {
    fact.value = await restoreFact(props.factId)
    emit('changed')
  } catch (cause) {
    actionFailure.value = toFailure(cause)
  } finally {
    busy.value = false
  }
}

function surfaceLabel(surface: string): string {
  return MEMORY.surfaces[surface] ?? surface
}

function statusLabel(value: string): string {
  return value === 'retracted'
    ? MEMORY.statusRetracted
    : value === 'superseded'
      ? MEMORY.statusSuperseded
      : MEMORY.statusActive
}

function payloadText(payload: unknown): string {
  if (payload === undefined || payload === null) return ''
  if (typeof payload === 'string') return payload
  try {
    return JSON.stringify(payload, null, 2)
  } catch {
    return String(payload)
  }
}

onMounted(() => {
  dialog.value?.showModal()
  void load()
})
</script>

<template>
  <dialog ref="dialog" class="detail" @cancel.prevent="close" @close="onClosed">
    <header class="detail__head">
      <h2 class="detail__title">{{ MEMORY.detailTitle }}</h2>
      <button type="button" class="close" :aria-label="MEMORY.close" @click="close">×</button>
    </header>

    <SkeletonBlock v-if="status === 'loading'" :lines="6" :line-height="32" />

    <FailureState
      v-else-if="status === 'failed' && failure"
      :failure="failure"
      :title="MEMORY.loadFailed"
      @retry="load"
    />

    <template v-else-if="fact">
      <div class="chips">
        <span class="chip" :class="`chip--${fact.status}`">{{ statusLabel(fact.status) }}</span>
        <span class="chip">{{ surfaceLabel(fact.surface) }}</span>
        <span v-if="fact.scope" class="chip">{{ MEMORY.scope }}：{{ fact.scope }}</span>
        <span v-if="fact.sensitive" class="chip chip--sensitive">{{ MEMORY.sensitive }}</span>
        <span class="chip">
          {{ fact.confirmed_by === 'user' ? MEMORY.confirmedUser : MEMORY.confirmedGate }}
        </span>
        <span class="chip">{{ MEMORY.confidence }} {{ formatPercent(fact.confidence, 0) }}</span>
        <span class="chip">{{ MEMORY.version }} {{ fact.version }}</span>
      </div>

      <!-- 编辑 -->
      <form v-if="editing" class="edit" @submit.prevent="saveEdit">
        <label class="field">
          <span class="field__label">{{ MEMORY.editStatement }}</span>
          <textarea v-model="editStatement" class="field__area" rows="4"></textarea>
        </label>
        <label class="field">
          <span class="field__label">{{ MEMORY.editScope }}</span>
          <input v-model="editScope" class="field__input" type="text" />
        </label>
        <p v-if="editStatement.trim() === ''" class="field__hint">{{ MEMORY.editEmpty }}</p>
        <div class="actions">
          <button type="button" class="ghost" @click="cancelEdit">{{ MEMORY.cancel }}</button>
          <button type="submit" class="primary" :disabled="busy || editStatement.trim() === ''">
            {{ busy ? MEMORY.saving : MEMORY.save }}
          </button>
        </div>
      </form>

      <template v-else>
        <p class="statement">{{ fact.statement }}</p>

        <dl class="meta">
          <dt>{{ MEMORY.createdAt }}</dt>
          <dd>{{ formatDateTime(fact.created_at) }}</dd>
          <template v-if="fact.updated_at">
            <dt>{{ MEMORY.updatedAt }}</dt>
            <dd>{{ formatDateTime(fact.updated_at) }}</dd>
          </template>
          <template v-if="fact.retract_time">
            <dt>{{ MEMORY.retractTime }}</dt>
            <dd>{{ formatDateTime(fact.retract_time) }}</dd>
          </template>
        </dl>

        <div class="actions">
          <button type="button" class="ghost" :disabled="busy" @click="beginEdit">
            {{ MEMORY.edit }}
          </button>
          <button
            v-if="fact.status === 'active'"
            type="button"
            class="danger"
            :disabled="busy"
            @click="retract"
          >
            {{ busy ? MEMORY.retracting : MEMORY.retract }}
          </button>
          <button
            v-else-if="fact.status === 'retracted'"
            type="button"
            class="ghost"
            :disabled="busy"
            @click="restore"
          >
            {{ busy ? MEMORY.restoring : MEMORY.restore }}
          </button>
        </div>
        <p class="hint">{{ MEMORY.deleteHint }}</p>
      </template>

      <p v-if="actionFailure" class="error">
        {{ MEMORY.editFailed }}：{{ describeFailure(actionFailure) }}
      </p>

      <section class="evidence">
        <h3 class="evidence__title">{{ MEMORY.evidence }}</h3>
        <p v-if="fact.evidence.length === 0" class="hint">{{ MEMORY.evidenceEmpty }}</p>
        <ul v-else class="evidence__list">
          <li v-for="trace in fact.evidence" :key="trace.trace_id" class="trace">
            <header class="trace__head">
              <span class="chip">{{ surfaceLabel(trace.surface) }}</span>
              <span class="trace__kind">{{ trace.kind }}</span>
              <span class="trace__time">{{ formatDateTime(trace.created_at) }}</span>
            </header>
            <p class="trace__statement">{{ trace.statement }}</p>
            <p v-if="trace.session_id" class="trace__link">
              {{ MEMORY.traceSession }} {{ trace.session_id }}
              <template v-if="trace.turn_id"> · {{ MEMORY.traceTurn }} {{ trace.turn_id }}</template>
            </p>
            <pre v-if="payloadText(trace.payload)" class="trace__payload">{{ payloadText(trace.payload) }}</pre>
          </li>
        </ul>
      </section>
    </template>
  </dialog>
</template>

<style scoped src="./FactDetailDialog.css"></style>
