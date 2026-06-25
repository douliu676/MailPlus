const http = require('http')
const https = require('https')
const fs = require('fs')
const path = require('path')
const { URL } = require('url')

const host = process.env.FRONTEND_HOST || '0.0.0.0'
const port = Number(process.env.FRONTEND_PORT || 4399)
const webRoot = process.env.WEB_ROOT || path.join(__dirname, 'web')
const backendURL = new URL(process.env.BACKEND_URL || 'http://127.0.0.1:4400')
const requestTimeoutMs = Number(process.env.FRONTEND_REQUEST_TIMEOUT_MS || 30000)
const maxRequestURLLength = Number(process.env.FRONTEND_MAX_URL_LENGTH || 8192)

const mimeTypes = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.jpeg': 'image/jpeg',
  '.gif': 'image/gif',
  '.svg': 'image/svg+xml',
  '.ico': 'image/x-icon',
  '.woff': 'font/woff',
  '.woff2': 'font/woff2',
  '.ttf': 'font/ttf',
}

function isPlainMailPath(pathname) {
  return /^\/mail\/[^/]+\/all(?:\/.*)?$/.test(pathname)
}

function isQuickMailPlainPath(pathname) {
  return /^\/(?:imap|outlook)\/mail\/[^/]+\/all\/[^/]+(?:\/[0-9]+)?$/.test(pathname)
}

function parseRequestURL(req, baseURL) {
  if (!req.url) {
    return { error: 'bad-request', url: null }
  }
  if (req.url.length > maxRequestURLLength) {
    return { error: 'uri-too-long', url: null }
  }

  try {
    return { error: null, url: new URL(req.url, baseURL) }
  } catch (error) {
    console.warn('frontend invalid request URL:', req.url, error.message)
    return { error: 'bad-request', url: null }
  }
}

function sendText(res, statusCode, message) {
  if (res.writableEnded) return
  res.writeHead(statusCode, { 'content-type': 'text/plain; charset=utf-8' })
  res.end(message)
}

function sendJSON(res, statusCode, body) {
  if (res.writableEnded) return
  res.writeHead(statusCode, { 'content-type': 'application/json; charset=utf-8' })
  res.end(JSON.stringify(body))
}

function drainRequest(req) {
  req.resume()
}

function sendURLParseError(req, res, error) {
  drainRequest(req)
  if (error === 'uri-too-long') {
    sendText(res, 414, 'URI Too Long')
    return
  }
  sendText(res, 400, 'Bad Request')
}

function proxyRequest(req, res, rewritePath) {
  const parsed = parseRequestURL(req, backendURL)
  if (parsed.error || !parsed.url) {
    sendURLParseError(req, res, parsed.error)
    return
  }

  const target = parsed.url
  if (rewritePath) {
    target.pathname = rewritePath(target.pathname)
  }

  const headers = { ...req.headers, host: backendURL.host }
  const options = {
    protocol: backendURL.protocol,
    hostname: backendURL.hostname,
    port: backendURL.port || (backendURL.protocol === 'https:' ? 443 : 80),
    method: req.method,
    path: `${target.pathname}${target.search}`,
    headers,
  }

  const client = backendURL.protocol === 'https:' ? https : http
  const upstream = client.request(options, (upstreamRes) => {
    if (res.headersSent || res.writableEnded) {
      upstreamRes.resume()
      return
    }

    res.writeHead(upstreamRes.statusCode || 502, upstreamRes.headers)
    upstreamRes.on('error', (error) => {
      console.error('frontend proxy response error:', error.message)
      if (!res.writableEnded) {
        res.destroy(error)
      }
    })
    upstreamRes.pipe(res)
  })

  upstream.on('timeout', () => {
    upstream.destroy(new Error('backend request timeout'))
  })

  upstream.on('error', (error) => {
    console.error('frontend proxy request error:', error.message)
    if (res.headersSent || res.writableEnded) {
      res.destroy(error)
      return
    }

    const requestPath = req.url || ''
    if (requestPath === '/api/health' || requestPath.startsWith('/api/health?')) {
      sendJSON(res, 503, { code: 503, msg: 'Backend is restarting', data: null })
      return
    }

    sendJSON(res, 502, { code: 502, msg: `Backend service is temporarily unavailable: ${error.message}`, data: null })
  })

  upstream.setTimeout(requestTimeoutMs)

  req.on('aborted', () => {
    upstream.destroy(new Error('client request aborted'))
  })

  req.on('error', (error) => {
    console.error('frontend client request error:', error.message)
    upstream.destroy(error)
  })

  req.pipe(upstream)
}

