<script setup lang="ts">
/**
 * 知识库。列表栏 + 详情。
 *
 * 三条纪律：
 * 1. `vector_state` 是**逐文档**的。一份 pending 不让整页转圈；全文检索在
 *    向量就绪前就能用，页面要把这一点说出来；
 * 2. 重建也是逐文档的，没有全库重建。失败的那一份才有「重新解析」；
 * 3. 删除知识库是不可逆的，要二次确认并说明原文件不受影响。
 *
 * 页面只保留模板；列表/详情、新建、文档动作、加入文档、检索与预览在这里
 * 由 `useKnowledgeBases` 接成一张表。
 */

import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'

import EmptyState from '../components/common/EmptyState.vue'
import FailureState from '../components/common/FailureState.vue'
import PartialBanner from '../components/common/PartialBanner.vue'
import SkeletonBlock from '../components/common/SkeletonBlock.vue'
import StatusBadge from '../components/common/StatusBadge.vue'
import SurfaceGate from '../components/common/SurfaceGate.vue'
import AppShell from '../components/shell/AppShell.vue'
import PageHeader from '../components/shell/PageHeader.vue'
import { KNOWLEDGE } from '../copy/knowledge'
import { formatDateTime, formatNumber, formatPercent, truncate } from '../shared/format'

import KbDocumentPreviewDialog from './knowledge/KbDocumentPreviewDialog.vue'
import { useKnowledgeBases } from './knowledge/useKnowledgeBases'

const props = defineProps<{ kbId?: string }>()
const router = useRouter()

const selectedId = computed(() => props.kbId ?? null)

/**
 * 弹窗实例由页面持有：`ref` 是模板里的那一个，怎么打开、怎么收起由组装层决定。
 */
const previewDialog = ref<InstanceType<typeof KbDocumentPreviewDialog> | null>(null)

const {
  bases,
  listStatus,
  listFailure,
  loadList,
  openBase,
  detailStatus,
  detailFailure,
  pollFailure,
  retryDetail,
  refreshDetail,
  currentBase,
  currentDocuments,
  partialVectors,
  createOpen,
  createName,
  createDescription,
  createBusy,
  createError,
  openCreate,
  closeCreate,
  submitCreate,
  actionBusy,
  isRebuilding,
  setDefault,
  deleteBase,
  rebuild,
  removeDocument,
  previewDocument,
  previewText,
  previewStatus,
  previewFailure,
  previewLoadingMore,
  previewRetry,
  previewLoadMore,
  openPreview,
  closePreview,
  addOpen,
  fileOptions,
  addFileId,
  addBusy,
  addFailure,
  resetAdd,
  openAdd,
  submitAdd,
  searchQuery,
  searchBusy,
  searchResults,
  searchFailure,
  searchedFor,
  runSearch,
  vectorLabel,
  vectorTone,
  locatorLabel,
} = useKnowledgeBases(selectedId, router, previewDialog)
</script>



