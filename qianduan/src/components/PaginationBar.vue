<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { Check, ChevronDown, ChevronLeft, ChevronRight } from 'lucide-vue-next'

type PaginationItem = { key: string; type: 'page'; page: number } | { key: string; type: 'ellipsis' }

const props = withDefaults(defineProps<{
  page: number
  pages?: number
  pageSize: number
  pageSizeOptions: number[]
  total: number
  disabled?: boolean
  showPageSize?: boolean
  showSummary?: boolean
}>(), {
  pages: undefined,
  disabled: false,
  showPageSize: true,
  showSummary: true,
})

const emit = defineEmits<{
  (event: 'page-change', page: number): void
  (event: 'page-size-change', size: number): void
}>()

const rootRef = ref<HTMLElement | null>(null)
const pageSizeDropdownOpen = ref(false)
const pageJump = ref('')

const normalizedPageSize = computed(() => Math.max(1, Math.floor(Number(props.pageSize) || 1)))
const totalPages = computed(() => {
  const explicitPages = Math.floor(Number(props.pages) || 0)
  if (explicitPages > 0) return explicitPages
  const calculatedPages = Math.ceil(Math.max(0, Number(props.total) || 0) / normalizedPageSize.value)
  return Math.max(1, calculatedPages)
})
const currentPage = computed(() => Math.max(1, Math.min(Math.floor(Number(props.page) || 1), totalPages.value)))
const pageStart = computed(() => (props.total <= 0 ? 0 : (currentPage.value - 1) * normalizedPageSize.value + 1))
const pageEnd = computed(() => Math.min(currentPage.value * normalizedPageSize.value, Math.max(0, Number(props.total) || 0)))
const normalizedPageSizeOptions = computed(() => {
  const values = [...props.pageSizeOptions, normalizedPageSize.value]
    .map((item) => Math.floor(Number(item) || 0))
    .filter((item) => item > 0)
  return Array.from(new Set(values)).sort((a, b) => a - b)
})
const paginationItems = computed(() => buildPaginationItems(currentPage.value, totalPages.value))

onMounted(() => {
  document.addEventListener('click', handleDocumentClick)
})

onBeforeUnmount(() => {
  document.removeEventListener('click', handleDocumentClick)
})

function buildPaginationItems(page: number, pages: number): PaginationItem[] {
  const safePages = Math.max(1, Math.floor(Number(pages) || 1))
  const safePage = Math.max(1, Math.min(Math.floor(Number(page) || 1), safePages))
  const items: PaginationItem[] = []
  const addPage = (itemPage: number) => items.push({ key: `page-${itemPage}`, type: 'page', page: itemPage })
  const addEllipsis = (key: string) => items.push({ key, type: 'ellipsis' })

  if (safePages <= 7) {
    for (let itemPage = 1; itemPage <= safePages; itemPage += 1) addPage(itemPage)
    return items
  }

  if (safePage <= 4) {
    for (let itemPage = 1; itemPage <= 4; itemPage += 1) addPage(itemPage)
    addEllipsis('ellipsis-end')
    addPage(safePages)
    return items
  }

  if (safePage >= safePages - 3) {
    addPage(1)
    addEllipsis('ellipsis-start')
    for (let itemPage = safePages - 3; itemPage <= safePages; itemPage += 1) addPage(itemPage)
    return items
  }

  addPage(1)
  addEllipsis('ellipsis-start')
  addPage(safePage - 1)
  addPage(safePage)
  addPage(safePage + 1)
  addEllipsis('ellipsis-end')
  addPage(safePages)
  return items
}

function handleDocumentClick(event: MouseEvent) {
  if (!rootRef.value?.contains(event.target as Node)) {
    pageSizeDropdownOpen.value = false
  }
}

function selectPageSize(size: number) {
  pageSizeDropdownOpen.value = false
  if (props.disabled || size === normalizedPageSize.value) return
  emit('page-size-change', size)
}

function changePage(page: number) {
  if (props.disabled) return
  const nextPage = Math.max(1, Math.min(Math.floor(Number(page) || 1), totalPages.value))
  if (nextPage === currentPage.value) return
  emit('page-change', nextPage)
}

function jumpToPage() {
  const nextPage = Number(pageJump.value)
  if (Number.isFinite(nextPage)) changePage(nextPage)
  pageJump.value = ''
}
</script>

