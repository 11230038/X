/**
 * 这一轮用哪个人格。
 *
 * 人格是**这一轮**的，和附件、知识库一样：它进 `start_turn` 的 `persona`
 * 字段，不写进会话。区别是选择会跨轮保留 —— 用户选了一个"耐心的物理老师"，
 * 下一句通常还想要它，而不是每问一句就重选一次。
 *
 * 列表来自服务端。选中的那个人格如果已经被删掉，刷新列表时就把它清掉，
 * 否则下一轮会点一个不存在的 id。
 *
 * **锁定**是给题目问答页用的：那里每一轮都必须带上同一个人格，输入区不提供
 * 切换。人格是账号级数据、id 不跨账号，所以锁定按名字找；账号里还没有同名
 * 人格时，按页面给的种子内容建一个。`ensure()` 是发送前的等待点 —— 首页的
 * 「开始提问」可能早于列表返回，不 await 就会把第一句话发成没有人格的一轮。
 *
 * **不用人格**是给路径对话页用的：那里既不给选择器，也不把人格带进任何一轮。
 * 连列表都不读 —— 用不到的请求不发，`selectedId` 恒为空，`SendOptions` 里
 * 自然不会有 `persona` 字段。
 */

import { computed, ref } from 'vue'
import type { RouteLocationNormalizedLoaded } from 'vue-router'

import type { ApiFailure } from '../../api/client'
import { createPersona, listPersonas, type Persona } from '../../api/personas'
import type { PersonaChoice } from '../../components/chat/Composer.vue'
import { isSystemPersona } from '../../copy/personas'
import { toFailure } from '../../shared/resource'

/** 一页只允许用这一个人格；内容只在建号时作为种子。 */
export interface PersonaLock {
  name: string
  description: string
  content: string
}

function initialSelection(route: RouteLocationNormalizedLoaded, lock?: PersonaLock): string {
  if (lock) return ''
  const value = route.query.persona
  return typeof value === 'string' ? value : ''
}

export function useChatPersona(
  route: RouteLocationNormalizedLoaded,
  lock?: PersonaLock,
  enabled = true,
) {
  const records = ref<Persona[]>([])
  const selectedId = ref(enabled ? initialSelection(route, lock) : '')
  const loading = ref(false)
  const failure = ref<ApiFailure | null>(null)
  let loaded = false
  /** 列表请求已在飞：`load()` 与 `ensure()` 共用它，不重复发。 */
  let pending: Promise<void> | null = null

  const locked = enabled && lock !== undefined

  const items = computed<PersonaChoice[]>(() => {
    if (!enabled) return []
    // 锁定的页面只列那一个；普通页面的选择器不列系统人格（写作教练那类），
    // 它们只属于自己那一页。
    const visible = lock
      ? records.value.filter((persona) => persona.name === lock.name)
      : records.value.filter((persona) => !isSystemPersona(persona.name))
    return visible.map((persona) => ({
      id: persona.persona_id,
      label: persona.name,
      detail: persona.description,
    }))
  })

  function remember(list: Persona[]): void {
    records.value = [...list].sort((left, right) => left.name.localeCompare(right.name))
  }

  /**
   * 锁定人格的解析：先找同名的，没有就建。
   *
   * 建失败要再读一次列表：名字在同一账号内唯一，另一个标签页可能刚好先建了
   * 同名的那一行，这时正确的动作是采用它，而不是把这一页报成失败。
   */
  async function resolveLock(): Promise<void> {
    if (!lock) return
    const match = records.value.find((persona) => persona.name === lock.name)
    if (match) {
      selectedId.value = match.persona_id
      return
    }
    try {
      const created = await createPersona({
        name: lock.name,
        description: lock.description,
        content: lock.content,
      })
      remember([...records.value, created])
      selectedId.value = created.persona_id
    } catch (cause) {
      remember((await listPersonas()).personas)
      const raced = records.value.find((persona) => persona.name === lock.name)
      if (!raced) throw cause
      selectedId.value = raced.persona_id
    }
  }

  async function loadOnce(): Promise<void> {
    loading.value = true
    failure.value = null
    try {
      remember((await listPersonas()).personas)
      loaded = true
      if (lock) {
        await resolveLock()
      } else if (selectedId.value !== '') {
        // 选中的那个不在了，或者它是别处锁定的系统人格（地址里带进来的旧
        // 链接），都清掉：下一轮不必点一个不在列表里的东西。
        const chosen = records.value.find((persona) => persona.persona_id === selectedId.value)
        if (!chosen || isSystemPersona(chosen.name)) selectedId.value = ''
      }
    } catch (cause) {
      failure.value = toFailure(cause)
      if (lock) selectedId.value = ''
    } finally {
      loading.value = false
    }
  }

  function load(force = false): Promise<void> {
    if (!enabled) return Promise.resolve()
    if (loaded && !force) return Promise.resolve()
    if (pending !== null) return pending
    pending = loadOnce().finally(() => {
      pending = null
    })
    return pending
  }

  /**
   * 发送前把锁定人格准备好。普通页面没有锁定，直接返回。
   *
   * 建号失败时不抛错：失败已经落在 `failure` 上，调用方按"人格没准备好"
   * 拒绝这一句，界面同时给重试入口。
   */
  async function ensure(): Promise<void> {
    if (!enabled || !lock || selectedId.value !== '') return
    await load()
  }

  function select(personaId: string): void {
    if (!enabled || lock) return
    selectedId.value = personaId
  }

  return { items, selectedId, loading, failure, locked, load, select, ensure }
}
