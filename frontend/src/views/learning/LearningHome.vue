<script setup lang="ts">
/**
 * 学习首页。
 *
 * 版式照 `.product/Rinnex_UI_初版演示_v1.0.html.html` 的「全新首页看板」：
 * 48px 的标题 + 18px 说明，下面是一个三列的液态玻璃砖块（Bento）网格，
 * 砖块 32px 内边距、28px 圆角、半透明白底加模糊，砖缝 32px、整体最宽 1200。
 * 五块砖按演示稿的顺序排：
 *
 *   路径完成率（流体球）｜本周学习快照（三行带色点）｜当前模块进度（步骤条）
 *   本周任务进度（两根渐变进度条，横跨两列）｜今日学习时长（大字 + 一句提示）
 *
 * 卡里的数各有出处：**路径完成率**是第一条掌握路径自己的已掌握 / 总数
 * （`GET /dashboard` 每行的 `progress`）；**当前模块进度**是这条路径里第一个
 * 还没全掌握的模块（`GET /mastery/paths/:id`）；**练习正确率**是本周练习统计
 * 里判过的题（`GET /practice/stats`）；**本周掌握知识点**是证据回放出来的
 * "本周从没掌握变成掌握"的个数（`GET /mastery/mastered`）；**连续打卡**和
 * **今日学习时长**是在线心跳记下的分钟与天数（`GET/POST /presence`）；
 * **复习错题集**是本周复习过的错题数 / 错题集现在的题数（`GET /practice/mistakes`）；
 * **掌握路径模块**是本周完成的模块数 / 这条路径的模块总数
 * （`GET /mastery/paths/:id/progress`）。五块砖的数都有出处了：读不到的那一格
 * 画横线，不填示意值，也不把"还没读到"画成一个 0。
 */

import { computed, onMounted, ref } from 'vue'
import { RouterLink, type RouteLocationRaw } from 'vue-router'

import type { ApiFailure } from '../../api/client'
import { getDashboard, type DashboardView } from '../../api/dashboard'
import {
  getMasteredBetween,
  getMasteryPath,
  getPathProgress,
  type MasteryModule,
  type PathProgress,
} from '../../api/mastery'
import {
  getMistakeProgress,
  getPracticeStats,
  type MistakeProgress,
  type PracticeStats,
} from '../../api/practice'
import { canSurface } from '../../auth/permissions'
import PartialBanner from '../../components/common/PartialBanner.vue'
import SkeletonBlock from '../../components/common/SkeletonBlock.vue'
import SurfaceGate from '../../components/common/SurfaceGate.vue'
import { LEARNING } from '../../copy/learning'
import { presenceSummary, readPresence } from '../../presence'
import { EMPTY, formatPercent } from '../../shared/format'
import { toFailure } from '../../shared/resource'

type Status = 'idle' | 'loading' | 'ready'

const status = ref<Status>('idle')
const failure = ref<ApiFailure | null>(null)
const dashboard = ref<DashboardView | null>(null)
const weekStats = ref<PracticeStats | null>(null)
/** 本周从"没掌握"变成"掌握"的知识点数；读不到时是 `null`，画横线。 */
const weekMastered = ref<number | null>(null)
/** 本周复习过的错题 / 错题集现在有多少道；读不到时是 `null`。 */
const mistakes = ref<MistakeProgress | null>(null)
/** 本周完成的模块 / 这条路径的模块总数；读不到时是 `null`。 */
const moduleTask = ref<PathProgress | null>(null)
/** 路径完成率那条路径的整棵树，用来算当前模块和步骤条。 */
const modules = ref<MasteryModule[]>([])

/** 本周从周一开始。统计接口按桶给数，这里只把这一周的几个桶加起来。 */
function startOfWeek(now: Date): Date {
  const mondayOffset = (now.getDay() + 6) % 7
  return new Date(now.getFullYear(), now.getMonth(), now.getDate() - mondayOffset)
}

