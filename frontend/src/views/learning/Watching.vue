<script setup lang="ts">
/**
 * 视频学习。手动输入关键词搜 B 站，选中一条后在当前页用官方播放器观看。
 *
 * 三条规则：
 *
 * 1. **首屏不发请求。** 没有推荐、没有热门、不从课程或材料里自动取词。用户
 *    不提交，页面就停在搜索框上。
 * 2. **输入框存草稿，实际搜索用 `appliedQuery`。** 重试和翻页都基于上一次
 *    真正提交的词，不是用户正在改的那一行。
 * 3. **再搜一次失败不换掉已经读到的结果。** 旧列表留在原处，顶部挂一条部分
 *    失败条；只有第一次就失败时才画整页的失败态。
 */

import { computed, ref } from 'vue'

import type { ApiFailure } from '../../api/client'
import { searchVideos, type VideoSearchItem } from '../../api/watching'
import EmptyState from '../../components/common/EmptyState.vue'
import FailureState from '../../components/common/FailureState.vue'
import LoadMore from '../../components/common/LoadMore.vue'
import PartialBanner from '../../components/common/PartialBanner.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import SurfaceGate from '../../components/common/SurfaceGate.vue'
import PageHeader from '../../components/shell/PageHeader.vue'
import { WATCHING } from '../../copy/watching'
import { formatClock, formatNumber } from '../../shared/format'
import { toFailure } from '../../shared/resource'

type LoadStatus = 'idle' | 'loading' | 'ready' | 'failed'

const input = ref<HTMLInputElement | null>(null)
/** 用户正在输入的那一行。 */
const draft = ref('')
/** 上一次真正提交的词。重试用它。 */
const appliedQuery = ref('')
/** 当前这一批结果属于哪个词。翻页用它——再搜失败时结果还是上一个词的。 */
const loadedQuery = ref('')

const status = ref<LoadStatus>('idle')
const items = ref<VideoSearchItem[]>([])
const page = ref(1)
const hasMore = ref(false)
const failure = ref<ApiFailure | null>(null)

const selectedId = ref('')
const selected = computed(
  () => items.value.find((item) => item.video_id === selectedId.value) ?? null,
)

const loadingMore = ref(false)
const loadFailure = ref<ApiFailure | null>(null)

const canSearch = computed(
  () => draft.value.trim() !== '' && status.value !== 'loading' && !loadingMore.value,
)

function submitSearch(): void {
  if (!canSearch.value) return
  appliedQuery.value = draft.value.trim()
  void runSearch()
}

async function runSearch(): Promise<void> {
  status.value = 'loading'
  failure.value = null
  loadFailure.value = null
  loadingMore.value = false
  try {
    const result = await searchVideos(appliedQuery.value, 1)
    items.value = result.items
    page.value = result.page
    hasMore.value = result.has_more
    loadedQuery.value = appliedQuery.value
    selectedId.value = result.items[0]?.video_id ?? ''
    status.value = 'ready'
  } catch (cause) {
    failure.value = toFailure(cause)
    // 已经有上一次的结果时，这一次失败只是"没更新"。
    status.value = items.value.length > 0 ? 'ready' : 'failed'
  }
}

async function loadMore(): Promise<void> {
  if (loadingMore.value || !hasMore.value || status.value !== 'ready') return
  loadingMore.value = true
  loadFailure.value = null
  try {
    const result = await searchVideos(loadedQuery.value, page.value + 1)
    items.value = [...items.value, ...result.items]
    page.value = result.page
    hasMore.value = result.has_more
  } catch (cause) {
    loadFailure.value = toFailure(cause)
  } finally {
    loadingMore.value = false
  }
}

/** 空结果时"换个关键词"：清掉输入并聚焦，不做任何自动搜索。 */
function restartSearch(): void {
  draft.value = ''
  input.value?.focus()
}

/** 一行元信息：只连非空的段，避免出现开头的分隔符。 */
function metaLine(item: VideoSearchItem): string {
  return [item.author, formatClock(item.duration_seconds), `${formatNumber(item.play_count)} 播放`]
    .filter((part) => part !== '')
    .join(' · ')
}
</script>

<template>
  <SurfaceGate surface="watching">
    <PageHeader :title="WATCHING.title" :description="WATCHING.description" />

    <form class="search" @submit.prevent="submitSearch">
      <input
        ref="input"
        v-model="draft"
        class="search__input"
        type="search"
        maxlength="100"
        :placeholder="WATCHING.searchPlaceholder"
        :aria-label="WATCHING.searchPlaceholder"
      />
      <button type="submit" class="search__button" :disabled="!canSearch">
        {{ status === 'loading' ? WATCHING.searching : WATCHING.searchAction }}
      </button>
    </form>

    <SkeletonBlock
      v-if="status === 'loading' && items.length === 0"
      :lines="6"
      :line-height="40"
    />

    <FailureState
      v-else-if="status === 'failed' && failure"
      :failure="failure"
      :title="WATCHING.loadFailedTitle"
      @retry="runSearch"
    />

    <EmptyState
      v-else-if="status === 'ready' && items.length === 0"
      :title="WATCHING.emptyTitle"
      :hint="WATCHING.emptyHint"
      :action-label="WATCHING.emptyAction"
      @action="restartSearch"
    />

    <section v-else-if="items.length > 0" class="results">
      <PartialBanner
        v-if="failure"
        :failure="failure"
        :text="WATCHING.partialFailed"
        @retry="runSearch"
      />

      <div class="results__split">
        <div class="results__list-pane">
          <ul class="results__list">
            <li v-for="item in items" :key="item.video_id">
              <button
                type="button"
                class="result"
                :class="{ 'result--on': item.video_id === selectedId }"
                @click="selectedId = item.video_id"
              >
                <img
                  v-if="item.cover_url"
                  class="result__cover"
                  :src="item.cover_url"
                  alt=""
                  loading="lazy"
                  referrerpolicy="no-referrer"
                />
                <span v-else class="result__cover result__cover--empty" aria-hidden="true"></span>
                <span class="result__text">
                  <span class="result__title">{{ item.title }}</span>
                  <span class="result__meta">{{ metaLine(item) }}</span>
                </span>
              </button>
            </li>
          </ul>
          <LoadMore
            v-if="status === 'ready'"
            :loading="loadingMore"
            :exhausted="!hasMore"
            :failure="loadFailure"
            :label="WATCHING.loadMore"
            @load="loadMore"
          />
        </div>

        <aside class="player">
          <template v-if="selected">
            <div class="player__frame">
              <iframe
                :key="selected.video_id"
                class="player__iframe"
                :src="selected.embed_url"
                :title="selected.title"
                allowfullscreen
                loading="lazy"
                referrerpolicy="no-referrer-when-downgrade"
              ></iframe>
            </div>
            <div class="player__meta">
              <p class="player__title">{{ selected.title }}</p>
              <p class="player__by">{{ selected.author }}</p>
              <a
                class="player__link"
                :href="selected.watch_url"
                target="_blank"
                rel="noopener noreferrer"
              >
                {{ WATCHING.watchOnSite }}
              </a>
            </div>
          </template>
          <p v-else class="player__empty">{{ WATCHING.playerEmpty }}</p>
        </aside>
      </div>
    </section>

    <EmptyState v-else :title="WATCHING.idleTitle" :hint="WATCHING.idleHint" />
  </SurfaceGate>
</template>

<style scoped src="./Watching.css"></style>
