// Polaris "hello" plugin: a complete, dependency-free reference plugin.
//   - serves its manifest
//   - receives credentials at install (/polaris/install) and config pushes
//   - verifies signed webhook events (/polaris/events)
//   - shows an admin page inside the panel (token = HS256 JWT signed with the plugin secret)
//   - adds a user-center link that renews the visitor's account through the panel API
//
//   PORT=9100 node server.js
'use strict'
const http = require('http')
const crypto = require('crypto')
const fs = require('fs')

const PORT = Number(process.env.PORT || 9100)
const STATE_FILE = process.env.STATE_FILE || './state.json'
const INSTALL_KEY = process.env.INSTALL_KEY || '' // optional: require X-Polaris-Install-Key

// ---- state (credentials the panel gave us at install) ----
let state = { panel: null, api_base: '', token: '', plugin_secret: '', webhook_secret: '', settings: {}, events: [] }
try { state = { ...state, ...JSON.parse(fs.readFileSync(STATE_FILE, 'utf8')) } } catch { /* first run */ }
const save = () => fs.writeFileSync(STATE_FILE, JSON.stringify(state, null, 2))

const manifest = JSON.parse(fs.readFileSync(require('path').join(__dirname, 'polaris-plugin.json'), 'utf8'))

// ---- helpers ----
const hmac = (key, data) => crypto.createHmac('sha256', key).update(data).digest('hex')
const safeEq = (a, b) => a.length === b.length && crypto.timingSafeEqual(Buffer.from(a), Buffer.from(b))

// X-Polaris-Signature: t=<unix>,v1=<hex HMAC-SHA256(secret, "<t>.<body>")>
function verifySignature(header, secret, raw) {
  const m = /^t=(\d+),v1=([0-9a-f]{64})$/.exec(header || '')
  if (!m || !secret || Math.abs(Date.now() / 1000 - Number(m[1])) > 300) return false
  return safeEq(hmac(secret, `${m[1]}.${raw}`), m[2])
}

// HS256 JWT issued by the panel for embedded pages (scope "admin") and
// user-center links (scope "user").
function verifyToken(tok, scope) {
  const parts = String(tok || '').split('.')
  if (parts.length !== 3 || !state.plugin_secret) return null
  const sig = crypto.createHmac('sha256', state.plugin_secret).update(parts[0] + '.' + parts[1]).digest('base64url')
  if (!safeEq(sig, parts[2])) return null
  const c = JSON.parse(Buffer.from(parts[1], 'base64url').toString())
  if (c.iss !== 'polaris' || c.aud !== manifest.id || c.scope !== scope || c.exp * 1000 < Date.now()) return null
  return c
}

async function panel(method, path, body, extra = {}) {
  const r = await fetch(state.api_base + path, {
    method, headers: { Authorization: 'Bearer ' + state.token, 'Content-Type': 'application/json', ...extra },
    body: body ? JSON.stringify(body) : undefined,
  })
  const text = await r.text()
  if (!r.ok) throw new Error(`panel ${r.status}: ${text}`)
  return text ? JSON.parse(text) : null
}

const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]))
const page = (title, body) => `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>${esc(title)}</title><style>body{font:14px system-ui,sans-serif;margin:0;padding:20px;color:#111}
table{border-collapse:collapse;width:100%}td,th{border-bottom:1px solid #ddd;padding:6px 8px;text-align:left}
.btn{background:#0e7490;color:#fff;border:0;border-radius:8px;padding:9px 16px;font-size:14px;cursor:pointer}
.mute{color:#666}code{background:#f3f4f6;padding:1px 5px;border-radius:4px}</style>${body}`

const readBody = (req) => new Promise((ok, fail) => {
  const chunks = []; let n = 0
  req.on('data', (c) => { n += c.length; if (n > 1 << 20) { req.destroy(); fail(new Error('too large')) } else chunks.push(c) })
  req.on('end', () => ok(Buffer.concat(chunks).toString())); req.on('error', fail)
})

