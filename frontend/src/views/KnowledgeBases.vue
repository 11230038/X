<script setup lang="ts">
/**
 * 知识库。目录（卡片）与详情。
 *
 * 三条纪律：
 * 1. `vector_state` 是**逐文档**的。一份 pending 不让整页转圈；全文检索在
 *    向量就绪前就能用，页面要把这一点说出来；
 * 2. 重建也是逐文档的，没有全库重建。失败的那一份才有「重新解析」；
 * 3. 删除知识库是不可逆的，要二次确认并说明原文件不受影响。
 *
 * 页面只保留模板；列表/详情、新建、文档动作、加入文档、检索与预览在这里
 * 由 `useKnowledgeBases` 接成一张表。
 *
 * 文档卡的左半边就是「打开详情」；「进入沉浸阅读」是卡片上的另一个动作，
 * 它用文档背后的 `file_id` 建阅读材料，不碰知识库里的索引。
 */

import { computed, ref, watch } from 'vue'
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
import { formatDateTime, formatNumber, formatPercent } from '../shared/format'

import KbDocumentPreviewDialog from './knowledge/KbDocumentPreviewDialog.vue'
import KbOperationsPanel from './knowledge/KbOperationsPanel.vue'
import { documentBadge } from './knowledge/documentKind'
import { useKnowledgeBases } from './knowledge/useKnowledgeBases'
import AppIcon from '../components/common/AppIcon.vue'

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
  enterReading,
  isEnteringReading,
  readingError,
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

/**
 * 新建知识库的弹窗。表单状态与提交仍在 `useKbCreate` 里，这里只把弹窗的开合
 * 钉在 `createOpen` 上：目录页、空态与右侧面板三处的「新建」都只是把它置真，
 * 开与关只有这一个真相。
 */
const createDialog = ref<HTMLDialogElement | null>(null)

watch(createOpen, (open) => {
  const dialog = createDialog.value
  if (!dialog) return
  if (open && !dialog.open) dialog.showModal()
  if (!open && dialog.open) dialog.close()
})

/**
 * 标题与副标题：详情到了用详情，详情还在路上时先用列表里的那一行。只等详情
 * 的话，标题会先写成"知识库"、再改成名字，跳一下。
 */
const headerBase = computed(
  () =>
    currentBase.value ?? bases.value.find((base) => base.kb_id === selectedId.value) ?? null,
)

const headerTitle = computed(() =>
  headerBase.value ? KNOWLEDGE.titleFor(headerBase.value.name) : KNOWLEDGE.title,
)

const headerDescription = computed(() => {
  if (!headerBase.value) return KNOWLEDGE.description
  return headerBase.value.description || KNOWLEDGE.documentsSubtitle
})
</script>



