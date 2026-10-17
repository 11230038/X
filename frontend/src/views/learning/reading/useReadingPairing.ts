/**
 * 双语配对。
 *
 * 两步：先选候选，再显式确认；确认成功后**重新读一次**，只有服务端返回
 * `enabled` 才承认已启用。未启用时下游一律是"未启用"，不是空内容。
 */

import { ref, type Ref } from 'vue'

import type { ApiFailure } from '../../../api/client'
import {
  cancelPairing,
  confirmPairing,
  getPairings,
  type Bilingual,
  type PairingCandidate,
} from '../../../api/reading'
import { READING } from '../../../copy/reading'
import { toFailure } from '../../../shared/resource'
import { notify } from '../../../shared/toast'

type Status = 'idle' | 'loading' | 'ready' | 'failed'

export function useReadingPairing(currentId: Ref<string>) {
  const bilingual = ref<Bilingual | null>(null)
  const candidates = ref<PairingCandidate[]>([])
  const pairingStatus = ref<Status>('idle')
  const pairingFailure = ref<ApiFailure | null>(null)
  const pairingActionFailure = ref<ApiFailure | null>(null)
  const pairChoice = ref('')
  const pairingBusy = ref(false)

  async function loadPairings(): Promise<void> {
    const materialId = currentId.value
    pairingStatus.value = 'loading'
    pairingFailure.value = null
    try {
      const loaded = await getPairings(materialId)
      if (currentId.value !== materialId) return
      bilingual.value = loaded.bilingual
      candidates.value = loaded.candidates
      pairingStatus.value = 'ready'
    } catch (cause) {
      if (currentId.value !== materialId) return
      pairingFailure.value = toFailure(cause)
      pairingStatus.value = 'failed'
    }
  }

  async function confirmSelectedPairing(): Promise<void> {
    const candidate = candidates.value.find((item) => item.material_id === pairChoice.value)
    if (!candidate) return
    pairingBusy.value = true
    pairingActionFailure.value = null
    try {
      await confirmPairing(currentId.value, candidate.material_id)
      pairChoice.value = ''
      await loadPairings()
      notify(READING.pairingsEnabledNotice, 'success')
    } catch (cause) {
      pairingActionFailure.value = toFailure(cause)
    } finally {
      pairingBusy.value = false
    }
  }

  async function cancelCurrentPairing(): Promise<void> {
    if (!bilingual.value?.pairing) return
    pairingBusy.value = true
    pairingActionFailure.value = null
    try {
      await cancelPairing(bilingual.value.pairing.pairing_id)
      await loadPairings()
      notify(READING.pairingsCancelled, 'success')
    } catch (cause) {
      pairingActionFailure.value = toFailure(cause)
    } finally {
      pairingBusy.value = false
    }
  }

  function resetPairing(): void {
    bilingual.value = null
    candidates.value = []
    pairingStatus.value = 'idle'
    pairingFailure.value = null
    pairingActionFailure.value = null
    pairChoice.value = ''
  }

  return {
    bilingual,
    candidates,
    pairingStatus,
    pairingFailure,
    pairingActionFailure,
    pairChoice,
    pairingBusy,
    loadPairings,
    confirmSelectedPairing,
    cancelCurrentPairing,
    resetPairing,
  }
}
