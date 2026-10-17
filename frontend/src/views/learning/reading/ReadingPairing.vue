<script setup lang="ts">
/**
 * 双语配对。
 *
 * 候选只是一份推荐；在用户显式确认并且服务端返回 `enabled` 之前，页面不会
 * 显示任何双语正文。
 */

import { computed } from 'vue'

import type { ApiFailure } from '../../../api/client'
import { describeFailure } from '../../../api/errors'
import type { Bilingual, PairingCandidate } from '../../../api/reading'
import PartialBanner from '../../../components/common/PartialBanner.vue'
import SkeletonBlock from '../../../components/common/SkeletonBlock.vue'
import { READING } from '../../../copy/reading'

const props = defineProps<{
  bilingual: Bilingual | null
  candidates: PairingCandidate[]
  status: 'idle' | 'loading' | 'ready' | 'failed'
  failure: ApiFailure | null
  actionFailure: ApiFailure | null
  busy: boolean
  choice: string
}>()

const emit = defineEmits<{
  retry: []
  cancel: []
  confirm: []
  'update:choice': [value: string]
}>()

const selectedName = computed(
  () => props.candidates.find((candidate) => candidate.material_id === props.choice)?.filename ?? '',
)

function choose(event: Event): void {
  emit('update:choice', (event.target as HTMLInputElement).value)
}
</script>

<template>
  <section class="panel__section">
    <h2 class="panel__title">{{ READING.pairingsTitle }}</h2>

    <SkeletonBlock v-if="status === 'loading'" :lines="3" :line-height="28" />

    <PartialBanner
      v-else-if="status === 'failed' && failure"
      :failure="failure"
      :text="READING.pairingsFailed"
      @retry="$emit('retry')"
    />

    <template v-else-if="bilingual">
      <div v-if="bilingual.enabled && bilingual.pairing" class="pairing">
        <p class="pairing__state">{{ READING.pairingsEnabled }}</p>
        <p class="pairing__languages">
          {{ READING.pairingsLanguages(bilingual.pairing.primary_language, bilingual.pairing.counterpart_language) }}
        </p>
        <button type="button" class="ghost" :disabled="busy" @click="$emit('cancel')">
          {{ busy ? READING.pairingsCancelling : READING.pairingsCancel }}
        </button>
      </div>

      <div v-else class="pairing">
        <p class="pairing__state">{{ READING.pairingsDisabled }}</p>
        <p v-if="bilingual.reason" class="pairing__reason">
          {{ READING.pairingsReason(bilingual.reason) }}
        </p>

        <template v-if="candidates.length > 0">
          <p class="panel__hint">{{ READING.pairingsTwoStep }}</p>
          <ul class="candidates">
            <li v-for="candidate in candidates" :key="candidate.material_id">
              <label class="candidate" :class="{ 'candidate--on': choice === candidate.material_id }">
                <input
                  type="radio"
                  name="reading-pairing"
                  :value="candidate.material_id"
                  :checked="choice === candidate.material_id"
                  @change="choose"
                />
                <span class="candidate__body">
                  <span class="candidate__name">{{ candidate.filename }}</span>
                  <span class="candidate__meta">
                    {{ candidate.language }} · {{ READING.unitChars(candidate.text_chars) }} ·
                    {{ READING.pairingsScore(candidate.score) }}
                  </span>
                </span>
              </label>
            </li>
          </ul>
          <button
            type="button"
            class="primary"
            :disabled="choice === '' || busy"
            @click="$emit('confirm')"
          >
            {{ choice ? READING.pairingsConfirm(selectedName) : READING.pairingsSelect }}
          </button>
          <p class="panel__hint">{{ READING.pairingsConfirmHint }}</p>
        </template>

        <p v-else class="panel__empty">{{ READING.pairingsEmpty }}</p>
      </div>

      <p v-if="actionFailure" class="inline-error">
        {{ READING.pairingsFailedAction }}：{{ describeFailure(actionFailure) }}
      </p>
    </template>
  </section>
</template>

<style scoped src="./controls.css"></style>
<style scoped src="./panel-head.css"></style>
<style scoped src="./list-reset.css"></style>
<style scoped src="./pairing.css"></style>
