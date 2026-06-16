const http = require('http')
const https = require('https')
const fs = require('fs')
const path = require('path')
const { URL } = require('url')

const host = process.env.FRONTEND_HOST || '0.0.0.0'
const port = Number(process.env.FRONTEND_PORT || 4399)
const webRoot = process.env.WEB_ROOT || path.join(__dirname, 'web')
const backendURL = new URL(process.env.BACKEND_URL || 'http://127.0.0.1:4400')

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

function proxyRequest(req, res, rewritePath) {
  const target = new URL(req.url, backendURL)
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
    res.writeHead(upstreamRes.statusCode || 502, upstreamRes.headers)
    upstreamRes.pipe(res)
  })

  upstream.on('error', (error) => {
    res.writeHead(502, { 'content-type': 'text/plain; charset=utf-8' })
    res.end(`Bad Gateway: ${error.message}`)
  })

  req.pipe(upstream)
}

function sendFile(res, filePath) {
  fs.stat(filePath, (statError, stat) => {
    if (statError || !stat.isFile()) {
      sendIndex(res)
      return
    }

    const ext = path.extname(filePath).toLowerCase()
    res.writeHead(200, {
      'content-type': mimeTypes[ext] || 'application/octet-stream',
      'content-length': stat.size,
    })
    fs.createReadStream(filePath).pipe(res)
  })
}

function sendIndex(res) {
  const indexPath = path.join(webRoot, 'index.html')
  fs.readFile(indexPath, (error, content) => {
    if (error) {
      res.writeHead(404, { 'content-type': 'text/plain; charset=utf-8' })
      res.end('index.html not found')
      return
    }
    res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' })
    res.end(content)
  })
}

function staticPath(pathname) {
  let decoded = pathname
  try {
    decoded = decodeURIComponent(pathname)
  } catch {
    decoded = '/'
  }
  const normalized = path.normalize(decoded).replace(/^(\.\.[/\\])+/, '')
  return path.join(webRoot, normalized)
}

const server = http.createServer((req, res) => {
  const requestURL = new URL(req.url, `http://${req.headers.host || 'localhost'}`)

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
    res.writeHead(405, { 'content-type': 'text/plain; charset=utf-8' })
    res.end('Method Not Allowed')
    return
  }

  sendFile(res, staticPath(requestURL.pathname))
})

server.listen(port, host, () => {
  console.log(`frontend listening on ${host}:${port}`)
  console.log(`proxy backend ${backendURL.href}`)
})
