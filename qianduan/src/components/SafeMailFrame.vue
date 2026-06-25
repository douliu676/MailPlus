<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { sanitizeMailHtml } from '../utils/sanitizeMailHtml'

const props = withDefaults(defineProps<{
  html?: string
  text?: string
  title?: string
  minHeight?: number
}>(), {
  html: '',
  text: '',
  title: '邮件正文',
  minHeight: 800,
})

const frameRef = ref<HTMLIFrameElement | null>(null)
const frameHeight = ref(props.minHeight)
let resizeObserver: ResizeObserver | null = null
let resizeTimers: number[] = []

function escapeHTML(value: string) {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#039;')
}

function linkifyText(value: string) {
  const escaped = escapeHTML(value || '')
  return escaped
    .replace(/(https?:\/\/[^\s<]+)/g, '<a href="$1" target="_blank" rel="noopener noreferrer">$1</a>')
    .replace(/\n/g, '<br />')
}

const bodyHTML = computed(() => {
  const html = sanitizeMailHtml(props.html || '').trim()
  if (html) return html
  return linkifyText(props.text || '')
})

const hasContent = computed(() => bodyHTML.value.trim().length > 0)
const contentKey = computed(() => {
  const value = `${props.title || ''}\n${props.html || ''}\n${props.text || ''}`
  let hash = 0
  for (let index = 0; index < value.length; index += 1) {
    hash = ((hash << 5) - hash + value.charCodeAt(index)) | 0
  }
  return `${value.length}-${hash}`
})

const frameSrcdoc = computed(() => `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <base target="_blank">
  <style>
    html, body {
      margin: 0;
      padding: 0;
      background: #ffffff;
      color: #111827;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Arial, "Microsoft YaHei", sans-serif;
      font-size: 14px;
      line-height: 1.65;
      word-break: normal;
      overflow-wrap: anywhere;
      overflow: visible !important;
    }

    img, video, canvas {
      max-width: 100% !important;
      height: auto;
    }

    table {
      max-width: 100%;
    }

    pre, code, td, th {
      overflow-wrap: anywhere;
    }

    pre {
      white-space: pre-wrap;
    }

    a {
      color: #2563eb;
      text-decoration: underline;
      text-underline-offset: 2px;
    }
  </style>
</head>
<body>${bodyHTML.value}</body>
</html>`)

function clearFrameWatchers() {
  resizeObserver?.disconnect()
  resizeObserver = null
  resizeTimers.forEach((timer) => window.clearTimeout(timer))
  resizeTimers = []
}

function getFrameContentHeight(frame: HTMLIFrameElement) {
  const doc = frame.contentDocument
  const body = doc?.body
  const html = doc?.documentElement
  if (!doc || !body || !html) return props.minHeight

  let maxBottom = 0
  const bodyRect = body.getBoundingClientRect()
  doc.body.querySelectorAll('*').forEach((node) => {
    const rect = (node as HTMLElement).getBoundingClientRect()
    maxBottom = Math.max(maxBottom, rect.bottom - bodyRect.top)
  })

  return Math.ceil(Math.max(
    props.minHeight,
    body.scrollHeight,
    body.offsetHeight,
    html.clientHeight,
    html.scrollHeight,
    html.offsetHeight,
    maxBottom,
  ))
}

function resizeFrame() {
  const frame = frameRef.value
  if (!frame) return

  try {
    frameHeight.value = getFrameContentHeight(frame) + 2
  } catch {
    frameHeight.value = Math.max(props.minHeight, 520)
  }
}

function queueResize() {
  nextTick(() => {
    resizeFrame()
    resizeTimers.push(window.setTimeout(resizeFrame, 80))
    resizeTimers.push(window.setTimeout(resizeFrame, 300))
    resizeTimers.push(window.setTimeout(resizeFrame, 1000))
  })
}

function handleLoad(event: Event) {
  const frame = event.target as HTMLIFrameElement | null
  frame?.contentWindow?.scrollTo(0, 0)
  clearFrameWatchers()
  frameHeight.value = props.minHeight
  queueResize()

  try {
    const doc = frame?.contentDocument
    if (!doc?.body || !doc.documentElement) return
    resizeObserver = new ResizeObserver(queueResize)
    resizeObserver.observe(doc.body)
    resizeObserver.observe(doc.documentElement)
    doc.querySelectorAll('img').forEach((image) => {
      image.addEventListener('load', queueResize, { once: true })
      image.addEventListener('error', queueResize, { once: true })
    })
  } catch {
    // The iframe uses srcdoc and should be same-origin, but keep rendering even if a browser blocks inspection.
  }
}

watch(contentKey, () => {
  clearFrameWatchers()
  frameHeight.value = props.minHeight
}, { flush: 'sync' })

onBeforeUnmount(clearFrameWatchers)
</script>

<template>
  <div v-if="hasContent" class="safe-mail-viewer-shell">
    <iframe
      :key="contentKey"
      ref="frameRef"
      class="safe-mail-viewer"
      :title="title"
      :srcdoc="frameSrcdoc"
      :style="{ minHeight: `${minHeight}px`, height: `${frameHeight}px` }"
      sandbox="allow-same-origin allow-popups allow-popups-to-escape-sandbox"
      referrerpolicy="no-referrer"
      scrolling="no"
      @load="handleLoad"
    ></iframe>
  </div>
  <div v-else class="safe-mail-empty">这封邮件没有可显示的正文</div>
</template>

<style scoped>
.safe-mail-viewer-shell {
  display: block;
  width: 100%;
}

.safe-mail-viewer {
  box-sizing: border-box;
  width: 100%;
  min-height: 800px;
  max-height: none;
  overflow: hidden;
  border: 1px solid rgb(203 213 225 / 0.85);
  border-radius: 8px;
  background: #fff;
  color: #111827;
  overscroll-behavior: contain;
  display: block;
}

.safe-mail-empty {
  display: grid;
  min-height: 220px;
  place-items: center;
  border: 1px solid rgb(203 213 225 / 0.85);
  border-radius: 8px;
  background: #f8fafc;
  color: #64748b;
  font-size: 14px;
  font-weight: 600;
}
</style>