<template>
  <SurfaceGate surface="knowledge_base">
    <AppShell>
      <template #list>
        <aside class="kb-pane">
          <div class="kb-pane__head">
            <span class="kb-pane__title">{{ KNOWLEDGE.list }}</span>
            <button type="button" class="new-button" @click="openCreate">
              {{ KNOWLEDGE.create }}
            </button>
          </div>

          <div class="kb-pane__scroll">
            <SkeletonBlock v-if="listStatus === 'loading'" :lines="5" :line-height="18" />
            <FailureState
              v-else-if="listStatus === 'failed' && listFailure"
              title="知识库列表没能加载出来"
              :failure="listFailure"
              @retry="loadList"
            />
            <EmptyState
              v-else-if="bases.length === 0"
              :title="KNOWLEDGE.empty"
              :hint="KNOWLEDGE.emptyHint"
              :action-label="KNOWLEDGE.create"
              @action="openCreate"
            />
            <ul v-else class="kb-list">
              <li v-for="base in bases" :key="base.kb_id">
                <button
                  type="button"
                  class="kb-row"
                  :class="{ 'kb-row--active': base.kb_id === selectedId }"
                  @click="openBase(base.kb_id)"
                >
                  <span class="kb-row__head">
                    <span class="kb-row__name" :title="base.name">{{ base.name }}</span>
                    <StatusBadge v-if="base.is_default" :label="KNOWLEDGE.defaultBadge" tone="accent" small />
                  </span>
                  <span v-if="base.description" class="kb-row__description">
                    {{ truncate(base.description, 60) }}
                  </span>
                  <span class="kb-row__meta">
                    {{ KNOWLEDGE.documentCount(base.documents, base.ready_documents) }}
                  </span>
                </button>
              </li>
            </ul>
          </div>
        </aside>
      </template>

      <div class="page">
        <div class="page__inner">
          <PageHeader :title="KNOWLEDGE.title" :description="KNOWLEDGE.description">
            <template #actions>
              <button type="button" class="button" @click="openCreate">
                {{ KNOWLEDGE.create }}
              </button>
            </template>
          </PageHeader>

          <form v-if="createOpen" class="create-card" @submit.prevent="submitCreate">
            <h2 class="create-card__title">{{ KNOWLEDGE.createTitle }}</h2>
            <label class="field">
              <span class="field__label">{{ KNOWLEDGE.name }}</span>
              <input
                v-model="createName"
                class="field__input"
                type="text"
                maxlength="120"
                :placeholder="KNOWLEDGE.namePlaceholder"
              />
            </label>
            <label class="field">
              <span class="field__label">{{ KNOWLEDGE.descriptionLabel }}</span>
              <textarea
                v-model="createDescription"
                class="field__input"
                rows="2"
                :placeholder="KNOWLEDGE.descriptionPlaceholder"
              ></textarea>
            </label>
            <p v-if="createError" class="form-error" role="alert">{{ createError }}</p>
            <div class="create-card__actions">
              <button type="submit" class="button button--primary" :disabled="createBusy">
                {{ createBusy ? '正在创建…' : KNOWLEDGE.createAction }}
              </button>
              <button type="button" class="button" @click="closeCreate">
                {{ KNOWLEDGE.cancel }}
              </button>
            </div>
          </form>

          <SkeletonBlock v-if="detailStatus === 'loading'" :lines="7" :line-height="16" />
          <FailureState
            v-else-if="detailStatus === 'failed' && detailFailure"
            title="知识库没能加载出来"
            :failure="detailFailure"
            :exit-label="detailFailure.code === 'not_found' ? '回到知识库列表' : ''"
            @retry="retryDetail"
            @exit="router.push({ name: 'knowledge-bases' })"
          />
          <EmptyState
            v-else-if="!currentBase"
            :title="KNOWLEDGE.select"
            :hint="KNOWLEDGE.selectHint"
            :action-label="bases.length === 0 ? KNOWLEDGE.create : ''"
            @action="openCreate"
          />

          <template v-else>
            <PartialBanner
              v-if="pollFailure"
              :failure="pollFailure"
              text="文档状态没有继续刷新，下面是上一次的结果"
              @retry="refreshDetail"
            />
            <PartialBanner
              v-else-if="partialVectors"
              :text="KNOWLEDGE.partialVectors"
              tone="muted"
              :retryable="false"
            />

            <header class="kb-head">
              <div class="kb-head__text">
                <div class="kb-head__title-row">
                  <h2 class="kb-head__title">{{ currentBase.name }}</h2>
                  <StatusBadge
                    v-if="currentBase.is_default"
                    :label="KNOWLEDGE.defaultBadge"
                    tone="accent"
                  />
                </div>
                <p v-if="currentBase.description" class="kb-head__description">
                  {{ currentBase.description }}
                </p>
                <p class="kb-head__meta">
                  {{ KNOWLEDGE.documentCount(currentBase.documents, currentBase.ready_documents) }}
                  · 更新于 {{ formatDateTime(currentBase.updated_at) }}
                </p>
              </div>
              <div class="kb-head__actions">
                <button
                  v-if="!currentBase.is_default"
                  type="button"
                  class="button"
                  :disabled="actionBusy"
                  @click="setDefault"
                >
                  {{ KNOWLEDGE.setDefault }}
                </button>
                <button
                  type="button"
                  class="button button--danger"
                  :disabled="actionBusy"
                  @click="deleteBase"
                >
                  {{ KNOWLEDGE.delete }}
                </button>
              </div>
            </header>

            <section class="section">
              <div class="section__head">
                <h3 class="section-title">{{ KNOWLEDGE.documents }}</h3>
                <button type="button" class="button" @click="openAdd">
                  {{ KNOWLEDGE.addDocument }}
                </button>
              </div>

              <div v-if="addOpen" class="add-card">
                <template v-if="fileOptions === null">
                  <p v-if="addFailure" class="form-error" role="alert">{{ addFailure }}</p>
                  <SkeletonBlock v-else :lines="2" />
                </template>
                <template v-else-if="fileOptions.length === 0">
                  <p class="muted-note">{{ KNOWLEDGE.noFilesToAdd }}</p>
                  <RouterLink class="button" to="/files">{{ KNOWLEDGE.goToFiles }}</RouterLink>
                </template>
                <template v-else>
                  <label class="field">
                    <span class="field__label">{{ KNOWLEDGE.chooseFile }}</span>
                    <select v-model="addFileId" class="field__input">
                      <option v-for="file in fileOptions" :key="file.id" :value="file.id">
                        {{ file.filename }}
                      </option>
                    </select>
                  </label>
                  <p v-if="addFailure" class="form-error" role="alert">{{ addFailure }}</p>
                  <div class="add-card__actions">
                    <button
                      type="button"
                      class="button button--primary"
                      :disabled="addBusy || !addFileId"
                      @click="submitAdd"
                    >
                      {{ addBusy ? '正在加入…' : KNOWLEDGE.addDocumentAction }}
                    </button>
                    <button type="button" class="button" @click="resetAdd">
                      {{ KNOWLEDGE.cancel }}
                    </button>
                  </div>
                </template>
              </div>

              <EmptyState
                v-if="currentDocuments.length === 0"
                :title="KNOWLEDGE.emptyDocuments"
                :hint="KNOWLEDGE.emptyDocumentsHint"
                :action-label="KNOWLEDGE.addDocument"
                @action="openAdd"
              />
              <ul v-else class="document-list">
                <li v-for="document in currentDocuments" :key="document.document_id" class="document">
                  <div class="document__main">
                    <span class="document__name" :title="document.filename">
                      {{ document.filename }}
                    </span>
                    <span class="document__meta">
                      {{ document.engine }} · {{ formatNumber(document.text_chars) }} 字 ·
                      {{ formatDateTime(document.updated_at) }}
                    </span>
                    <span v-if="document.vector_error" class="document__error">
                      {{ document.vector_error }}
                    </span>
                  </div>
                  <div class="document__side">
                    <StatusBadge
                      :label="vectorLabel(document.vector_state)"
                      :tone="vectorTone(document.vector_state)"
                      small
                    />
                    <button
                      type="button"
                      class="link-button"
                      @click="openPreview(document)"
                    >
                      {{ KNOWLEDGE.preview }}
                    </button>
                    <button
                      v-if="document.vector_state === 'failed'"
                      type="button"
                      class="link-button"
                      :disabled="isRebuilding(document.document_id)"
                      @click="rebuild(document)"
                    >
                      {{ isRebuilding(document.document_id) ? '正在重试…' : KNOWLEDGE.rebuild }}
                    </button>
                    <button
                      type="button"
                      class="link-button link-button--danger"
                      :disabled="actionBusy"
                      @click="removeDocument(document)"
                    >
                      {{ KNOWLEDGE.removeDocument }}
                    </button>
                  </div>
                </li>
              </ul>
            </section>

            <section class="section">
              <h3 class="section-title">{{ KNOWLEDGE.search }}</h3>
              <form class="search-form" @submit.prevent="runSearch">
                <input
                  v-model="searchQuery"
                  class="search-form__input"
                  type="search"
                  :placeholder="KNOWLEDGE.searchPlaceholder"
                  :aria-label="KNOWLEDGE.searchPlaceholder"
                />
                <button type="submit" class="button button--primary" :disabled="searchBusy">
                  {{ searchBusy ? '正在检索…' : KNOWLEDGE.searchAction }}
                </button>
              </form>
              <p v-if="searchFailure" class="form-error" role="alert">{{ searchFailure }}</p>

              <template v-if="searchResults !== null">
                <EmptyState
                  v-if="searchResults.length === 0"
                  :title="KNOWLEDGE.searchEmpty"
                  :hint="KNOWLEDGE.searchEmptyHint"
                />
                <ol v-else class="source-list">
                  <li v-for="source in searchResults" :key="source.index" class="source">
                    <div class="source__head">
                      <span class="source__title">{{ source.title }}</span>
                      <span class="source__score">
                        {{ KNOWLEDGE.score }} {{ formatPercent(source.score, 0) }}
                      </span>
                    </div>
                    <p class="source__snippet">{{ source.snippet }}</p>
                    <p class="source__locator">
                      {{ locatorLabel(source) }}
                      <template v-if="searchedFor"> · 检索词「{{ searchedFor }}」</template>
                    </p>
                  </li>
                </ol>
              </template>
            </section>
          </template>
        </div>
      </div>
    </AppShell>

    <KbDocumentPreviewDialog
      ref="previewDialog"
      :document="previewDocument"
      :text="previewText"
      :status="previewStatus"
      :failure="previewFailure"
      :loading-more="previewLoadingMore"
      @close="closePreview"
      @retry="previewRetry"
      @load-more="previewLoadMore"
    />
  </SurfaceGate>
</template>

<style scoped src="./KnowledgeBases.css"></style>