function sendFile(req, res, filePath) {
  fs.stat(filePath, (statError, stat) => {
    if (statError || !stat.isFile()) {
      sendIndex(req, res)
      return
    }

    const ext = path.extname(filePath).toLowerCase()
    res.writeHead(200, {
      'content-type': mimeTypes[ext] || 'application/octet-stream',
      'content-length': stat.size,
    })

    if (req.method === 'HEAD') {
      res.end()
      return
    }

    const stream = fs.createReadStream(filePath)
    stream.on('error', (error) => {
      console.error('frontend static file error:', error.message)
      if (!res.headersSent) {
        sendText(res, 500, 'Internal Server Error')
        return
      }
      if (!res.writableEnded) {
        res.destroy(error)
      }
    })
    stream.pipe(res)
  })
}

function sendIndex(req, res) {
  const indexPath = path.join(webRoot, 'index.html')
  fs.readFile(indexPath, (error, content) => {
    if (error) {
      sendText(res, 404, 'index.html not found')
      return
    }
    res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' })
    if (req.method === 'HEAD') {
      res.end()
      return
    }
    res.end(content)
  })
}

function staticPath(pathname) {
  let decoded = pathname
  try {
    decoded = decodeURIComponent(pathname)
  } catch (error) {
    console.warn('frontend invalid encoded path:', pathname, error.message)
    decoded = '/'
  }

  const resolvedWebRoot = path.resolve(webRoot)
  const normalized = path.normalize(decoded).replace(/^(\.\.[/\\])+/, '')
  const resolvedPath = path.resolve(resolvedWebRoot, `.${path.sep}${normalized}`)
  if (resolvedPath !== resolvedWebRoot && !resolvedPath.startsWith(`${resolvedWebRoot}${path.sep}`)) {
    return path.join(resolvedWebRoot, 'index.html')
  }
  return resolvedPath
}

const server = http.createServer((req, res) => {
  try {
    const parsed = parseRequestURL(req, `http://${req.headers.host || 'localhost'}`)
    if (parsed.error || !parsed.url) {
      sendURLParseError(req, res, parsed.error)
      return
    }

    const requestURL = parsed.url

    if (requestURL.pathname === '/api' || requestURL.pathname.startsWith('/api/')) {
      proxyRequest(req, res)
      return
    }

    if (isPlainMailPath(requestURL.pathname)) {
      proxyRequest(req, res, (pathname) => pathname.replace(/^\/mail\//, '/api/public/mail/'))
      return
    }

    if (isQuickMailPlainPath(requestURL.pathname)) {
      proxyRequest(req, res)
      return
    }

    if (req.method !== 'GET' && req.method !== 'HEAD') {
      drainRequest(req)
      sendText(res, 405, 'Method Not Allowed')
      return
    }

    sendFile(req, res, staticPath(requestURL.pathname))
  } catch (error) {
    console.error('frontend request handler error:', error)
    if (!res.headersSent && !res.writableEnded) {
      sendText(res, 500, 'Internal Server Error')
    } else if (!res.writableEnded) {
      res.destroy(error)
    }
  }
})

server.on('clientError', (error, socket) => {
  console.warn('frontend client parse error:', error.message)
  if (socket.writable) {
    socket.end('HTTP/1.1 400 Bad Request\r\nConnection: close\r\n\r\n')
  }
})

process.on('uncaughtException', (error) => {
  console.error('frontend uncaught exception:', error)
})

process.on('unhandledRejection', (reason) => {
  console.error('frontend unhandled rejection:', reason)
})

server.listen(port, host, () => {
  console.log(`frontend listening on ${host}:${port}`)
  console.log(`proxy backend ${backendURL.href}`)
})
