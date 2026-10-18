import { ref } from 'vue'
import type { QuickMailIdentity, QuickMailMessage, QuickMailMode, QuickMailReceivePayload, QuickMailReceiveResult } from '../api/quickMail'

type QuickMailQuery = QuickMailReceivePayload & { mode: QuickMailMode }
type QuickMailTransport = {
  receive(mode: QuickMailMode, payload: QuickMailReceivePayload, signal: AbortSignal): Promise<QuickMailReceiveResult>
  detail(mode: QuickMailMode, identity: QuickMailIdentity, message: QuickMailMessage, signal: AbortSignal): Promise<QuickMailMessage>
}
type CacheResult = { total: number; cached: number; failed: number }
type DetailJob = {
  message: QuickMailMessage
  attempts: number
  state: 'queued' | 'running' | 'complete' | 'failed'
  waiters: Array<() => void>
}
type DetailRun = {
  generation: number
  query: QuickMailQuery
  jobs: Map<string, DetailJob>
  queue: string[]
  pending: Map<string, AbortController>
  notify: boolean
  notified: boolean
}

const detailConcurrency = 2
const detailAttempts = 3 // Initial attempt plus at most two automatic retries.

// A batch owns its credentials, queue and notifications. Closing a message only
// closes the viewer; cancelling a batch aborts every request and settles waiters.
export function useQuickMailReader(transport: QuickMailTransport, options: {
  onCacheComplete?: (result: CacheResult) => void
} = {}) {
  const messages = ref<QuickMailMessage[]>([])
  const selectedMessage = ref<QuickMailMessage | null>(null)
  const lastQueryEmail = ref('')
  const fetching = ref(false)
  const detailLoading = ref(false)
  const detailError = ref('')
  let generation = 0
  let query: QuickMailQuery | null = null
  let receiveController: AbortController | null = null
  let detailRun: DetailRun | null = null

  function isCurrent(run: DetailRun) {
    return detailRun === run && run.generation === generation
  }

  function settle(job: DetailJob) {
    for (const resolve of job.waiters.splice(0)) resolve()
  }

  function closeDetail() {
    selectedMessage.value = null
    detailLoading.value = false
    detailError.value = ''
  }

  function cancelReceive() {
    generation++
    receiveController?.abort()
    receiveController = null
    const cancelled = detailRun
    detailRun = null
    if (cancelled) {
      cancelled.queue = []
      for (const controller of cancelled.pending.values()) controller.abort()
      for (const job of cancelled.jobs.values()) settle(job)
    }
    fetching.value = false
    closeDetail()
  }

  function reset() {
    cancelReceive()
    messages.value = []
    lastQueryEmail.value = ''
    query = null
  }

  function createRun(snapshot: QuickMailQuery, notify: boolean): DetailRun {
    const run: DetailRun = {
      generation, query: { ...snapshot }, jobs: new Map(), queue: [],
      pending: new Map(), notify, notified: false,
    }
    detailRun = run
    return run
  }

  function enqueue(run: DetailRun, message: QuickMailMessage, priority = false) {
    let job = run.jobs.get(message.id)
    if (!job) {
      job = { message, attempts: 0, state: 'queued', waiters: [] }
      run.jobs.set(message.id, job)
    } else if (job.state === 'failed' && priority) {
      // An explicit click can retry a message after automatic attempts ended.
      job.attempts = 0
      job.state = 'queued'
    }
    if (job.state === 'queued') {
      run.queue = run.queue.filter((id) => id !== message.id)
      if (priority) run.queue.unshift(message.id)
      else run.queue.push(message.id)
    }
    return job
  }

  function notifyComplete(run: DetailRun) {
    if (!run.notify || run.notified || messages.value.length === 0) return
    run.notified = true
    // Deliver after the list response is handled, including complete-body
    // responses from older backends. A reset also suppresses queued notices.
    queueMicrotask(() => queueMicrotask(() => {
      if (!isCurrent(run)) return
      if (run.pending.size || run.queue.length) {
        run.notified = false
        return
      }
      const total = messages.value.length
      const cached = messages.value.filter((message) => message.body_loaded).length
      options.onCacheComplete?.({ total, cached, failed: total - cached })
    }))
  }

  function pump(run: DetailRun) {
    if (!isCurrent(run)) return
    while (run.pending.size < detailConcurrency && run.queue.length > 0) {
      const id = run.queue.shift()!
      const job = run.jobs.get(id)
      if (!job || job.state !== 'queued') continue
      const controller = new AbortController()
      job.state = 'running'
      job.attempts++
      run.pending.set(id, controller)
      void loadDetail(run, job, controller)
    }
    if (run.pending.size === 0 && run.queue.length === 0) notifyComplete(run)
  }

  async function loadDetail(run: DetailRun, job: DetailJob, controller: AbortController) {
    const cached = job.message
    try {
      const detail = await transport.detail(run.query.mode, {
        email: run.query.email, admin_key: run.query.admin_key,
      }, cached, controller.signal)
      if (!isCurrent(run) || controller.signal.aborted) return
      const complete = { ...cached, ...detail, id: cached.id, folder: cached.folder, body_loaded: true }
      messages.value = messages.value.map((item) => item.id === cached.id ? complete : item)
      job.state = 'complete'
      if (selectedMessage.value?.id === cached.id) {
        selectedMessage.value = complete
        detailLoading.value = false
        detailError.value = ''
      }
      settle(job)
    } catch (error) {
      if (!isCurrent(run) || controller.signal.aborted) return
      if (job.attempts < detailAttempts) {
        // Failed requests go behind untouched messages, without stalling them.
        job.state = 'queued'
        run.queue.push(cached.id)
      } else {
        job.state = 'failed'
        if (selectedMessage.value?.id === cached.id) {
          detailLoading.value = false
          detailError.value = error instanceof Error ? error.message : '读取正文失败，请重试'
        }
        settle(job)
      }
    } finally {
      run.pending.delete(cached.id)
      pump(run)
    }
  }

  async function receive(nextQuery: QuickMailQuery) {
    cancelReceive()
    const current = generation
    const snapshot = { ...nextQuery }
    const controller = new AbortController()
    receiveController = controller
    fetching.value = true
    try {
      const result = await transport.receive(snapshot.mode, {
        email: snapshot.email, limit: snapshot.limit, admin_key: snapshot.admin_key,
      }, controller.signal)
      if (current !== generation || controller.signal.aborted) return null
      const unique = new Map<string, QuickMailMessage>()
      for (const message of [...(result.inbox || []), ...(result.trash || [])]) {
        unique.set(message.id, {
          ...message,
          folder: message.folder === 'trash' ? 'trash' : 'inbox',
          body: message.body || message.body_preview || '',
          html: message.html || '',
          // Older backends return complete messages without this flag.
          body_loaded: message.body_loaded ?? true,
        })
      }
      closeDetail()
      messages.value = [...unique.values()].sort((a, b) => (Number(b.timestamp) || 0) - (Number(a.timestamp) || 0))
      query = snapshot
      lastQueryEmail.value = snapshot.email
      const run = createRun(snapshot, true)
      for (const message of messages.value) {
        if (!message.body_loaded) enqueue(run, message)
      }
      queueMicrotask(() => pump(run))
      return result
    } catch (error) {
      if (current !== generation || controller.signal.aborted) return null
      throw error
    } finally {
      if (receiveController === controller) {
        receiveController = null
        fetching.value = false
      }
    }
  }

  async function select(message: QuickMailMessage) {
    if (!query || fetching.value) return
    const cached = messages.value.find((item) => item.id === message.id)
    if (!cached) return
    selectedMessage.value = cached
    detailError.value = ''
    detailLoading.value = !cached.body_loaded
    if (cached.body_loaded) return
    const run = detailRun || createRun(query, false)
    const job = enqueue(run, cached, true)
    const completion = new Promise<void>((resolve) => job.waiters.push(resolve))
    pump(run)
    await completion
  }

  return { messages, selectedMessage, lastQueryEmail, fetching, detailLoading, detailError, receive, select, closeDetail, cancelReceive, reset }
}
