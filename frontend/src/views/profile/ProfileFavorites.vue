<script setup lang="ts">
/**
 * 收藏夹。版式照演示稿的 `#page-favorites`：一排三列卡片，每张写着收藏的
 * 内容、它来自哪里、以及什么时候收的。
 *
 * 收藏在这个版本有两种真实来源：
 * 1. 沉浸阅读的书签（`GET /reading/bookmarks`），原文由服务端按坐标回读，
 *    卡片上是「书籍片段」；
 * 2. 题库里标了收藏的题目（`GET /questions?bookmarked=true`），卡片上是
 *    「题目」，点进去在题库里打开那一道。
 *
 * 演示稿的「对话灵感 / 知识卡片」两类没有收藏接口，不画假分类。两类来源
 * 各自读、各自失败：一边读不出来时另一边照常显示，顶上说明缺的是哪一边。
 *
 * 这一页挂在个人中心下面：入口是个人中心那张「我的收藏」卡，打开之后左侧
 * 仍是个人中心那一列（个人资料 / 收藏夹 / 账号与安全），不再另画演示稿的
 * 「收藏分类」—— 分类只对应上面两类，列出来是多余的一列假按钮。
 */

import { computed, onMounted, ref } from 'vue'
import { RouterLink, useRouter, type RouteLocationRaw } from 'vue-router'

import type { ApiFailure } from '../../api/client'
import { listQuestions, type QuestionEntry } from '../../api/questions'
import { listBookmarks, type ReadingBookmark } from '../../api/reading'
import { canSurface, surfaceDecision } from '../../auth/permissions'
import EmptyState from '../../components/common/EmptyState.vue'
import FailureState from '../../components/common/FailureState.vue'
import PartialBanner from '../../components/common/PartialBanner.vue'
import PermissionDenied from '../../components/common/PermissionDenied.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import AppShell from '../../components/shell/AppShell.vue'
import PageHeader from '../../components/shell/PageHeader.vue'
import { PROFILE } from '../../copy/profile'
import { formatRelative } from '../../shared/format'
import { toFailure } from '../../shared/resource'
import AppIcon from '../../components/common/AppIcon.vue'

type Status = 'idle' | 'loading' | 'ready' | 'failed'

/** 一张卡片，来自哪一类收藏由 `kind` 决定标签与去处。 */
interface FavoriteCard {
  key: string
  kind: 'reading' | 'question'
  text: string
  /** 书籍片段是引文，加引号；题目不加。 */
  quoted: boolean
  source: string
  at: string
  to: RouteLocationRaw
}

const router = useRouter()

const readingAllowed = computed(() => canSurface('reading') !== false)
const questionsAllowed = computed(() => canSurface('notebook') !== false)
const allowed = computed(() => readingAllowed.value || questionsAllowed.value)
const denied = computed(() => surfaceDecision('reading'))

const cards = ref<FavoriteCard[]>([])
const status = ref<Status>('idle')
const failure = ref<ApiFailure | null>(null)
const readingFailure = ref<ApiFailure | null>(null)
const questionsFailure = ref<ApiFailure | null>(null)

function readingCard(bookmark: ReadingBookmark): FavoriteCard {
  const locator = bookmark.mark.locator
  const excerpt = bookmark.text?.trim() ?? ''
  return {
    key: `reading:${bookmark.mark.mark_id}`,
    kind: 'reading',
    text: excerpt === '' ? PROFILE.favoritesSnippet(locator.ordinal) : excerpt,
    quoted: excerpt !== '',
    source: bookmark.material.filename || PROFILE.favoritesUnknownMaterial,
    at: bookmark.mark.created_at,
    // 阅读页认这四个参数，与批注、引用深链用的是同一套坐标。
    to: {
      name: 'reading',
      query: {
        material: bookmark.mark.material_id,
        ordinal: String(locator.ordinal),
        start: String(locator.char_start),
        end: String(locator.char_end),
      },
    },
  }
}

function questionCard(entry: QuestionEntry): FavoriteCard {
  return {
    key: `question:${entry.question.question_id}`,
    kind: 'question',
    text: entry.question.text,
    quoted: false,
    source: entry.question.source || PROFILE.favoritesQuestionSource,
    at: entry.answer.updated_at || entry.created_at,
    // 题库页按地址上的 `question=` 打开那一道。
    to: { name: 'questions', query: { question: entry.question.question_id } },
  }
}

async function readingCards(): Promise<FavoriteCard[]> {
  try {
    return (await listBookmarks()).bookmarks.map(readingCard)
  } catch (cause) {
    readingFailure.value = toFailure(cause)
    return []
  }
}

async function questionCards(): Promise<FavoriteCard[]> {
  try {
    // 收藏的题按收藏时间排，服务端按更新时间给；一页足够画出收藏夹，
    // 没有"更多"分页 —— 几十道之外仍可在题库里按收藏筛选查看。
    const listed = await listQuestions({ bookmarked: true, limit: 50 })
    return listed.questions.map(questionCard)
  } catch (cause) {
    questionsFailure.value = toFailure(cause)
    return []
  }
}

function newestFirst(a: FavoriteCard, b: FavoriteCard): number {
  const left = Date.parse(a.at)
  const right = Date.parse(b.at)
  if (Number.isNaN(left) && Number.isNaN(right)) return 0
  if (Number.isNaN(left)) return 1
  if (Number.isNaN(right)) return -1
  return right - left
}

