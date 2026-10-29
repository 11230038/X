<script setup lang="ts">
/**
 * 个人中心。头像落在这一页，版式照演示稿的 `#page-profile`：一张渐变资料卡、
 * 一排数字、两张入口卡、一组设置行。
 *
 * 与演示稿不同的三处，都是因为后端没有对应数据：
 * 1. 演示稿的"编辑资料"没有接口，不画按钮；
 * 2. 头衔（Lv.36 …）换成本账号真实的角色；数字换成统计接口算得出的三项，
 *    取不到就画"—"（失败时整排不画），不填示意值；
 * 3. 演示稿的"错题本"没有页面也没有接口，不画；"我的收藏"有后端真有的
 *    阅读书签，落到 `/profile/favorites`。题库与练习照旧是另外两张入口。
 *
 * 账号信息、改密与退出登录留在子页"账号与安全"（`/profile/account`）——
 * 这一页只做概览。
 */

import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'

import { getPracticeStats, type PracticeStats } from '../../api/practice'
import { canSurface, isAdmin, permissionStatus } from '../../auth/permissions'
import AppShell from '../../components/shell/AppShell.vue'
import PageHeader from '../../components/shell/PageHeader.vue'
import { ADMIN } from '../../copy/admin'
import { PROFILE } from '../../copy/profile'
import { currentAccount } from '../../session'
import { formatPercent } from '../../shared/format'
import AppIcon from '../../components/common/AppIcon.vue'

type LoadStatus = 'idle' | 'loading' | 'ready' | 'failed'

/** 与练习页的"累计"同一口径：按周取满约五年，碰不到接口 400 个桶的上限。 */
const WINDOW_WEEKS = 255

const ROLE_LABELS: Record<string, string> = {
  admin: PROFILE.roleAdmin,
  user: PROFILE.roleUser,
}

const account = computed(() => currentAccount.value)
const username = computed(() => account.value?.username ?? '')
const initial = computed(() => username.value.slice(0, 1).toUpperCase())
const roleLabel = computed(() => {
  const role = account.value?.role ?? ''
  return role === '' ? '' : (ROLE_LABELS[role] ?? role)
})

// 收藏夹里同时收着阅读的段落与题库的题目，哪一边开着就还有东西可看。
const showFavorites = computed(
  () => canSurface('reading') !== false || canSurface('notebook') !== false,
)
const showQuestions = computed(() => canSurface('notebook') !== false)
const showPractice = computed(() => canSurface('practice') !== false)
const showShortcuts = computed(
  () => showFavorites.value || showQuestions.value || showPractice.value,
)

const practiceAllowed = computed(() => canSurface('practice') !== false)

/**
 * 用户管理与日志统计原来是设置里的「管理台」，现在拆成两页挂在个人中心下。
 * 与侧栏同一口径：快照没到时不画，`admin` 面被明确关掉时也不画。
 */
const showAdmin = computed(() => isAdmin.value && canSurface('admin') !== false)

const stats = ref<PracticeStats | null>(null)
const statsStatus = ref<LoadStatus>('idle')

async function loadStats(): Promise<void> {
  if (statsStatus.value === 'loading') return
  statsStatus.value = 'loading'
  try {
    const from = new Date(Date.now() - WINDOW_WEEKS * 7 * 24 * 60 * 60 * 1000)
    stats.value = await getPracticeStats({ from: from.toISOString(), bucket: 'week' })
    statsStatus.value = 'ready'
  } catch {
    // 个人中心不是统计页：取不到就整排不画，把错误留给练习页那张卡去说。
    stats.value = null
    statsStatus.value = 'failed'
  }
}

const totals = computed(() => {
  let reviews = 0
  let correct = 0
  let wrong = 0
  for (const point of stats.value?.series ?? []) {
    reviews += point.reviews
    correct += point.correct
    wrong += point.wrong
  }
  const judged = correct + wrong
  return {
    reviews,
    correct,
    accuracy: judged === 0 ? 0 : correct / judged,
  }
})

const showStats = computed(
  () => practiceAllowed.value && statsStatus.value !== 'failed',
)

const statReviews = computed(() =>
  statsStatus.value === 'ready' ? String(totals.value.reviews) : PROFILE.statPending,
)
const statCorrect = computed(() =>
  statsStatus.value === 'ready' ? String(totals.value.correct) : PROFILE.statPending,
)
const statAccuracy = computed(() =>
  statsStatus.value === 'ready' ? formatPercent(totals.value.accuracy, 0) : PROFILE.statPending,
)