async function load(): Promise<void> {
  if (!dashboard.value) status.value = 'loading'
  failure.value = null

  // 三个读互不牵连：一个取不到时它那一格画横线，其余照常。
  // 权限明确为 false 时不发那个请求；`unknown` 时照发，服务端再判一次。
  const weekStart = startOfWeek(new Date()).toISOString()
  const now = new Date().toISOString()
  const [view, stats, mastered, reviewed] = await Promise.allSettled([
    getDashboard(),
    canSurface('practice') === false
      ? Promise.resolve(null)
      : getPracticeStats({ from: weekStart, bucket: 'day' }),
    canSurface('mastery') === false
      ? Promise.resolve(null)
      : getMasteredBetween(weekStart, now),
    canSurface('practice') === false
      ? Promise.resolve(null)
      : getMistakeProgress(weekStart, now),
  ])

  if (view.status === 'fulfilled') {
    dashboard.value = view.value
    modules.value = []
    moduleTask.value = null
    // 模块进度与模块完成数都要用完成率那条路径自己的树，所以得先知道是哪条
    // 路径，再并行读它的树和这一周完成的模块数。
    const first = view.value.paths[0]
    if (first) {
      const [detail, progress] = await Promise.allSettled([
        getMasteryPath(first.id),
        canSurface('mastery') === false
          ? Promise.resolve(null)
          : getPathProgress(first.id, weekStart, now),
      ])
      // 读不到时这两格画横线，不影响完成率那格（比例本来就在 `/dashboard` 里）。
      modules.value =
        detail.status === 'fulfilled' ? (detail.value.tree.modules ?? []) : []
      moduleTask.value =
        progress.status === 'fulfilled' && progress.value !== null ? progress.value : null
    }
  } else {
    failure.value = toFailure(view.reason)
  }
  weekStats.value = stats.status === 'fulfilled' ? stats.value : null
  weekMastered.value =
    mastered.status === 'fulfilled' && mastered.value !== null ? mastered.value.mastered : null
  mistakes.value =
    reviewed.status === 'fulfilled' && reviewed.value !== null ? reviewed.value : null
  status.value = 'ready'
}

/** 卡下面那行写哪条路径：列表的第一条，也就是最近动过的那条。 */
const topPath = computed(() => dashboard.value?.paths[0] ?? null)

/**
 * 路径完成率是**这一条路径自己的**已掌握 / 总数，和名字下面那句是同一对数。
 * 没有路径、或者路径里还没有知识点时是 `null`，画成一条横线 —— 0% 是一个结论。
 */
const progress = computed(() => {
  const value = topPath.value?.progress
  if (!value || value.total === 0) return null
  return value.done / value.total
})

/** 球的整数百分比；`null` 时球里写横线。 */
const pathPercent = computed(() =>
  progress.value === null ? null : Math.round(progress.value * 100),
)

/**
 * 球外面那圈渐变环：从 180° 起，粉到紫占一半、紫到蓝占另一半，总共铺到
 * 这个百分比为止，后面透明 —— 演示稿里 44% 就是环走了 44%。没有数据时不
 * 画环。
 */
const ringBackground = computed(() => {
  const percent = pathPercent.value ?? 0
  const half = percent / 2
  return `conic-gradient(from 180deg, rgb(247 169 239 / 90%) 0%, rgb(177 140 255 / 90%) ${half}%, rgb(102 210 245 / 90%) ${percent}%, transparent ${percent}%)`
})

/**
 * 在线心跳给的两个数。心跳还没回来（或没登录）时是 `null`，画横线 ——
 * 0 分钟和"还没读到"是两件事。
 */
const todayMinutes = computed(() => presenceSummary.value?.minutes ?? null)
const streakDays = computed(() => presenceSummary.value?.streak_days ?? null)

/**
 * 「本周掌握知识点」与「当前模块进度」点进去都是这条路径的学习地图（按模块
 * 铺开知识点四态）。还没有路径时去一级页选一条 —— 那两格本来也是横线，
 * 但入口仍然成立。
 */
const pointsLink = computed<RouteLocationRaw>(() =>
  topPath.value
    ? { name: 'learning-map', query: { path: topPath.value.id } }
    : { name: 'learning-map' },
)

