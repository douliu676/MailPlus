<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { CheckCircle2, Download, ListChecks, LoaderCircle, Trash2, X, XCircle } from 'lucide-vue-next'
import { useAppStore } from '../stores/app'
import { useTaskStore, type BackgroundTask } from '../stores/tasks'

const appStore = useAppStore()
const taskStore = useTaskStore()
const props = withDefaults(defineProps<{
  variant?: 'topbar' | 'sidebar'
  collapsed?: boolean
}>(), {
  variant: 'topbar',
  collapsed: false,
})
const open = ref(false)
const rootRef = ref<HTMLElement | null>(null)
const panelRef = ref<HTMLElement | null>(null)
let refreshTimer: number | null = null

const tasks = computed(() => taskStore.orderedTasks.value)
const runningCount = computed(() => taskStore.runningTasks.value.length)
const finishedCount = computed(() => taskStore.finishedTasks.value.length)
const activeTask = computed(() => taskStore.runningTasks.value[0] || tasks.value[0] || null)
const activeProgress = computed(() => (activeTask.value ? taskStore.progressPercent(activeTask.value) : 0))
const isSidebar = computed(() => props.variant === 'sidebar')

function toggleOpen() {
  open.value = !open.value
  if (open.value) {
    void taskStore.refreshTasks().catch(() => undefined)
  }
}

function closeOnOutside(event: MouseEvent) {
  const target = event.target as Node
  if (!rootRef.value?.contains(target) && !panelRef.value?.contains(target)) {
    open.value = false
  }
}

function taskStatusClass(task: BackgroundTask) {
  if (task.status === 'success') return 'task-status-success'
  if (task.status === 'partial') return 'task-status-warning'
  if (task.status === 'failed') return 'task-status-error'
  return 'task-status-running'
}

function taskBarClass(task: BackgroundTask) {
  if (task.status === 'success') return 'task-progress-success'
  if (task.status === 'partial') return 'task-progress-warning'
  if (task.status === 'failed') return 'task-progress-error'
  return 'task-progress-running'
}

function taskIcon(task: BackgroundTask) {
  if (task.status === 'success') return CheckCircle2
  if (task.status === 'failed') return XCircle
  return LoaderCircle
}

async function downloadResult(task: BackgroundTask) {
  try {
    await taskStore.downloadTask(task)
    appStore.showSuccess('下载已开始')
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '任务结果下载失败')
  }
}

async function removeTask(id: string) {
  try {
    await taskStore.removeTask(id)
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '任务结果清理失败')
  }
}

async function clearFinishedTasks() {
  try {
    await taskStore.clearFinishedTasks()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '任务结果清理失败')
  }
}

onMounted(() => {
  document.addEventListener('click', closeOnOutside)
  void taskStore.refreshTasks().catch(() => undefined)
  refreshTimer = window.setInterval(() => {
    void taskStore.refreshTasks().catch(() => undefined)
  }, 30000)
})

onBeforeUnmount(() => {
  document.removeEventListener('click', closeOnOutside)
  if (refreshTimer) {
    window.clearInterval(refreshTimer)
    refreshTimer = null
  }
})
</script>

<template>
  <div
    ref="rootRef"
    class="task-center relative"
    :class="[
      isSidebar ? 'task-center-sidebar' : 'task-center-topbar',
      { 'task-center-sidebar-collapsed': isSidebar && props.collapsed },
    ]"
  >
    <button
      class="task-center-trigger"
      :class="{
        'task-center-trigger-sidebar': isSidebar,
        'task-center-trigger-sidebar-collapsed': isSidebar && props.collapsed,
      }"
      type="button"
      title="任务中心"
      @click.stop="toggleOpen"
    >
      <ListChecks class="task-center-trigger-icon h-5 w-5" />
      <span v-if="isSidebar && !props.collapsed" class="task-center-trigger-label">任务中心</span>
      <span v-if="runningCount > 0" class="task-center-badge">{{ runningCount }}</span>
      <span v-else-if="finishedCount > 0" class="task-center-dot"></span>
      <span v-if="runningCount > 0" class="task-center-ring" :style="{ '--task-progress': `${activeProgress}%` }"></span>
    </button>

    <Teleport to="body" :disabled="!isSidebar">
      <transition name="dropdown">
        <div
          v-if="open"
          ref="panelRef"
          class="task-center-panel"
          :class="{
            'task-center-panel-sidebar': isSidebar,
            'task-center-panel-sidebar-collapsed': isSidebar && props.collapsed,
          }"
        >
        <div class="task-center-header">
          <div>
            <div class="task-center-title">任务中心</div>
            <div class="task-center-subtitle">{{ runningCount > 0 ? `${runningCount} 个任务进行中` : '暂无运行中任务' }}</div>
          </div>
          <button
            v-if="finishedCount > 0"
            class="task-center-clear"
            type="button"
            title="清除已完成"
            @click="clearFinishedTasks"
          >
            <Trash2 class="h-4 w-4" />
          </button>
        </div>

        <div v-if="tasks.length === 0" class="task-center-empty">
          暂无任务
        </div>

        <div v-else class="task-center-list">
          <div v-for="task in tasks" :key="task.id" class="task-item">
            <div class="task-item-top">
              <div class="task-item-title-wrap">
                <component :is="taskIcon(task)" class="task-item-icon h-4 w-4" :class="{ 'animate-spin': task.status === 'running' }" />
                <span class="task-item-title">{{ task.title }}</span>
              </div>
              <div class="task-item-actions">
                <span class="task-status" :class="taskStatusClass(task)">{{ taskStore.statusLabel(task.status) }}</span>
                <button
                  v-if="task.status === 'success' && task.download_url"
                  class="task-icon-button"
                  type="button"
                  title="下载"
                  @click="downloadResult(task)"
                >
                  <Download class="h-4 w-4" />
                </button>
                <button
                  v-if="task.status !== 'running'"
                  class="task-icon-button"
                  type="button"
                  title="移除"
                  @click="removeTask(task.id)"
                >
                  <X class="h-4 w-4" />
                </button>
              </div>
            </div>

            <div class="task-item-message">{{ task.message || '等待任务状态' }}</div>
            <div class="task-progress-track">
              <div
                class="task-progress-bar"
                :class="taskBarClass(task)"
                :style="{ width: `${taskStore.progressPercent(task)}%` }"
              ></div>
            </div>
            <div class="task-item-meta">
              <span>{{ taskStore.progressText(task) }}</span>
              <span>{{ taskStore.progressPercent(task) }}%</span>
            </div>
          </div>
        </div>
        </div>
      </transition>
    </Teleport>
  </div>