/**
 * 数字等权限快照到了再取：练习面被明确关掉时不发这个注定 403 的请求，也
 * 不留一份没权限的历史数字。快照取失败（`'unknown'`）时照常取 —— 后端每个
 * 请求还会再判一次。
 */
watch(
  [permissionStatus, practiceAllowed],
  ([status, allowed]) => {
    if (status === 'idle' || status === 'loading') return
    if (!allowed) {
      stats.value = null
      statsStatus.value = 'idle'
      return
    }
    if (statsStatus.value === 'idle' || statsStatus.value === 'failed') void loadStats()
  },
  { immediate: true },
)
</script>

<template>
  <AppShell>
    <div class="page">
      <div class="page__inner">
        <PageHeader :title="PROFILE.title" :description="PROFILE.description" />

        <section class="hero">
          <div class="hero__avatar" aria-hidden="true">
            <span v-if="initial !== ''">{{ initial }}</span>
            <AppIcon v-else name="user" tone="ink" />
          </div>
          <div class="hero__info">
            <div class="hero__name-row">
              <h2 class="hero__name">{{ username || '—' }}</h2>
              <span v-if="roleLabel !== ''" class="hero__badge">{{ roleLabel }}</span>
            </div>
            <p class="hero__bio">{{ PROFILE.bio }}</p>

            <div v-if="showStats" class="hero__stats">
              <div class="stat">
                <span class="stat__value">{{ statReviews }}</span>
                <span class="stat__label">{{ PROFILE.statReviews }}</span>
              </div>
              <div class="stat">
                <span class="stat__value">{{ statCorrect }}</span>
                <span class="stat__label">{{ PROFILE.statCorrect }}</span>
              </div>
              <div class="stat">
                <span class="stat__value">{{ statAccuracy }}</span>
                <span class="stat__label">{{ PROFILE.statAccuracy }}</span>
              </div>
            </div>
          </div>
        </section>

        <div v-if="showShortcuts" class="shortcuts">
          <RouterLink v-if="showFavorites" class="shortcut" to="/profile/favorites">
            <span class="shortcut__icon shortcut__icon--yellow" aria-hidden="true"><AppIcon name="star" /></span>
            <span class="shortcut__text">
              <strong>{{ PROFILE.favorites }}</strong>
              <small>{{ PROFILE.favoritesHint }}</small>
            </span>
          </RouterLink>
          <RouterLink v-if="showQuestions" class="shortcut" to="/questions">
            <span class="shortcut__icon shortcut__icon--blue" aria-hidden="true"><AppIcon name="quiz" /></span>
            <span class="shortcut__text">
              <strong>{{ PROFILE.questions }}</strong>
              <small>{{ PROFILE.questionsHint }}</small>
            </span>
          </RouterLink>
          <RouterLink v-if="showPractice" class="shortcut" to="/practice">
            <span class="shortcut__icon shortcut__icon--pink" aria-hidden="true"><AppIcon name="practice" /></span>
            <span class="shortcut__text">
              <strong>{{ PROFILE.practice }}</strong>
              <small>{{ PROFILE.practiceHint }}</small>
            </span>
          </RouterLink>
        </div>

        <div class="links">
          <RouterLink class="links__row" to="/profile/account">
            <span class="links__label"><AppIcon name="user" />{{ PROFILE.accountSecurity }}</span>
            <span class="links__value">
              {{ PROFILE.accountSecurityHint }}
              <span class="links__chevron" aria-hidden="true">›</span>
            </span>
          </RouterLink>
          <RouterLink class="links__row" to="/settings">
            <span class="links__label"><AppIcon name="settings" />{{ PROFILE.settings }}</span>
            <span class="links__value">
              {{ PROFILE.settingsHint }}
              <span class="links__chevron" aria-hidden="true">›</span>
            </span>
          </RouterLink>
          <RouterLink v-if="showAdmin" class="links__row" to="/profile/users">
            <span class="links__label"><AppIcon name="users" />{{ ADMIN.users }}</span>
            <span class="links__value">
              {{ ADMIN.usersHint }}
              <span class="links__chevron" aria-hidden="true">›</span>
            </span>
          </RouterLink>
          <RouterLink v-if="showAdmin" class="links__row" to="/profile/logs">
            <span class="links__label"><AppIcon name="log" />{{ ADMIN.audit }}</span>
            <span class="links__value">
              {{ ADMIN.auditHint }}
              <span class="links__chevron" aria-hidden="true">›</span>
            </span>
          </RouterLink>
        </div>
      </div>
    </div>
  </AppShell>
</template>

