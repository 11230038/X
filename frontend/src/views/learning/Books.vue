<script setup lang="ts">
/**
 * 活书书架。
 *
 * 这一页只回答两个问题：我有哪些书，以及每本读到哪了。新建放在这里，删除
 * 放在详情页 —— 在一张卡片上删一本正在读的书，比在一个已经有返回按钮的
 * 页面上删更容易点错。
 *
 * 卡片上的"几章已生成"来自列表接口的聚合计数，不来自大纲：书架不应该为了
 * 显示一个进度数字去拉每一章。
 */

import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import { listBooks, type BookDetail, type BookSummary } from '../../api/books'
import { canSurface } from '../../auth/permissions'
import EmptyState from '../../components/common/EmptyState.vue'
import FailureState from '../../components/common/FailureState.vue'
import PartialBanner from '../../components/common/PartialBanner.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import StatusBadge from '../../components/common/StatusBadge.vue'
import SurfaceGate from '../../components/common/SurfaceGate.vue'
import PageHeader from '../../components/shell/PageHeader.vue'
import { BOOKS } from '../../copy/books'
import { formatRelative } from '../../shared/format'
import { useResource } from '../../shared/resource'

import BookCreateDialog from './books/BookCreateDialog.vue'

const router = useRouter()

const { status, data, failure, stale, load } = useResource(listBooks)
const books = computed<BookSummary[]>(() => data.value?.books ?? [])

const createDialog = ref<InstanceType<typeof BookCreateDialog> | null>(null)

function openCreate(): void {
  createDialog.value?.show()
}

function openBook(book: BookSummary): void {
  void router.push({ name: 'book', params: { bookId: book.book_id } })
}

function onCreated(created: BookDetail): void {
  // 创建成功后直接进详情：下一步永远是"生成大纲"。
  void router.push({ name: 'book', params: { bookId: created.book_id } })
}

onMounted(() => {
  if (canSurface('book') === false) return
  void load()
})
</script>

<template>
  <SurfaceGate surface="book">
    <PageHeader :title="BOOKS.title" :description="BOOKS.description">
      <template #actions>
        <button type="button" class="book-ghost" @click="router.push({ name: 'files' })">
          {{ BOOKS.emptySecondary }}
        </button>
        <button type="button" class="book-primary" @click="openCreate">
          {{ BOOKS.newBook }}
        </button>
      </template>
    </PageHeader>

    <PartialBanner
      v-if="stale && books.length > 0"
      :failure="failure"
      :text="BOOKS.refreshFailedKept"
      @retry="load"
    />

    <SkeletonBlock
      v-if="status === 'loading' && books.length === 0"
      :lines="4"
      :line-height="54"
    />

    <FailureState
      v-else-if="status === 'failed' && failure"
      :failure="failure"
      :title="BOOKS.loadFailed"
      @retry="load"
    />

    <EmptyState
      v-else-if="books.length === 0"
      :title="BOOKS.emptyTitle"
      :hint="BOOKS.emptyHint"
      :action-label="BOOKS.emptyAction"
      :secondary-label="BOOKS.emptySecondary"
      @action="openCreate"
      @secondary="router.push({ name: 'files' })"
    />

    <template v-else>
      <div class="book-meta">{{ BOOKS.count(books.length) }}</div>

      <ul class="shelf">
        <li v-for="book in books" :key="book.book_id">
          <button type="button" class="card" @click="openBook(book)">
            <span class="card__head">
              <span class="card__title">{{ book.title }}</span>
              <span class="card__chevron" aria-hidden="true">→</span>
            </span>

            <span class="card__source">
              {{ BOOKS.sourceOf(BOOKS.sourceLabel(book.source_kind), book.source_title) }}
            </span>

            <span class="card__stats">
              <StatusBadge
                :label="BOOKS.chapters(book.ready_chapters, book.chapter_count)"
                :tone="book.ready_chapters > 0 ? 'accent' : 'neutral'"
                small
              />
              <StatusBadge
                v-if="book.outline_confirmed"
                :label="BOOKS.outlineConfirmed"
                tone="success"
                small
              />
              <span class="card__progress">
                {{
                  book.last_ordinal > 0
                    ? BOOKS.progressAt(book.last_ordinal)
                    : BOOKS.notStarted
                }}
              </span>
            </span>

            <span class="card__date">{{ BOOKS.updatedAt(formatRelative(book.updated_at)) }}</span>
          </button>
        </li>
      </ul>
    </template>

    <BookCreateDialog ref="createDialog" @created="onCreated" />
  </SurfaceGate>
</template>

<style scoped src="./books/books.css"></style>
<style scoped>
.shelf {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
  gap: 12px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.card {
  display: flex;
  flex-direction: column;
  gap: 7px;
  width: 100%;
  height: 100%;
  padding: 13px 15px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
  text-align: left;
  cursor: pointer;
}

.card:hover {
  border-color: var(--accent);
}

.card__head {
  display: flex;
  gap: 8px;
  align-items: flex-start;
  justify-content: space-between;
}

.card__title {
  overflow: hidden;
  color: var(--text);
  font-size: 14px;
  font-weight: 650;
  line-height: 1.4;
  text-overflow: ellipsis;
}

.card__chevron {
  flex: none;
  color: var(--text-faint);
  font-size: 12px;
}

.card:hover .card__chevron {
  color: var(--accent);
}

.card__source {
  overflow: hidden;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.5;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.card__stats {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
}

.card__progress {
  color: var(--text-muted);
  font-size: 11px;
}

.card__date {
  margin-top: auto;
  color: var(--text-faint);
  font-size: 10px;
}
</style>