/** 「路径完成率」整块点进去：回到这条路径的总览；没有路径时去选一条。 */
const pathLink = computed<RouteLocationRaw>(() =>
  topPath.value
    ? { name: 'mastery', query: { path: topPath.value.id } }
    : { name: 'mastery' },
)

/** 时长卡下面那句：今天和昨天的比较，两个数都是心跳记出来的。 */
const timeHint = computed(() => {
  const summary = presenceSummary.value
  return summary ? LEARNING.timeHint(summary.minutes, summary.yesterday_minutes) : ''
})

/** 模块完成进度条旁边那对数：本周完成的模块 / 这条路径的模块总数。 */
const moduleTaskAside = computed(() =>
  moduleTask.value
    ? `${moduleTask.value.modules_done} / ${moduleTask.value.modules_total}`
    : `${EMPTY} / ${EMPTY}`,
)

/** 条宽按同一对数的比例；没有模块（或还没读到）时是 0。 */
const moduleTaskFill = computed(() => {
  const value = moduleTask.value
  if (!value || value.modules_total === 0) return '0%'
  return `${Math.min(100, Math.round((value.modules_done / value.modules_total) * 100))}%`
})

/** 错题集进度条旁边那对数：本周复习过的 / 错题集现在有多少道。 */
const mistakeAside = computed(() =>
  mistakes.value
    ? `${mistakes.value.reviewed} / ${mistakes.value.total}`
    : `${EMPTY} / ${EMPTY}`,
)

/** 条宽按同一对数的比例；错题集是空的（或还没读到）时是 0。 */
const mistakeFill = computed(() => {
  const value = mistakes.value
  if (!value || value.total === 0) return '0%'
  return `${Math.min(100, Math.round((value.reviewed / value.total) * 100))}%`
})

/** 本周判过的题里答对的比例；一题都没判过也是 `null`。 */
const accuracy = computed(() => {
  let correct = 0
  let wrong = 0
  for (const bucket of weekStats.value?.series ?? []) {
    correct += bucket.correct
    wrong += bucket.wrong
  }
  const judged = correct + wrong
  return judged === 0 ? null : correct / judged
})

function masteredIn(module: MasteryModule): number {
  return (module.knowledge_points ?? []).filter((point) => point.mastery === 'mastered').length
}

/**
 * 当前模块是**路径完成率那条路径**的第一个还没全部掌握的模块；全都掌握了就
 * 停在最后一个。取的是树的顺序，也就是路径对话给出的学习顺序。
 */
const currentModuleIndex = computed(() => {
  const list = modules.value
  if (list.length === 0) return -1
  const index = list.findIndex((module) => {
    const points = module.knowledge_points ?? []
    return points.length > 0 && masteredIn(module) < points.length
  })
  return index >= 0 ? index : list.length - 1
})

const currentModule = computed(() =>
  currentModuleIndex.value >= 0 ? modules.value[currentModuleIndex.value] : null,
)

/** 当前模块里已掌握 / 知识点数，标题右侧那对数。 */
const moduleProgress = computed(() => {
  const module = currentModule.value
  if (!module) return null
  const points = module.knowledge_points ?? []
  if (points.length === 0) return null
  return { done: masteredIn(module), total: points.length }
})

const moduleAside = computed(() =>
  moduleProgress.value
    ? `${moduleProgress.value.done} / ${moduleProgress.value.total}`
    : `${EMPTY} / ${EMPTY}`,
)

type StepState = 'done' | 'active' | 'todo'

/** 一个模块一个节点：整块掌握的是 done，当前模块是 active，其余是 todo。 */
const steps = computed<{ key: string; state: StepState }[]>(() => {
  const list = modules.value
  if (list.length === 0) {
    return Array.from({ length: 5 }, (_, index) => ({
      key: `placeholder-${index}`,
      state: 'todo' as const,
    }))
  }
  return list.map((module, index) => {
    const points = module.knowledge_points ?? []
    let state: StepState = 'todo'
    if (points.length > 0 && masteredIn(module) === points.length) state = 'done'
    else if (index === currentModuleIndex.value) state = 'active'
    return { key: module.id, state }
  })
})

