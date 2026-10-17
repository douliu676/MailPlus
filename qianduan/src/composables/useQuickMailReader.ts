import { ref } from 'vue'
import type { QuickMailIdentity, QuickMailMessage, QuickMailMode, QuickMailReceivePayload, QuickMailReceiveResult } from '../api/quickMail'

type QuickMailQuery = QuickMailReceivePayload & { mode: QuickMailMode }
type QuickMailTransport = {
  receive(mode: QuickMailMode, payload: QuickMailReceivePayload, signal: AbortSignal): Promise<QuickMailReceiveResult>
  detail(mode: QuickMailMode, identity: QuickMailIdentity, message: QuickMailMessage, signal: AbortSignal): Promise<QuickMailMessage>
}

// Keep results and credentials scoped to the successful query, not the editable form.
export function useQuickMailReader(transport: QuickMailTransport) {
  const messages = ref<QuickMailMessage[]>([])
  const selectedMessage = ref<QuickMailMessage | null>(null)
  const lastQueryEmail = ref('')
  const fetching = ref(false)
  const detailLoading = ref(false)
  const detailError = ref('')
  let generation = 0
  let selection = 0
  let query: QuickMailQuery | null = null
  let receiveController: AbortController | null = null
  const pendingDetails = new Map<string, { controller: AbortController; promise: Promise<QuickMailMessage> }>()

  function abortDetails(exceptID?: string) {
    for (const [id, pending] of pendingDetails) {
      if (id === exceptID) continue
      pending.controller.abort()
      pendingDetails.delete(id)
    }
  }

  function closeDetail() {
    selection++
    abortDetails()
    selectedMessage.value = null
    detailLoading.value = false
    detailError.value = ''
  }

  function cancelReceive() {
    generation++
    receiveController?.abort()
    receiveController = null
    fetching.value = false
    closeDetail()
  }

  function reset() {
    cancelReceive()
    messages.value = []
    lastQueryEmail.value = ''
    query = null
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
      messages.value = [...unique.values()]
      query = snapshot
      lastQueryEmail.value = snapshot.email
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
    if (!query) return
    const current = generation
    const currentSelection = ++selection
    const snapshot = { ...query }
    const cached = messages.value.find((item) => item.id === message.id)
    if (!cached) return
    abortDetails(cached.id)
    selectedMessage.value = cached
    detailError.value = ''
    detailLoading.value = !cached.body_loaded
    if (cached.body_loaded) return
    let pending = pendingDetails.get(cached.id)
    if (!pending) {
      const controller = new AbortController()
      pending = {
        controller,
        promise: transport.detail(snapshot.mode, {
          email: snapshot.email, admin_key: snapshot.admin_key,
        }, cached, controller.signal),
      }
      pendingDetails.set(cached.id, pending)
    }
    try {
      const detail = await pending.promise
      if (current !== generation || pending.controller.signal.aborted) return
      const complete = { ...cached, ...detail, id: cached.id, body_loaded: true }
      messages.value = messages.value.map((item) => item.id === cached.id ? complete : item)
      if (currentSelection === selection) selectedMessage.value = complete
    } catch (error) {
      if (current === generation && currentSelection === selection && !pending.controller.signal.aborted) {
        detailError.value = error instanceof Error ? error.message : '读取正文失败，请重试'
      }
    } finally {
      if (pendingDetails.get(cached.id) === pending) pendingDetails.delete(cached.id)
      if (current === generation && currentSelection === selection) detailLoading.value = false
    }
  }

  return { messages, selectedMessage, lastQueryEmail, fetching, detailLoading, detailError, receive, select, closeDetail, cancelReceive, reset }
}
