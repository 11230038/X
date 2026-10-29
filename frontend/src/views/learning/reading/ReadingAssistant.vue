<script setup lang="ts">
/**
 * 文档问答助手。
 *
 * 一问是正文里的一段选区，所以每条记录都把"问的是哪一段"原样留在面板里：
 * 用户回头看得见自己当时选了什么，答案也就有了依据。
 */

import { describeFailure } from '../../../api/errors'
import MarkdownBody from '../../../components/common/MarkdownBody.vue'
import { READING } from '../../../copy/reading'
import type { AssistantExchange } from './useReadingAssistant'
import AppIcon from '../../../components/common/AppIcon.vue'

defineProps<{
  /** 当前打开的材料名。没有材料时是空串。 */
  filename: string
  exchanges: AssistantExchange[]
}>()
</script>

<template>
  <aside class="assistant">
    <h2 class="assistant__title">
      <AppIcon name="bulb" />
      {{ READING.assistantTitle }}
    </h2>

    <section v-if="filename" class="doc">
      <p class="doc__label">
        <AppIcon name="pin" />
        {{ READING.assistantDocument }}
      </p>
      <p class="doc__name">{{ filename }}</p>
    </section>

    <p v-if="!filename" class="assistant__hint">{{ READING.assistantNoMaterial }}</p>
    <p v-else-if="exchanges.length === 0" class="assistant__hint">{{ READING.assistantEmpty }}</p>

    <ol v-else class="exchanges">
      <li v-for="exchange in exchanges" :key="exchange.id" class="exchange">
        <p class="exchange__label exchange__label--ask">{{ READING.assistantQuestion }}</p>
        <blockquote class="exchange__question">{{ exchange.question }}</blockquote>

        <p class="exchange__label">{{ READING.assistantAnswer }}</p>
        <p v-if="exchange.status === 'asking'" class="exchange__pending">
          {{ READING.assistantAsking }}
        </p>
        <p v-else-if="exchange.status === 'failed'" class="exchange__failed">
          {{ READING.assistantFailed }}：{{
            exchange.failure ? describeFailure(exchange.failure) : ''
          }}
        </p>
        <div v-else class="exchange__answer">
          <MarkdownBody :text="exchange.answer" complete />
        </div>
      </li>
    </ol>
  </aside>
</template>

<style scoped src="./assistant.css"></style>