/** 进度线铺到当前节点为止；全部掌握时铺满。 */
const stepsLineWidth = computed(() => {
  const list = steps.value
  const active = list.findIndex((step) => step.state === 'active')
  if (active > 0 && list.length > 1) return `${(active / (list.length - 1)) * 100}%`
  if (active === -1 && list.length > 0 && list.every((step) => step.state === 'done')) return '100%'
  return '0%'
})

onMounted(() => {
  // 心跳在根组件里已经起了；这里多读一次是为了"这次打开页面"的数立刻是新的，
  // 而不是等下一分钟的报到。读不到不影响其余几块砖。
  void readPresence()
  if (canSurface('dashboard') === false) return
  void load()
})
</script>

<template>
  <SurfaceGate surface="dashboard">
    <div class="home">
      <header class="home-header">
        <h1 class="home-title">{{ LEARNING.homeTitle }}</h1>
        <p class="home-subtitle">{{ LEARNING.homeSubtitle }}</p>
      </header>

      <PartialBanner v-if="failure" :failure="failure" :text="LEARNING.loadFailed" @retry="load" />

      <SkeletonBlock v-if="status === 'loading'" :lines="6" :line-height="120" />

      <div v-else class="home-grid">
        <!-- 路径完成率：流体球。整块可点，回到这条路径的总览。 -->
        <RouterLink class="glass-brick flow-ball-card brick-link" :to="pathLink">
          <div class="brick-title">{{ LEARNING.pathCompletion }}</div>
          <div class="flow-ball-wrapper">
            <div class="flow-ball-shadow"></div>
            <div class="flow-ball">
              <span
                v-if="progress !== null"
                class="flow-ball-ring"
                :style="{ background: ringBackground }"
              ></span>
              <div class="flow-ball-text">
                {{ pathPercent === null ? EMPTY : pathPercent
                }}<span v-if="pathPercent !== null" class="flow-ball-text__unit">%</span>
              </div>
            </div>
          </div>
          <div class="flow-ball-label">
            {{ topPath ? LEARNING.pathSubtitle(topPath.title) : LEARNING.noPath }}
          </div>
        </RouterLink>

        <!-- 本周学习快照：三行，左边一个色点 -->
        <section class="glass-brick">
          <div class="brick-title">{{ LEARNING.weekSnapshot }}</div>
          <!--
            前两行整行可点：正确率回练习队列；本周掌握知识点回这条路径的
            学习地图（按模块铺开知识点四态）。连续打卡没有去处，保持普通一行。
          -->
          <div class="stat-list">
            <RouterLink class="stat-row stat-row--link" to="/practice">
              <div class="stat-label">
                <span class="stat-dot stat-dot--blue"></span>{{ LEARNING.accuracy }}
              </div>
              <div class="stat-val">{{ formatPercent(accuracy, 0) }}</div>
            </RouterLink>
            <RouterLink class="stat-row stat-row--link" :to="pointsLink">
              <div class="stat-label">
                <span class="stat-dot stat-dot--pink"></span>{{ LEARNING.weekMastered }}
              </div>
              <div class="stat-val">
                {{ weekMastered === null ? EMPTY : weekMastered
                }}<span v-if="weekMastered !== null" class="stat-val__unit">
                  {{ LEARNING.points }}</span>
              </div>
            </RouterLink>
            <div class="stat-row">
              <div class="stat-label">
                <span class="stat-dot stat-dot--purple"></span>{{ LEARNING.streak }}
              </div>
              <div class="stat-val">
                {{ streakDays === null ? EMPTY : streakDays
                }}<span v-if="streakDays !== null" class="stat-val__unit">
                  {{ LEARNING.days }}</span>
              </div>
            </div>
          </div>
          <p class="brick-note">{{ LEARNING.snapshotNote }}</p>
        </section>

        <!-- 当前模块进度：标题右侧写分数，下面一条步骤线。整块可点，
             去学习地图看这条路径的模块与知识点。 -->
        <RouterLink class="glass-brick brick-link" :to="pointsLink">
          <div class="brick-title">
            {{ LEARNING.moduleProgress }}
            <span class="brick-title__aside">{{ moduleAside }}</span>
          </div>
          <div class="steps-container">
            <div class="steps-line">
              <div class="steps-line-active" :style="{ width: stepsLineWidth }"></div>
            </div>
            <div
              v-for="step in steps"
              :key="step.key"
              :class="['step-node', `step-node--${step.state}`]"
            ></div>
          </div>
          <p class="step-desc">
            {{ LEARNING.learningNow('') }}<strong>{{ currentModule?.name ?? EMPTY }}</strong></p>
          <p v-if="currentModule === null" class="brick-note">{{ LEARNING.moduleNote }}</p>
        </RouterLink>

        <!-- 本周任务进度：两根渐变进度条，横跨两列 -->
        <section class="glass-brick brick-span-2">
          <div class="brick-title">{{ LEARNING.weekTasks }}</div>
          <!-- 两条进度都去练习页：错题本来就在队列里（带「错题」标签），
               没有单独的错题页，所以不另造一个入口。 -->

          <RouterLink class="progress-group progress-group--link" to="/practice">
            <div class="progress-head">
              <span>{{ LEARNING.taskModules }}</span><span>{{ moduleTaskAside }}</span>
            </div>
            <div class="progress-bar">
              <div class="progress-fill fill-pink-purple" :style="{ width: moduleTaskFill }"></div>
            </div>
          </RouterLink>
          <RouterLink class="progress-group progress-group--link" to="/practice">
            <div class="progress-head">
              <span>{{ LEARNING.taskReview }}</span><span>{{ mistakeAside }}</span>
            </div>
            <div class="progress-bar">
              <div class="progress-fill fill-blue-green" :style="{ width: mistakeFill }"></div>
            </div>
          </RouterLink>
        </section>

        <!-- 今日学习时长：大字 + 一句提示 -->
        <section class="glass-brick time-card">
          <div class="brick-title">{{ LEARNING.todayTime }}</div>
          <div class="time-display">
            {{ todayMinutes === null ? EMPTY : todayMinutes
            }}<span class="time-unit">{{ LEARNING.minutes }}</span>
          </div>
          <div class="time-hint">{{ timeHint }}</div>
        </section>
      </div>
    </div>
  </SurfaceGate>