</template>

<style scoped>
.task-center-trigger {
  position: relative;
  display: inline-flex;
  height: 2.5rem;
  width: 2.5rem;
  align-items: center;
  justify-content: center;
  border: 1px solid rgb(226 232 240);
  border-radius: 0.75rem;
  background: white;
  color: rgb(71 85 105);
  transition: color 0.15s ease, border-color 0.15s ease, background-color 0.15s ease;
}

.task-center-trigger:hover {
  border-color: rgb(20 184 166 / 0.65);
  color: rgb(13 148 136);
}

.task-center-sidebar {
  width: 100%;
}

.task-center-trigger-sidebar {
  width: 100%;
  justify-content: flex-start;
  gap: 0.75rem;
  border-color: transparent;
  background: transparent;
  padding: 0 0.75rem;
}

.task-center-trigger-sidebar .task-center-trigger-icon {
  color: rgb(56 189 248);
}

.task-center-trigger-sidebar:hover .task-center-trigger-icon {
  color: rgb(14 165 233);
}

.task-center-trigger-sidebar-collapsed {
  width: 2.5rem;
  justify-content: center;
  padding: 0;
}

.task-center-trigger-label {
  min-width: 0;
  overflow: hidden;
  font-size: 0.875rem;
  font-weight: 600;
  line-height: 1;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dark .task-center-trigger {
  border-color: rgb(51 65 85);
  background: rgb(30 41 59);
  color: rgb(203 213 225);
}

.dark .task-center-trigger-sidebar {
  border-color: transparent;
  background: transparent;
}

.dark .task-center-trigger-sidebar .task-center-trigger-icon {
  color: rgb(96 165 250);
}

.dark .task-center-trigger-sidebar:hover .task-center-trigger-icon {
  color: rgb(125 211 252);
}

.dark .task-center-trigger:hover {
  border-color: rgb(45 212 191 / 0.7);
  color: rgb(94 234 212);
}

.task-center-badge {
  position: absolute;
  right: -0.35rem;
  top: -0.35rem;
  display: inline-flex;
  min-width: 1.1rem;
  height: 1.1rem;
  align-items: center;
  justify-content: center;
  border-radius: 999px;
  background: rgb(20 184 166);
  padding: 0 0.25rem;
  color: white;
  font-size: 0.68rem;
  font-weight: 800;
}

.task-center-dot {
  position: absolute;
  right: 0.45rem;
  top: 0.45rem;
  width: 0.45rem;
  height: 0.45rem;
  border-radius: 999px;
  background: rgb(34 197 94);
}

.task-center-ring {
  position: absolute;
  inset: -0.24rem;
  border-radius: 0.95rem;
  background: conic-gradient(rgb(20 184 166) var(--task-progress), transparent 0);
  opacity: 0.32;
  pointer-events: none;
}

.task-center-panel {
  position: absolute;
  right: 0;
  top: calc(100% + 0.65rem);
  z-index: 80;
  width: min(24rem, calc(100vw - 1.5rem));
  overflow: hidden;
  border: 1px solid rgb(226 232 240);
  border-radius: 0.95rem;
  background: white;
  box-shadow: 0 22px 48px rgb(15 23 42 / 0.18);
}

.task-center-panel-sidebar {
  position: fixed;
  right: auto;
  left: 244px;
  top: auto;
  bottom: 6.25rem;
  z-index: 120;
}

.task-center-panel-sidebar-collapsed {
  left: 80px;
}

.dark .task-center-panel {
  border-color: rgb(51 65 85);
  background: rgb(15 23 42);
  box-shadow: 0 22px 48px rgb(0 0 0 / 0.36);
}

.task-center-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  border-bottom: 1px solid rgb(241 245 249);
  padding: 0.9rem 1rem;
}