<template>
  <div ref="rootRef" class="pagination-bar">
    <div v-if="showSummary || showPageSize" class="pagination-bar-left">
      <p v-if="showSummary" class="pagination-summary" aria-live="polite">
        <span class="pagination-summary-range">
          显示 <span class="pagination-summary-value">{{ pageStart }}</span> 至
          <span class="pagination-summary-value">{{ pageEnd }}</span>
        </span>
        <span class="pagination-summary-total">
          共 <span class="pagination-summary-value">{{ total }}</span> 条结果
        </span>
      </p>

      <div v-if="showPageSize" class="pagination-page-size">
        <span class="pagination-page-size-label">每页:</span>
        <div class="page-size-select">
          <button
            class="page-size-trigger"
            type="button"
            :disabled="disabled"
            @click.stop="pageSizeDropdownOpen = !pageSizeDropdownOpen"
          >
            <span>{{ normalizedPageSize }}</span>
            <ChevronDown class="h-4 w-4 transition-transform" :class="{ 'rotate-180': pageSizeDropdownOpen }" />
          </button>

          <div v-if="pageSizeDropdownOpen" class="page-size-menu">
            <button
              v-for="size in normalizedPageSizeOptions"
              :key="size"
              class="page-size-option"
              :class="{ 'page-size-option-active': size === normalizedPageSize }"
              type="button"
              @click="selectPageSize(size)"
            >
              <span>{{ size }}</span>
              <Check v-if="size === normalizedPageSize" class="h-4 w-4" />
            </button>
          </div>
        </div>
      </div>
    </div>

    <nav class="compact-pagination" aria-label="分页">
      <button
        class="pagination-arrow-button pagination-arrow-button-prev"
        type="button"
        aria-label="上一页"
        :disabled="disabled || currentPage <= 1"
        @click="changePage(currentPage - 1)"
      >
        <ChevronLeft class="h-4 w-4" aria-hidden="true" />
      </button>
      <template v-for="item in paginationItems" :key="item.key">
        <span v-if="item.type === 'ellipsis'" class="pagination-ellipsis">...</span>
        <button
          v-else
          class="pagination-page-button"
          :class="{ 'pagination-page-button-active': item.page === currentPage }"
          :aria-current="item.page === currentPage ? 'page' : undefined"
          type="button"
          :disabled="disabled"
          @click="changePage(item.page)"
        >
          {{ item.page }}
        </button>
      </template>
      <button
        class="pagination-arrow-button pagination-arrow-button-next"
        type="button"
        aria-label="下一页"
        :disabled="disabled || currentPage >= totalPages"
        @click="changePage(currentPage + 1)"
      >
        <ChevronRight class="h-4 w-4" aria-hidden="true" />
      </button>
      <form class="page-jump-form" @submit.prevent="jumpToPage">
        <input
          v-model.trim="pageJump"
          class="page-jump-input"
          type="text"
          inputmode="numeric"
          pattern="[0-9]*"
          min="1"
          :max="totalPages"
          :placeholder="String(currentPage)"
          aria-label="跳转页码"
          :disabled="disabled"
        />
        <button class="page-jump-button" type="submit" title="跳转页码" :disabled="disabled">
          <ChevronRight class="h-4 w-4" aria-hidden="true" />
        </button>
      </form>
    </nav>
  </div>
</template>

<style scoped>
.pagination-bar {
  --pagination-text: rgb(31 41 55);
  --pagination-muted-text: rgb(71 85 105);
  --pagination-value-text: rgb(15 23 42);
  --pagination-control-bg: rgb(255 255 255);
  --pagination-control-border: rgb(203 213 225);
  --pagination-control-text: rgb(51 65 85);
  --pagination-control-hover-bg: rgb(248 250 252);
  --pagination-disabled-bg: rgb(241 245 249);
  --pagination-disabled-border: rgb(226 232 240);
  --pagination-disabled-text: rgb(148 163 184);
  --pagination-menu-bg: rgb(255 255 255);
  --pagination-menu-border: rgb(226 232 240);
  --pagination-active-border: rgb(20 184 166);
  --pagination-active-bg: rgb(240 253 250);
  --pagination-active-text: rgb(13 148 136);
  --pagination-placeholder-text: rgb(148 163 184);
  --pagination-focus-ring: rgb(20 184 166 / 0.55);
  display: flex;
  width: 100%;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
}