</template>

<style scoped>
/* 演示稿 `.home-header`：标题 42px / 800、字距 -1.5px，说明 16px 灰。 */
.home-header {
  margin: 8px 0 56px;
}

.home-title {
  margin: 0 0 16px;
  color: var(--text);
  font-size: 48px;
  font-weight: 800;
  letter-spacing: -1.8px;
  line-height: 1.2;
}

.home-subtitle {
  margin: 0;
  color: var(--text-faint);
  font-size: 18px;
  font-weight: 500;
}

/* 演示稿 `.home-grid`：三列，砖缝比演示稿再宽一档（32px），最宽 1200。 */
.home-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 32px;
  max-width: 1200px;
}

/* 演示稿 `.glass-brick`：28px 内边距、24px 圆角、玻璃底加模糊与内高光，
   底上还有一层 20px 的白色网格。 */
.glass-brick {
  position: relative;
  display: flex;
  flex-direction: column;
  padding: 32px;
  overflow: hidden;
  border: 1px solid var(--glass-border);
  border-radius: 28px;
  background-color: var(--glass-bg);
  background-image:
    linear-gradient(rgb(255 255 255 / 10%) 1px, transparent 1px),
    linear-gradient(90deg, rgb(255 255 255 / 10%) 1px, transparent 1px);
  background-position: center;
  background-size: 20px 20px;
  box-shadow: var(--glass-shadow), var(--inset-light);
  backdrop-filter: blur(28px);
}