.dark .task-center-header {
  border-bottom-color: rgb(30 41 59);
}

.task-center-title {
  font-size: 0.92rem;
  font-weight: 800;
  color: rgb(15 23 42);
}

.dark .task-center-title {
  color: white;
}

.task-center-subtitle {
  margin-top: 0.15rem;
  font-size: 0.75rem;
  font-weight: 700;
  color: rgb(100 116 139);
}

.dark .task-center-subtitle {
  color: rgb(148 163 184);
}

.task-center-clear,
.task-icon-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 0.55rem;
  color: rgb(100 116 139);
  transition: background-color 0.15s ease, color 0.15s ease;
}

.task-center-clear {
  width: 2rem;
  height: 2rem;
}

.task-icon-button {
  width: 1.7rem;
  height: 1.7rem;
}

.task-center-clear:hover,
.task-icon-button:hover {
  background: rgb(241 245 249);
  color: rgb(15 23 42);
}

.dark .task-center-clear,
.dark .task-icon-button {
  color: rgb(148 163 184);
}

.dark .task-center-clear:hover,
.dark .task-icon-button:hover {
  background: rgb(30 41 59);
  color: white;
}

.task-center-empty {
  padding: 2.25rem 1rem;
  text-align: center;
  font-size: 0.875rem;
  color: rgb(100 116 139);
}

.dark .task-center-empty {
  color: rgb(148 163 184);
}

.task-center-list {
  max-height: min(29rem, calc(100vh - 7rem));
  overflow-y: auto;
  padding: 0.65rem;
}

.task-item {
  border: 1px solid rgb(226 232 240);
  border-radius: 0.75rem;
  padding: 0.75rem;
}

.task-item + .task-item {
  margin-top: 0.55rem;
}

.dark .task-item {
  border-color: rgb(51 65 85);
  background: rgb(15 23 42 / 0.5);
}

.task-item-top,
.task-item-title-wrap,
.task-item-actions,
.task-item-meta {
  display: flex;
  align-items: center;
}

.task-item-top {
  justify-content: space-between;
  gap: 0.75rem;
}

.task-item-title-wrap {
  min-width: 0;
  gap: 0.45rem;
}

.task-item-icon {
  flex-shrink: 0;
  color: rgb(20 184 166);
}

.task-item-title {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.86rem;
  font-weight: 800;
  color: rgb(30 41 59);
}

.dark .task-item-title {
  color: rgb(226 232 240);
}

.task-item-actions {
  flex-shrink: 0;
  gap: 0.25rem;
}

.task-status {
  border-radius: 999px;
  padding: 0.18rem 0.45rem;
  font-size: 0.68rem;
  font-weight: 800;
}

.task-status-running {
  background: rgb(20 184 166 / 0.13);
  color: rgb(13 148 136);
}

.task-status-success {
  background: rgb(34 197 94 / 0.13);
  color: rgb(22 163 74);
}

.task-status-warning {
  background: rgb(245 158 11 / 0.14);
  color: rgb(217 119 6);
}

.task-status-error {
  background: rgb(239 68 68 / 0.13);
  color: rgb(220 38 38);
}

.task-item-message {
  margin-top: 0.45rem;
  min-height: 1.1rem;
  overflow: hidden;
  color: rgb(100 116 139);
  font-size: 0.76rem;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dark .task-item-message {
  color: rgb(148 163 184);
}

.task-progress-track {
  margin-top: 0.55rem;
  height: 0.45rem;
  overflow: hidden;
  border-radius: 999px;
  background: rgb(226 232 240);
}

.dark .task-progress-track {
  background: rgb(51 65 85);
}

.task-progress-bar {
  height: 100%;
  min-width: 0.35rem;
  border-radius: inherit;
  transition: width 0.25s ease;
}

.task-progress-running {
  background: rgb(20 184 166);
}

.task-progress-success {
  background: rgb(34 197 94);
}

.task-progress-warning {
  background: rgb(245 158 11);
}

.task-progress-error {
  background: rgb(239 68 68);
}

.task-item-meta {
  justify-content: space-between;
  margin-top: 0.35rem;
  color: rgb(100 116 139);
  font-size: 0.72rem;
  font-weight: 800;
}

.dark .task-item-meta {
  color: rgb(148 163 184);
}

@media (max-width: 767px) {
  .task-center-panel-sidebar {
    left: 80px;
    bottom: 6rem;
    width: min(22rem, calc(100vw - 92px));
  }
}
</style>