async function load(): Promise<void> {
  status.value = 'loading'
  readingFailure.value = null
  questionsFailure.value = null
  const [reading, questions] = await Promise.all([
    readingAllowed.value ? readingCards() : Promise.resolve([]),
    questionsAllowed.value ? questionCards() : Promise.resolve([]),
  ])
  cards.value = [...reading, ...questions].sort(newestFirst)
  if (readingFailure.value && questionsFailure.value) {
    failure.value = readingFailure.value
    status.value = 'failed'
    return
  }
  failure.value = null
  status.value = 'ready'
}

function goBack(): void {
  if (window.history.length > 1) {
    router.back()
  } else {
    void router.push({ name: 'profile' })
  }
}

onMounted(() => {
  // 两类来源都明确关掉时这一读注定 403：交给权限门说，不发请求。
  if (!allowed.value) return
  void load()
})
</script>

<template>
  <AppShell>
    <PermissionDenied
      v-if="!allowed"
      surface="reading"
      :layer="denied?.layer"
      :reason="denied?.reason"
      @back="goBack"
    />

    <div v-else class="page">
      <div class="page__inner">
        <PageHeader
          :title="PROFILE.favoritesTitle"
          :description="PROFILE.favoritesDescription"
        />

        <FailureState
          v-if="status === 'failed' && failure"
          :failure="failure"
          :title="PROFILE.favoritesFailed"
          @retry="load"
        />

        <SkeletonBlock v-else-if="status === 'loading'" :lines="6" :line-height="64" />

        <template v-else>
          <!-- 一边读不出来、另一边还在：页面照常可用，只说缺的是哪一边。 -->
          <PartialBanner
            v-if="readingFailure"
            :failure="readingFailure"
            :text="PROFILE.favoritesReadingFailed"
            @retry="load"
          />
          <PartialBanner
            v-if="questionsFailure"
            :failure="questionsFailure"
            :text="PROFILE.favoritesQuestionsFailed"
            @retry="load"
          />

          <EmptyState
            v-if="cards.length === 0"
            :title="PROFILE.favoritesEmpty"
            :hint="PROFILE.favoritesEmptyHint"
            :action-label="PROFILE.favoritesGoReading"
            :secondary-label="PROFILE.favoritesGoQuestions"
            @action="router.push('/reading')"
            @secondary="router.push('/questions')"
          />

          <div v-else class="favorites">
            <RouterLink
              v-for="card in cards"
              :key="card.key"
              class="fav-card"
              :to="card.to"
            >
              <header class="fav-card__head">
                <span class="fav-card__type" :class="`fav-card__type--${card.kind}`">
                  <AppIcon :name="card.kind === 'reading' ? 'space' : 'quiz'" />
                  {{ card.kind === 'reading' ? PROFILE.favoritesKind : PROFILE.favoritesQuestionKind }}
                </span>
                <span class="fav-card__icon" aria-hidden="true"><AppIcon name="star" /></span>
              </header>
              <p
                class="fav-card__text"
                :class="{ 'fav-card__text--plain': !card.quoted }"
              >
                <template v-if="card.quoted">“{{ card.text }}”</template>
                <template v-else>{{ card.text }}</template>
              </p>
              <footer class="fav-card__foot">
                <span class="fav-card__file">
                  {{ PROFILE.favoritesFrom }}：{{ card.source }}
                </span>
                <time :datetime="card.at">{{ formatRelative(card.at) }}</time>
              </footer>
            </RouterLink>
          </div>
        </template>
      </div>
    </div>
  </AppShell>
</template>

<style scoped>
/*
 * 演示稿 `.fav-masonry` / `.fav-card`：三列、20px 间距、悬停抬起来。
 *
 * 卡片**一样大**：同一行里由网格拉伸到同一高度，文字区再固定成四行高。
 * 这样短句不缩、长句也不撑，页脚总落在同一位置，三列排下来是齐的。
 */
.favorites {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 20px;
}

.fav-card {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: 24px;
  border: 1px solid rgb(255 255 255 / 80%);
  border-radius: 20px;
  background: #fff;
  box-shadow: 0 8px 24px -8px rgb(51 68 85 / 8%);
  color: var(--text);
  text-decoration: none;
  transition: transform 0.3s cubic-bezier(0.34, 1.56, 0.64, 1), box-shadow 0.3s ease;
}

.fav-card:hover {
  transform: translateY(-6px);
  box-shadow: 0 20px 40px -10px rgb(51 68 85 / 15%);
}

.fav-card__head {
  display: flex;
  gap: 12px;
  align-items: center;
  justify-content: space-between;
}

/* 演示稿的三种类型标签。这里只有书籍片段（黄）与题目（蓝）两类。 */
.fav-card__type {
  padding: 4px 10px;
  border-radius: 8px;
  font-size: 12px;
  font-weight: 700;
}

.fav-card__type--reading {
  background: #fff9e6;
  color: #d4a017;
}

.fav-card__type--question {
  background: #e8faff;
  color: #0099cc;
}

.fav-card__icon {
  font-size: 20px;
  opacity: 0.8;
}

/*
 * 内容最多四行，再长就收起来：卡片是入口，完整的一段在阅读页/题库里读。
 * 不足四行也留出四行的高度，卡片高度才一致。
 */
.fav-card__text {
  display: -webkit-box;
  overflow: hidden;
  min-height: calc(1.6em * 4);
  margin: 0;
  color: var(--text);
  font-size: 15px;
  font-weight: 600;
  line-height: 1.6;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 4;
}

.fav-card__text--plain {
  color: var(--text-muted);
  font-weight: 500;
}

.fav-card__foot {
  display: flex;
  gap: 12px;
  justify-content: space-between;
  color: var(--text-muted);
  font-size: 12px;
}

.fav-card__file {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.fav-card__foot time {
  flex: none;
}
</style>