/* 整块是链接的砖：抹掉链接自己的颜色与下划线，剩下的就是原来那张砖。 */
.brick-link {
  color: inherit;
  text-decoration: none;
  transition: all 0.3s cubic-bezier(0.25, 0.8, 0.25, 1);
}

/* 只有**整块能点**的砖才上浮。另外几张砖（统计、进度、在场时长）是展示面
   板，自己没有去处 —— 鼠标划过时它们不动，会动的只有砖里那几行真链接。 */
.brick-link:hover {
  transform: translateY(-4px);
  box-shadow:
    0 32px 64px -12px rgb(0 0 0 / 6%),
    inset 0 2px 4px rgb(255 255 255 / 100%);
}

.brick-span-2 {
  grid-column: span 2;
}

.brick-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 28px;
  color: var(--text);
  font-size: 17px;
  font-weight: 700;
}

.brick-title__aside {
  color: var(--text-faint);
  font-size: 15px;
  font-weight: 600;
}

/* 流体球：160 的舞台、130 的玻璃球、底下一条紫色阴影，外圈渐变环由内联
   的 conic-gradient 画，遮罩只留最外一圈。 */
.flow-ball-card {
  align-items: center;
  justify-content: center;
}

.flow-ball-card .brick-title,
.time-card .brick-title {
  width: 100%;
}

.flow-ball-wrapper {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 180px;
  height: 180px;
  margin: 12px 0 24px;
}

.flow-ball-shadow {
  position: absolute;
  bottom: -5px;
  z-index: 0;
  width: 112px;
  height: 22px;
  border-radius: 50%;
  background: rgb(177 140 255 / 30%);
  filter: blur(16px);
}

.flow-ball {
  position: relative;
  z-index: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 150px;
  height: 150px;
  border-radius: 50%;
  background: radial-gradient(
    circle at 30% 30%,
    rgb(255 255 255 / 100%) 0%,
    rgb(255 255 255 / 40%) 60%,
    rgb(200 220 255 / 20%) 100%
  );
  box-shadow:
    inset 0 10px 20px rgb(255 255 255 / 90%),
    inset -10px -10px 20px rgb(177 140 255 / 15%),
    0 10px 20px rgb(177 140 255 / 10%);
}

.flow-ball-ring {
  position: absolute;
  inset: -4px;
  border-radius: 50%;
  filter: blur(2px);
  opacity: 0.9;
  mask: radial-gradient(transparent 64%, black 66%);
  -webkit-mask: radial-gradient(transparent 64%, black 66%);
}

.flow-ball-text {
  position: relative;
  z-index: 10;
  color: var(--text);
  font-size: 44px;
  font-weight: 800;
  letter-spacing: -1.5px;
}

.flow-ball-text__unit {
  font-size: 20px;
  font-weight: 600;
}

.flow-ball-label {
  margin-top: 12px;
  color: var(--text-faint);
  font-size: 16px;
  font-weight: 600;
}

/* 快照三行：名字带色点、数字在右，整组在砖里垂直居中。 */
.stat-list {
  display: flex;
  flex: 1;
  flex-direction: column;
  justify-content: center;
  gap: 26px;
}

.stat-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.stat-label {
  display: flex;
  align-items: center;
  gap: 14px;
  color: var(--text-muted);
  font-size: 17px;
  font-weight: 600;
}

.stat-dot {
  width: 9px;
  height: 9px;
  border-radius: 50%;
}

.stat-dot--blue {
  background: var(--color-blue);
}

.stat-dot--pink {
  background: var(--color-pink);
}

.stat-dot--purple {
  background: var(--color-purple);
}

.stat-val {
  color: var(--text);
  font-size: 24px;
  font-weight: 800;
  letter-spacing: -0.5px;
}

.stat-val__unit {
  color: var(--text-faint);
  font-size: 16px;
  font-weight: 600;
}

/*
 * 能点进去的行：整行就是链接，不加箭头也不写"跳转到…"—— 只靠悬停时的一层
 * 淡底色和手型光标说这里能点。负外边距配正内边距：底色比文字宽一点，但行与
 * 行的间距不变。
 */