// ---- routes ----
const routes = {
  'GET /polaris-plugin.json': async (req, res) => json(res, 200, manifest),

  // The panel hands over credentials. Without an install key anyone who can
  // reach this URL could take over a fresh plugin, so set INSTALL_KEY in production.
  'POST /polaris/install': async (req, res, raw) => {
    if (INSTALL_KEY && req.headers['x-polaris-install-key'] !== INSTALL_KEY) return json(res, 403, { error: 'bad install key' })
    const b = JSON.parse(raw)
    if (!verifySignature(req.headers['x-polaris-signature'], b.plugin_secret, raw)) return json(res, 401, { error: 'bad signature' })
    state = { ...state, panel: b.panel_url, api_base: b.api_base, token: b.token, plugin_secret: b.plugin_secret, webhook_secret: b.webhook_secret || '', settings: b.settings || {} }
    save(); console.log('installed by panel', b.panel_version)
    res.writeHead(204).end()
  },

  'POST /polaris/config': async (req, res, raw) => {
    if (!verifySignature(req.headers['x-polaris-signature'], state.plugin_secret, raw)) return json(res, 401, { error: 'bad signature' })
    state.settings = JSON.parse(raw).settings || {}; save(); res.writeHead(204).end()
  },

  'GET /polaris/health': async (req, res) => res.writeHead(204).end(),

  'POST /polaris/uninstall': async (req, res, raw) => {
    if (!verifySignature(req.headers['x-polaris-signature'], state.plugin_secret, raw)) return json(res, 401, { error: 'bad signature' })
    state = { ...state, token: '', plugin_secret: '', webhook_secret: '', events: [] }; save(); res.writeHead(204).end()
  },

  // Events: {id, type, created_at, data}. Answer 2xx fast; the panel retries otherwise.
  'POST /polaris/events': async (req, res, raw) => {
    if (!verifySignature(req.headers['x-polaris-signature'], state.webhook_secret, raw)) return json(res, 401, { error: 'bad signature' })
    const e = JSON.parse(raw)
    if (!state.events.some((x) => x.id === e.id)) { state.events.unshift(e); state.events.length = Math.min(state.events.length, 50); save() } // dedupe: delivery is at-least-once
    res.writeHead(204).end()
  },

  // Admin page embedded in the panel.
  'GET /admin/events': async (req, res, raw, url) => {
    const c = verifyToken(url.searchParams.get('polaris_token'), 'admin')
    if (!c) return html(res, 401, page('未授权', '<p>请从面板打开此页面。</p>'))
    let users = '?'
    try { const r = await panel('GET', '/users'); users = (r.items ?? r).length } catch (e) { users = e.message }
    const rows = state.events.map((e) => `<tr><td>${esc(e.created_at)}</td><td><code>${esc(e.type)}</code></td><td>${esc(e.data?.user?.username ?? e.data?.message ?? '')}</td></tr>`).join('')
    html(res, 200, page('Hello 事件', `<h2>${esc(state.settings.greeting || '你好')}，${esc(c.name)}</h2>
      <p class="mute">面板里共有 ${esc(users)} 个用户（通过插件令牌读取）。最近收到的事件：</p>
      <table><tr><th>时间</th><th>类型</th><th>内容</th></tr>${rows || '<tr><td colspan=3 class="mute">还没有事件</td></tr>'}</table>`))
  },

  // User-center link: shows the visitor and renews them on request.
  'GET /shop': async (req, res, raw, url) => {
    const tok = url.searchParams.get('polaris_token')
    const c = verifyToken(tok, 'user')
    if (!c) return html(res, 401, page('未授权', '<p>请从用户中心打开此页面。</p>'))
    html(res, 200, page('续期', `<h2>${esc(state.settings.greeting || '你好')}，${esc(c.name)}</h2>
      <p>点击下面的按钮把你的账号续期 ${esc(state.settings.renew_days || 30)} 天（演示：不收费）。</p>
      <form method="post" action="/shop/renew"><input type="hidden" name="polaris_token" value="${esc(tok)}">
      <button class="btn">续期</button></form>`))
  },
  'POST /shop/renew': async (req, res, raw) => {
    const tok = new URLSearchParams(raw).get('polaris_token')
    const c = verifyToken(tok, 'user')
    if (!c) return html(res, 401, page('未授权', '<p>链接已过期，请回到用户中心重新打开。</p>'))
    // Idempotency-Key: a double click or a retried callback renews only once.
    const key = 'hello-' + crypto.createHash('sha256').update(tok).digest('hex').slice(0, 32)
    const u = await panel('POST', `/users/${encodeURIComponent(c.sub)}/renew`, { days: Number(state.settings.renew_days || 30) }, { 'Idempotency-Key': key })
    html(res, 200, page('完成', `<h2>已续期</h2><p>新的到期时间：${esc(u.expireAt || '不限期')}</p>`))
  },
}

const json = (res, code, v) => { res.writeHead(code, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(v)) }
const html = (res, code, s) => { res.writeHead(code, { 'Content-Type': 'text/html; charset=utf-8' }); res.end(s) }

http.createServer(async (req, res) => {
  const url = new URL(req.url, 'http://x')
  const h = routes[`${req.method} ${url.pathname}`]
  if (!h) return json(res, 404, { error: 'not found' })
  try { await h(req, res, req.method === 'GET' ? '' : await readBody(req), url) } catch (e) { console.error(e); if (!res.headersSent) json(res, 500, { error: e.message }) }
}).listen(PORT, () => console.log('hello plugin on :' + PORT))
