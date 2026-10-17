/**
 * 这一轮用哪个人格。
 *
 * 人格是**这一轮**的，和附件、知识库一样：它进 `start_turn` 的 `persona`
 * 字段，不写进会话。区别是选择会跨轮保留 —— 用户选了一个"耐心的物理老师"，
 * 下一句通常还想要它，而不是每问一句就重选一次。
 *
 * 列表来自服务端。选中的那个人格如果已经被删掉，刷新列表时就把它清掉，
 * 否则下一轮会点一个不存在的 id。
 */

import { computed, ref } from 'vue'
import type { RouteLocationNormalizedLoaded } from 'vue-router'

import type { ApiFailure } from '../../api/client'
import { listPersonas, type Persona } from '../../api/personas'
import type { PersonaChoice } from '../../components/chat/Composer.vue'
import { toFailure } from '../../shared/resource'

function initialSelection(route: RouteLocationNormalizedLoaded): string {
  const value = route.query.persona
  return typeof value === 'string' ? value : ''
}

export function useChatPersona(route: RouteLocationNormalizedLoaded) {
  const records = ref<Persona[]>([])
  const selectedId = ref(initialSelection(route))
  const loading = ref(false)
  const failure = ref<ApiFailure | null>(null)
  let loaded = false

  const items = computed<PersonaChoice[]>(() =>
    records.value.map((persona) => ({
      id: persona.persona_id,
      label: persona.name,
      detail: persona.description,
    })),
  )

  async function load(force = false): Promise<void> {
    if (loaded && !force) return
    loading.value = true
    failure.value = null
    try {
      records.value = (await listPersonas()).personas
      loaded = true
      if (
        selectedId.value !== '' &&
        !records.value.some((persona) => persona.persona_id === selectedId.value)
      ) {
        selectedId.value = ''
      }
    } catch (cause) {
      failure.value = toFailure(cause)
    } finally {
      loading.value = false
    }
  }

  function select(personaId: string): void {
    selectedId.value = personaId
  }

  return { items, selectedId, loading, failure, load, select }
}