.stat-row--link,
.progress-group--link {
  color: inherit;
  text-decoration: none;
}

.stat-row--link {
  margin: 0 -10px;
  padding: 4px 10px;
  border-radius: 12px;
  transition: background 0.2s ease;
}

.stat-row--link:hover {
  background: rgb(255 255 255 / 45%);
}

.progress-group--link {
  display: block;
}

.progress-group--link:hover .progress-head {
  color: var(--text);
}

@media (prefers-reduced-motion: reduce) {
  .stat-row--link {
    transition: none;
  }
}

/* 步骤条：一条 3px 的底线，五个 16px 的节点压在上面。没有数据时没有
   done / active，全是灰圈，进度线宽度为 0。 */
.steps-container {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 12px;
  padding: 0 12px;
}

.steps-line {
  position: absolute;
  top: 50%;
  right: 22px;
  left: 22px;
  z-index: 1;
  height: 4px;
  border-radius: 2px;
  background: rgb(0 0 0 / 5%);
  transform: translateY(-50%);
}

.steps-line-active {
  position: absolute;
  top: 0;
  left: 0;
  height: 100%;
  border-radius: 2px;
  background: var(--color-purple);
  width: 0;
}

.step-node {
  position: relative;
  z-index: 2;
  box-sizing: border-box;
  width: 18px;
  height: 18px;
  border: 3px solid #d1d1d6;
  border-radius: 50%;
  background: white;
}

/* 演示稿：整块掌握的是紫圈，正在学的是蓝圈加一圈淡蓝光晕。 */
.step-node--done {
  border-color: var(--color-purple);
}

.step-node--active {
  border-color: var(--color-blue);
  box-shadow: 0 0 0 4px rgb(102 210 245 / 20%);
}

.step-desc {
  margin-top: 40px;
  color: var(--text-faint);
  font-size: 16px;
  font-weight: 500;
}

.step-desc strong {
  color: var(--text);
  font-weight: 700;
}

/* 两根渐变进度条：轨道 12px 半透明白、内阴影，填充由宽度决定。 */
.progress-group {
  margin-bottom: 32px;
}

.progress-group:last-child {
  margin-bottom: 0;
}

.progress-head {
  display: flex;
  justify-content: space-between;
  margin-bottom: 14px;
  color: var(--text-muted);
  font-size: 16px;
  font-weight: 600;
  transition: color 0.2s ease;
}

.progress-bar {
  height: 14px;
  overflow: hidden;
  border-radius: 7px;
  background: rgb(255 255 255 / 60%);
  box-shadow: inset 0 1px 3px rgb(0 0 0 / 5%);
}

.progress-fill {
  height: 100%;
  border-radius: 7px;
  box-shadow: inset 0 2px 4px rgb(255 255 255 / 30%);
  width: 0;
}

.fill-pink-purple {
  background: linear-gradient(90deg, var(--color-pink), var(--color-purple));
}

.fill-blue-green {
  background: linear-gradient(90deg, var(--color-blue), var(--color-green));
}

/* 今日学习时长：48px 的数字，单位 18px，下面一句 13px 提示。 */
.time-card {
  align-items: center;
  justify-content: center;
}

.time-display {
  display: flex;
  align-items: baseline;
  gap: 4px;
  margin-top: 12px;
  color: var(--text);
  font-size: 56px;
  font-weight: 800;
  letter-spacing: -1.8px;
}

.time-unit {
  color: var(--text-faint);
  font-size: 20px;
  font-weight: 600;
}

.time-hint {
  margin-top: 28px;
  color: var(--text-faint);
  font-size: 15px;
  font-weight: 500;
  text-align: center;
}

/* 砖底下的一句说明：比的基准是什么、这条路径为什么没有模块。演示稿里没有
   这一行，补它是为了不让人对着一个数或一条横线猜；13px，不抢砖里的数字。 */
.brick-note {
  margin: 20px 0 0;
  color: var(--text-faint);
  font-size: 13px;
  font-weight: 500;
  line-height: 1.6;
}
</style>