<template>
  <SurfaceGate surface="knowledge_base">
    <AppShell>
      <div class="page">
        <div class="page__inner">
          <button
            v-if="selectedId"
            type="button"
            class="kb-back"
            @click="router.push({ name: 'knowledge-bases' })"
          >
            ← {{ KNOWLEDGE.allBases }}
          </button>

          <PageHeader :title="headerTitle" :description="headerDescription" />

          <!--
            知识库目录：一张卡片一个库，点卡片进详情。演示稿没有「知识库列表」
            这一层，卡片照「资料市场」的卡片做。
          -->
          <template v-if="!selectedId">
            <SkeletonBlock
              v-if="listStatus === 'loading' && bases.length === 0"
              :lines="4"
              :line-height="22"
            />
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
            <div v-else class="kb-grid">
              <button
                v-for="base in bases"
                :key="base.kb_id"
                type="button"
                class="kb-card"
                @click="openBase(base.kb_id)"
              >
                <span class="kb-card__top">
                  <span class="kb-card__icon" aria-hidden="true"><AppIcon name="knowledge" /></span>
                  <span class="kb-card__name" :title="base.name">{{ base.name }}</span>
                  <StatusBadge
                    v-if="base.is_default"
                    class="kb-card__default"
                    :label="KNOWLEDGE.defaultBadge"
                    tone="accent"
                    small
                  />
                </span>
                <span v-if="base.description" class="kb-card__desc">
                  {{ base.description }}
                </span>
                <span class="kb-card__meta">
                  {{ KNOWLEDGE.documentCount(base.documents, base.ready_documents) }}
                </span>
                <span class="kb-card__time">更新于 {{ formatDateTime(base.updated_at) }}</span>
              </button>
            </div>
          </template>

          <template v-else>
            <SkeletonBlock v-if="detailStatus === 'loading'" :lines="7" :line-height="16" />
            <FailureState
              v-else-if="detailStatus === 'failed' && detailFailure"
              title="知识库没能加载出来"
              :failure="detailFailure"
              :exit-label="detailFailure.code === 'not_found' ? '回到知识库列表' : ''"
              @retry="retryDetail"
              @exit="router.push({ name: 'knowledge-bases' })"
            />

            <template v-else-if="currentBase">
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

              <div class="kb-summary">
                <StatusBadge
                  v-if="currentBase.is_default"
                  :label="KNOWLEDGE.defaultBadge"
                  tone="accent"
                  small
                />
                <span class="kb-summary__meta">
                  {{ KNOWLEDGE.documentCount(currentBase.documents, currentBase.ready_documents) }}
                  · 更新于 {{ formatDateTime(currentBase.updated_at) }}
                </span>
              </div>

              <!-- 加入文档的面板就开在按钮下面一屏内，不从顶部跳到最底下。 -->
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

              <!-- 检索在上：先问「能不能搜到」，再往下看是哪些文档。 -->
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

              <section class="section">
                <h3 class="section-title">{{ KNOWLEDGE.documents }}</h3>

                <EmptyState
                  v-if="currentDocuments.length === 0"
                  :title="KNOWLEDGE.emptyDocuments"
                  :hint="KNOWLEDGE.emptyDocumentsHint"
                  :action-label="KNOWLEDGE.addDocument"
                  @action="openAdd"
                />
                <ul v-else class="document-list">
                  <li v-for="document in currentDocuments" :key="document.document_id" class="document">
                    <!--
                      整张卡的左半边就是「打开详情」：点文件名、点类型牌都算。
                      右侧的动作是同级按钮，不会被这一次点击带走。
                    -->
                    <button
                      type="button"
                      class="document__open"
                      :aria-label="`${KNOWLEDGE.preview}：${document.filename}`"
                      @click="openPreview(document)"
                    >
                      <span
                        class="document__icon"
                        :class="`document__icon--${documentBadge(document.filename).kind}`"
                        aria-hidden="true"
                      >
                        {{ documentBadge(document.filename).label }}
                      </span>
                      <span class="document__main">
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
                        <span v-if="readingError(document)" class="document__error">
                          {{ readingError(document) }}
                        </span>
                      </span>
                    </button>
                    <div class="document__side">
                      <StatusBadge
                        :label="vectorLabel(document.vector_state)"
                        :tone="vectorTone(document.vector_state)"
                        small
                      />
                      <button
                        type="button"
                        class="button"
                        :disabled="isEnteringReading(document.document_id)"
                        @click="enterReading(document)"
                      >
                        {{
                          isEnteringReading(document.document_id)
                            ? KNOWLEDGE.enterReadingBusy
                            : KNOWLEDGE.enterReading
                        }}
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
            </template>
          </template>
        </div>
      </div>

      <template #panel="{ collapsed, toggle }">
        <KbOperationsPanel
          :collapsed="collapsed"
          :mode="selectedId ? 'detail' : 'directory'"
          :is-default="currentBase ? currentBase.is_default : null"
          :busy="actionBusy || (selectedId !== null && currentBase === null)"
          @toggle="toggle"
          @create="openCreate"
          @add="openAdd"
          @set-default="setDefault"
          @remove="deleteBase"
        />
      </template>
    </AppShell>

    <!--
      新建知识库是弹窗，不是页面里的一张卡片：目录、空态与右侧面板三处开的是
      同一个，表单不再把下面的内容顶下去；关掉就回到原处。
    -->
    <dialog ref="createDialog" class="kb-dialog" @cancel.prevent="closeCreate">
      <form class="kb-dialog__form" @submit.prevent="submitCreate">
        <h2 class="kb-dialog__title">{{ KNOWLEDGE.createTitle }}</h2>
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
        <div class="kb-dialog__actions">
          <button type="submit" class="button button--primary" :disabled="createBusy">
            {{ createBusy ? '正在创建…' : KNOWLEDGE.createAction }}
          </button>
          <button type="button" class="button" @click="closeCreate">
            {{ KNOWLEDGE.cancel }}
          </button>
        </div>
      </form>
    </dialog>

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
