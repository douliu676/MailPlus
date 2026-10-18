import { setTimeout as delay } from 'node:timers/promises'

export async function waitFor(check, description, timeout = 15_000) {
  const deadline = Date.now() + timeout
  while (Date.now() < deadline) {
    if (await check()) return
    await delay(100)
  }
  throw new Error('Timed out waiting for ' + description)
}

export async function connectDebugger(url) {
  const socket = new WebSocket(url)
  const pending = new Map()
  const exceptions = []
  let nextID = 0
  await new Promise((yes, no) => {
    socket.addEventListener('open', yes, { once: true })
    socket.addEventListener('error', no, { once: true })
  })
  socket.addEventListener('message', (event) => {
    const message = JSON.parse(String(event.data))
    if (message.method === 'Runtime.exceptionThrown') exceptions.push(message.params.exceptionDetails.text)
    const waiting = pending.get(message.id)
    if (!waiting) return
    pending.delete(message.id)
    clearTimeout(waiting.timer)
    if (message.error) waiting.reject(new Error(message.error.message))
    else waiting.resolve(message.result)
  })
  const send = (method, params = {}, sessionId) => new Promise((yes, no) => {
    const id = ++nextID
    const timer = setTimeout(() => { pending.delete(id); no(new Error('CDP timeout: ' + method)) }, 10_000)
    pending.set(id, { resolve: yes, reject: no, timer })
    socket.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }))
  })
  const evaluate = async (sessionId, expression) => {
    const result = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true }, sessionId)
    if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails))
    return result.result.value
  }
  return {
    send, evaluate, exceptions,
    async page(url) {
      const { targetId } = await send('Target.createTarget', { url })
      const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true })
      await send('Runtime.enable', {}, sessionId)
      return sessionId
    },
    close() {
      for (const waiter of pending.values()) { clearTimeout(waiter.timer); waiter.reject(new Error('Debugger closed')) }
      pending.clear()
      socket.close()
    },
  }
}