<style scoped>
/* 演示稿 `.profile-header-card`：粉蓝渐变、28px 圆角、右上角一块高光。 */
.hero {
  position: relative;
  display: flex;
  gap: 32px;
  align-items: center;
  padding: 40px;
  overflow: hidden;
  border: 1px solid var(--glass-border);
  border-radius: 28px;
  background: linear-gradient(135deg, #fff0fe, #e8faff);
  box-shadow: var(--glass-shadow);
}

.hero::before {
  content: '';
  position: absolute;
  top: -50px;
  right: -50px;
  width: 200px;
  height: 200px;
  border-radius: 50%;
  background: radial-gradient(circle, rgb(255 255 255 / 80%) 0%, transparent 70%);
}

.hero__avatar {
  position: relative;
  z-index: 1;
  display: grid;
  flex: none;
  place-items: center;
  width: 100px;
  height: 100px;
  border: 4px solid #fff;
  border-radius: 50%;
  background: linear-gradient(135deg, var(--color-yellow), var(--color-pink));
  box-shadow: 0 8px 24px rgb(0 0 0 / 10%);
  color: var(--text);
  font-size: 40px;
  font-weight: 800;
  line-height: 1;
}

.hero__info {
  position: relative;
  z-index: 1;
  flex: 1;
  min-width: 0;
}

.hero__name-row {
  display: flex;
  gap: 12px;
  align-items: center;
  margin-bottom: 8px;
}

.hero__name {
  margin: 0;
  color: var(--text);
  font-size: 28px;
  font-weight: 800;
  letter-spacing: -0.02em;
}

.hero__badge {
  padding: 4px 10px;
  border-radius: 8px;
  background: rgb(255 255 255 / 80%);
  color: var(--color-purple);
  font-size: 12px;
  font-weight: 800;
  box-shadow: 0 2px 8px rgb(177 140 255 / 20%);
}

.hero__bio {
  margin: 0 0 16px;
  color: var(--text-muted);
  font-size: 14px;
}

.hero__stats {
  display: flex;
  gap: 24px;
}

.stat {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.stat__value {
  color: var(--text);
  font-size: 18px;
  font-weight: 800;
}

.stat__label {
  color: var(--text-muted);
  font-size: 12px;
  font-weight: 600;
}

/* 演示稿 `.profile-actions-grid` / `.p-action-card`。 */
/* 入口从两张变成三张：按可用宽度自动收放，权限隐藏掉一张时不留下空列。 */
.shortcuts {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 16px;
}

.shortcut {
  display: flex;
  gap: 16px;
  align-items: center;
  padding: 20px;
  border: 1px solid rgb(0 0 0 / 2%);
  border-radius: 20px;
  background: #fff;
  box-shadow: var(--glass-shadow);
  color: var(--text);
  text-decoration: none;
  transition: transform 0.3s cubic-bezier(0.34, 1.56, 0.64, 1), box-shadow 0.3s ease;
}

.shortcut:hover {
  transform: translateY(-4px);
  box-shadow: var(--glass-shadow-hover);
}

.shortcut__icon {
  display: grid;
  flex: none;
  place-items: center;
  width: 48px;
  height: 48px;
  border-radius: 16px;
  font-size: 24px;
}

.shortcut__icon--yellow {
  background: #fff9e6;
  color: #d4a017;
}

.shortcut__icon--blue {
  background: #e8faff;
  color: #0099cc;
}

.shortcut__icon--pink {
  background: #fde7f3;
  color: #c026a3;
}

.shortcut__text {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.shortcut__text strong {
  font-size: 16px;
  font-weight: 700;
}

.shortcut__text small {
  color: var(--text-muted);
  font-size: 12px;
}

/* 演示稿 `.profile-list` / `.p-list-item`。 */
.links {
  padding: 8px;
  border-radius: 20px;
  background: #fff;
  box-shadow: var(--glass-shadow);
}

.links__row {
  display: flex;
  gap: 16px;
  align-items: center;
  justify-content: space-between;
  padding: 16px 20px;
  border-bottom: 1px solid rgb(0 0 0 / 4%);
  border-radius: 12px;
  color: var(--text);
  text-decoration: none;
  transition: background 0.2s ease;
}

.links__row:last-child {
  border-bottom: 0;
}

.links__row:hover {
  background: #f8fafc;
}

.links__label {
  display: flex;
  gap: 16px;
  align-items: center;
  font-size: 15px;
  font-weight: 600;
}

.links__label span {
  font-size: 18px;
}

.links__value {
  display: flex;
  gap: 8px;
  align-items: center;
  color: var(--text-muted);
  font-size: 14px;
}

.links__chevron {
  color: #e2e8f0;
  font-size: 18px;
}
</style>