.pagination-bar-left {
  display: flex;
  flex-wrap: wrap;
  min-width: 0;
  align-items: center;
  gap: 0.75rem;
}

.pagination-summary,
.pagination-page-size-label {
  font-size: 0.875rem;
  color: var(--pagination-text);
}

.pagination-summary {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  column-gap: 1.5rem;
  row-gap: 0.35rem;
  white-space: nowrap;
}

.pagination-summary-total {
  color: var(--pagination-muted-text);
}

.pagination-summary-value {
  color: var(--pagination-value-text);
  font-weight: 600;
}

.pagination-page-size {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 0.5rem;
}

.page-size-select {
  position: relative;
  width: 5rem;
}

.page-size-trigger {
  display: inline-flex;
  height: 2.25rem;
  width: 100%;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  border-radius: 0.75rem;
  border: 1px solid var(--pagination-control-border);
  background: var(--pagination-control-bg);
  padding: 0 0.75rem;
  font-size: 0.875rem;
  color: var(--pagination-control-text);
  outline: none;
  transition: border-color 0.15s ease, background-color 0.15s ease, color 0.15s ease, box-shadow 0.15s ease;
}

.page-size-trigger:hover {
  border-color: var(--pagination-active-border);
  background: var(--pagination-control-hover-bg);
  color: var(--pagination-text);
}

.page-size-trigger:focus-visible {
  border-color: var(--pagination-active-border);
  box-shadow: 0 0 0 2px var(--pagination-focus-ring);
}

.page-size-trigger:disabled {
  cursor: not-allowed;
  opacity: 0.62;
}

.page-size-menu {
  position: absolute;
  right: 0;
  bottom: calc(100% + 0.5rem);
  z-index: 60;
  width: 5rem;
  overflow: hidden;
  border: 1px solid var(--pagination-menu-border);
  border-radius: 0.75rem;
  background: var(--pagination-menu-bg);
  box-shadow: 0 18px 42px rgb(15 23 42 / 0.18);
}

.page-size-option {
  display: flex;
  width: 100%;
  height: 2.5rem;
  align-items: center;
  justify-content: space-between;
  padding: 0 0.9rem;
  font-size: 0.875rem;
  color: var(--pagination-control-text);
  transition: background-color 0.15s ease, color 0.15s ease;
}

.page-size-option:hover {
  background: var(--pagination-control-hover-bg);
}

.page-size-option-active {
  background: var(--pagination-active-bg);
  color: var(--pagination-active-text);
  font-weight: 600;
}

.compact-pagination .pagination-arrow-button,
.compact-pagination .pagination-page-button,
.compact-pagination .pagination-ellipsis {
  position: relative;
  display: inline-flex;
  height: 2.25rem;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--pagination-control-border);
  border-color: var(--pagination-control-border);
  background: var(--pagination-control-bg);
  color: var(--pagination-control-text);
  font-size: 0.875rem;
  font-weight: 500;
  line-height: 1;
  outline: none;
  transition: border-color 0.15s ease, background-color 0.15s ease, color 0.15s ease, box-shadow 0.15s ease;
}

.compact-pagination .pagination-arrow-button {
  width: 2.25rem;
  min-width: 2.25rem;
  padding: 0;
}

.compact-pagination .pagination-arrow-button-prev {
  border-radius: 0.5rem 0 0 0.5rem;
}

.compact-pagination .pagination-arrow-button-next {
  margin-left: -1px;
  border-radius: 0 0.5rem 0.5rem 0;
}

.compact-pagination .pagination-page-button,
.compact-pagination .pagination-ellipsis {
  margin-left: -1px;
  min-width: 2.25rem;
  padding: 0 0.7rem;
}

.compact-pagination .pagination-arrow-button:not(:disabled):hover,
.compact-pagination .pagination-page-button:not(:disabled):hover {
  background: var(--pagination-control-hover-bg);
  color: var(--pagination-text);
}

.compact-pagination .pagination-page-button-active,
.compact-pagination .pagination-page-button-active:not(:disabled):hover {
  z-index: 1;
  border-color: var(--pagination-active-border);
  background: var(--pagination-active-bg);
  color: var(--pagination-active-text);
  font-weight: 700;
}

.compact-pagination .pagination-arrow-button:focus-visible,
.compact-pagination .pagination-page-button:focus-visible,
.page-jump-button:focus-visible {
  z-index: 2;
  border-color: var(--pagination-active-border);
  box-shadow: 0 0 0 2px var(--pagination-focus-ring);
}

