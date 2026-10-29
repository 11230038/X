<script setup lang="ts">
/**
 * 工作区导入。三步：上传 ZIP 预览（逐项 create / conflict / missing）→ 人
 * 确认 → 提交。失败的作业可以恢复；已提交的不行。
 */

import { ref } from 'vue'

import type { ApiFailure } from '../../../api/client'
import {
  commitImport,
  previewImport,
  recoverJob,
  type WorkspaceJob,
  type WorkspacePreview,
} from '../../../api/workspace'
import { SETTINGS } from '../../../copy/settings'
import { toFailure } from '../../../shared/resource'
import { notify } from '../../../shared/toast'

import { ACTION_LABELS, REASON_LABELS, jobStateLabel, kindLabel } from './labels'
import AppIcon from '../../../components/common/AppIcon.vue'

const importFile = ref<File | null>(null)
const preview = ref<WorkspacePreview | null>(null)
const previewBusy = ref(false)
const commitBusy = ref(false)
const importFailure = ref<ApiFailure | null>(null)
const importJob = ref<WorkspaceJob | null>(null)
const recoverBusy = ref(false)

function onFileChosen(event: Event): void {
  const input = event.target as HTMLInputElement
  importFile.value = input.files?.[0] ?? null
  preview.value = null
  importJob.value = null
  importFailure.value = null
}

async function runPreview(): Promise<void> {
  if (!importFile.value) return
  previewBusy.value = true
  importFailure.value = null
  preview.value = null
  importJob.value = null
  try {
    const result = await previewImport(importFile.value)
    preview.value = result.preview
  } catch (cause) {
    importFailure.value = toFailure(cause)
  } finally {
    previewBusy.value = false
  }
}

async function runCommit(): Promise<void> {
  if (!preview.value) return
  commitBusy.value = true
  importFailure.value = null
  try {
    const result = await commitImport(preview.value.job_id)
    importJob.value = result.job
    notify('导入已提交', 'success')
  } catch (cause) {
    importFailure.value = toFailure(cause)
  } finally {
    commitBusy.value = false
  }
}

async function runRecover(): Promise<void> {
  if (!importJob.value) return
  recoverBusy.value = true
  importFailure.value = null
  try {
    const result = await recoverJob(importJob.value.job_id)
    importJob.value = result.job
  } catch (cause) {
    importFailure.value = toFailure(cause)
  } finally {
    recoverBusy.value = false
  }
}
</script>

<template>
  <section class="card">
    <div class="card__head">
      <span class="card__icon card__icon--warm" aria-hidden="true"><AppIcon name="inbox" /></span>
      <h2 class="card__title">{{ SETTINGS.import }}</h2>
    </div>
    <p class="hint">先上传 ZIP 预览；预览通过后提交才会真正写入。</p>

    <div class="import-row">
      <input type="file" accept=".zip,application/zip" @change="onFileChosen" />
      <button
        type="button"
        class="ghost"
        :disabled="previewBusy || !importFile"
        @click="runPreview"
      >
        {{ previewBusy ? '正在预览…' : SETTINGS.importPreview }}
      </button>
    </div>

    <p v-if="importFailure" class="error">导入没能继续：{{ importFailure.message }}</p>

    <template v-if="preview">
      <p class="hint">
        共 {{ preview.objects }} 个对象 ·
        新建 {{ preview.actions.create ?? 0 }} ·
        冲突 {{ preview.actions.conflict ?? 0 }} ·
        缺少 {{ preview.actions.missing ?? 0 }}
      </p>
      <ul class="preview-items">
        <li v-for="item in preview.items" :key="`${item.ref.kind}:${item.ref.id}`" class="preview">
          <span class="chip">{{ kindLabel(item.ref.kind) }}</span>
          <span class="preview__id">{{ item.ref.id }}</span>
          <span class="preview__action">{{ ACTION_LABELS[item.action] ?? item.action }}</span>
          <span v-if="item.reason" class="preview__reason">
            {{ REASON_LABELS[item.reason] ?? item.reason }}
          </span>
          <span v-if="item.needs?.length" class="preview__reason">
            依赖：{{ item.needs.map((need) => need.id).join('、') }}
          </span>
        </li>
      </ul>
      <div class="actions">
        <button type="button" class="primary" :disabled="commitBusy" @click="runCommit">
          {{ commitBusy ? '正在提交…' : SETTINGS.importCommit }}
        </button>
      </div>
    </template>

    <div v-if="importJob" class="job">
      <span class="job__state">
        {{ SETTINGS.jobState }}：{{ jobStateLabel(importJob.state) }}
      </span>
      <span v-if="importJob.failure_code" class="job__id">{{ importJob.failure_code }}</span>
      <button
        v-if="importJob.state === 'failed'"
        type="button"
        class="ghost"
        :disabled="recoverBusy"
        @click="runRecover"
      >
        {{ recoverBusy ? '正在恢复…' : SETTINGS.importRecover }}
      </button>
    </div>
  </section>
</template>

<style scoped src="../SettingsWorkspace.css"></style>