.compact-pagination .pagination-arrow-button:disabled,
.compact-pagination .pagination-page-button:disabled,
.page-jump-button:disabled {
  cursor: not-allowed;
  border-color: var(--pagination-disabled-border);
  background: var(--pagination-disabled-bg);
  color: var(--pagination-disabled-text);
  opacity: 1;
}

.compact-pagination .pagination-arrow-button:disabled:hover,
.compact-pagination .pagination-page-button:disabled:hover,
.page-jump-button:disabled:hover {
  border-color: var(--pagination-disabled-border);
  background: var(--pagination-disabled-bg);
  color: var(--pagination-disabled-text);
}

.page-jump-form {
  margin-left: 0.35rem;
  display: inline-flex;
  height: 2.25rem;
  align-items: stretch;
}

.page-jump-input {
  width: 3.35rem;
  border: 1px solid var(--pagination-control-border);
  border-right: 0;
  border-radius: 0.5rem 0 0 0.5rem;
  background: var(--pagination-control-bg);
  padding: 0 0.5rem;
  text-align: center;
  font-size: 0.8125rem;
  color: var(--pagination-control-text);
  outline: none;
}

.page-jump-input:focus {
  border-color: var(--pagination-active-border);
  box-shadow: 0 0 0 1px var(--pagination-focus-ring);
}

.page-jump-input::placeholder {
  color: var(--pagination-placeholder-text);
}

.page-jump-button {
  display: inline-flex;
  width: 1.95rem;
  min-width: 1.95rem;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--pagination-control-border);
  border-radius: 0 0.5rem 0.5rem 0;
  background: var(--pagination-control-bg);
  color: var(--pagination-control-text);
  padding: 0;
  font-size: 0.9rem;
  font-weight: 700;
  transition: border-color 0.15s ease, background-color 0.15s ease, color 0.15s ease;
}

.page-jump-button:hover {
  border-color: var(--pagination-active-border);
  background: var(--pagination-active-bg);
  color: var(--pagination-active-text);
}

:global(.dark .pagination-bar),
:global(html.dark .pagination-bar) {
  --pagination-text: rgb(226 232 240);
  --pagination-muted-text: rgb(203 213 225);
  --pagination-value-text: rgb(248 250 252);
  --pagination-control-bg: rgb(15 23 42);
  --pagination-control-border: rgb(71 85 105);
  --pagination-control-text: rgb(203 213 225);
  --pagination-control-hover-bg: rgb(30 41 59);
  --pagination-disabled-bg: rgb(15 23 42 / 0.42);
  --pagination-disabled-border: rgb(51 65 85 / 0.72);
  --pagination-disabled-text: rgb(100 116 139);
  --pagination-menu-bg: rgb(15 23 42);
  --pagination-menu-border: rgb(71 85 105);
  --pagination-active-border: rgb(45 212 191);
  --pagination-active-bg: rgb(20 184 166 / 0.18);
  --pagination-active-text: rgb(94 234 212);
  --pagination-placeholder-text: rgb(148 163 184 / 0.72);
  --pagination-focus-ring: rgb(20 184 166 / 0.5);
}

@media (max-width: 640px) {
  .pagination-bar {
    align-items: stretch;
    flex-direction: column;
    gap: 0.65rem;
  }

  .pagination-bar-left {
    width: 100%;
    justify-content: space-between;
  }

  .pagination-summary {
    white-space: normal;
  }

  .compact-pagination {
    max-width: 100%;
    overflow-x: auto;
    overflow-y: hidden;
    padding-bottom: 0.1rem;
    scrollbar-width: thin;
  }

  .compact-pagination .pagination-arrow-button,
  .compact-pagination .pagination-page-button,
  .compact-pagination .pagination-ellipsis {
    flex: 0 0 auto;
  }
}

@media (max-width: 420px) {
  .pagination-bar-left {
    align-items: flex-start;
    flex-direction: column;
    gap: 0.5rem;
  }

  .compact-pagination .pagination-page-button,
  .compact-pagination .pagination-ellipsis {
    min-width: 2.05rem;
    padding-right: 0.55rem;
    padding-left: 0.55rem;
  }

  .page-jump-form {
    display: none;
  }
}
</style>
