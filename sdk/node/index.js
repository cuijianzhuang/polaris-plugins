'use strict'
// Polaris plugin SDK for Node.js 18+. No dependencies.
const crypto = require('crypto')

const TOLERANCE = 300 // seconds of clock skew allowed on signatures

const hmacHex = (secret, data) => crypto.createHmac('sha256', secret).update(data).digest('hex')
const safeEqual = (a, b) => {
  const x = Buffer.from(a), y = Buffer.from(b)
  return x.length === y.length && crypto.timingSafeEqual(x, y)
}

/** sign returns the hex HMAC-SHA256(secret, "<ts>.<body>"). body is the raw bytes/string. */
function sign(secret, ts, body) {
  return crypto.createHmac('sha256', secret).update(`${ts}.`).update(body).digest('hex')
}

/** signatureHeader builds the X-Polaris-Signature value for a body. */
function signatureHeader(secret, body, ts = Math.floor(Date.now() / 1000)) {
  return `t=${ts},v1=${sign(secret, ts, body)}`
}

/**
 * verifySignature checks an X-Polaris-Signature header against the RAW request
 * body (do not re-serialise parsed JSON). Returns true/false.
 */
function verifySignature(header, secret, body, { tolerance = TOLERANCE, now = Date.now() / 1000 } = {}) {
  const m = /^t=(\d+),v1=([0-9a-f]{64})$/.exec(header || '')
  if (!m || !secret) return false
  if (Math.abs(now - Number(m[1])) > tolerance) return false
  return safeEqual(sign(secret, m[1], body), m[2])
}

/**
 * verifyIdentity checks the HS256 token the panel puts in ?polaris_token=.
 * scope is "admin" (embedded admin page) or "user" (user-center link).
 * Returns the claims, or null when the token is not acceptable.
 */
function verifyIdentity(token, secret, { audience, scope, now = Date.now() / 1000 } = {}) {
  const parts = String(token || '').split('.')
  if (parts.length !== 3 || !secret) return null
  const [h, p, s] = parts
  if (!safeEqual(crypto.createHmac('sha256', secret).update(`${h}.${p}`).digest('base64url'), s)) return null
  let head, claims
  try {
    head = JSON.parse(Buffer.from(h, 'base64url').toString())
    claims = JSON.parse(Buffer.from(p, 'base64url').toString())
  } catch { return null }
  if (head.alg !== 'HS256') return null
  if (claims.iss !== 'polaris' || typeof claims.exp !== 'number' || claims.exp < now) return null
  if (audience && claims.aud !== audience) return null
  if (scope && claims.scope !== scope) return null
  return claims
}

// ---- Subscription extensions and node metrics (protocol stage 4) ----

/** Content types a subscription format may declare in its manifest. */
const SUB_CONTENT_TYPES = ['text/plain', 'text/yaml', 'application/yaml', 'application/x-yaml', 'application/json', 'application/xml', 'text/xml']
const METRIC_UNITS = ['ms', 's', 'Mbps', 'kbps', '%', 'count', 'bool']
/** Metric names kept for the node's own figures. */
const METRIC_RESERVED = ['cpu', 'mem', 'memory', 'rx', 'tx', 'load', 'online', 'status', 'heartbeat', 'traffic', 'up', 'down']
const hasControl = (s) => /[\u0000-\u001f\u007f]/.test(s)
const chars = (s) => [...s].length

/**
 * parseSigned verifies a signed panel request (render, transform, install,
 * events) and returns its parsed JSON body, or null when the signature or
 * the JSON is bad. Pass the RAW body.
 */
function parseSigned(header, secret, body, opts) {
  if (!verifySignature(header, secret, body, opts)) return null
  try { return JSON.parse(body) } catch { return null }
}

/**
 * checkTransform applies the panel's rules for a /polaris/transform answer.
 * Returns null when the panel would accept it, otherwise the reason. The
 * panel skips a plugin whose answer breaks a rule, so test with this first.
 */
function checkTransform(request, response) {
  const ids = new Set((request.nodes || []).map((n) => n.id))
  if (!response || !Array.isArray(response.nodes)) return 'answer must be {"nodes": [...]}'
  const seen = new Set()
  for (const n of response.nodes) {
    if (!n || typeof n.id !== 'string' || !ids.has(n.id)) return `unknown node id ${JSON.stringify(n && n.id)}`
    if (seen.has(n.id)) return `node id ${n.id} listed twice`
    seen.add(n.id)
    if (n.name === undefined || n.name === null) continue
    if (typeof n.name !== 'string') return `name of ${n.id} must be a string`
    const name = n.name.trim()
    if (name && (chars(name) > 100 || hasControl(name))) return `name of ${n.id} must be at most 100 characters without control characters`
  }
  return null
}

/** checkMetric applies the panel's rules for one reported metric; null = valid. */
function checkMetric(m, now = new Date()) {
  if (!m || typeof m.name !== 'string' || !/^[a-z][a-z0-9_.-]{0,39}$/.test(m.name)) return 'name must start with a lowercase letter, at most 40 characters'
  if (METRIC_RESERVED.includes(m.name)) return `name ${m.name} is reserved`
  if (!METRIC_UNITS.includes(m.unit)) return `unit must be one of ${METRIC_UNITS.join(', ')}`
  if (typeof m.value !== 'number' || !Number.isFinite(m.value) || Math.abs(m.value) > 1e12) return 'value must be a finite number within ±1e12'
  if (m.unit === 'bool' && m.value !== 0 && m.value !== 1) return 'a bool metric is 0 or 1'
  if (m.label !== undefined && (typeof m.label !== 'string' || chars(m.label) > 40 || hasControl(m.label))) return 'label must be at most 40 characters without control characters'
  if (m.at !== undefined && m.at !== '') {
    const t = typeof m.at === 'string' && /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?(Z|[+-]\d\d:\d\d)$/.test(m.at) ? Date.parse(m.at) : NaN
    if (Number.isNaN(t) || t < now.getTime() - 86400e3 || t > now.getTime() + 300e3) return 'at must be an RFC 3339 time within the last 24 hours'
  }
  return null
}

/** PanelClient calls the panel API with the plugin's token. */
class PanelClient {
  constructor(apiBase, token, { fetch: f = globalThis.fetch } = {}) {
    this.apiBase = apiBase.replace(/\/+$/, '')
    this.token = token
    this.fetch = f
  }

  async request(method, path, body, headers = {}) {
    const res = await this.fetch(this.apiBase + path, {
      method,
      headers: { Authorization: `Bearer ${this.token}`, 'Content-Type': 'application/json', ...headers },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
    const text = await res.text()
    if (!res.ok) {
      const err = new Error(`polaris ${res.status}: ${text}`)
      err.status = res.status
      throw err
    }
    return text ? JSON.parse(text) : null
  }

  config() { return this.request('GET', '/plugin/config') }
  users() { return this.request('GET', '/users') }
  user(id) { return this.request('GET', `/users/${encodeURIComponent(id)}`) }
  /** renew extends an account; pass a stable idempotencyKey per order. */
  renew(id, days, { resetTraffic = false, idempotencyKey } = {}) {
    return this.request('POST', `/users/${encodeURIComponent(id)}/renew`, { days, resetTraffic },
      idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : {})
  }
  addTraffic(id, gb, { idempotencyKey } = {}) {
    return this.request('POST', `/users/${encodeURIComponent(id)}/add-traffic`, { gb },
      idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : {})
  }
  /** nodes lists nodes (scope nodes:read). */
  nodes() { return this.request('GET', '/nodes') }
  /** reportMetrics stores readings on a node (scope nodes:metrics:write), at most 20 per call. */
  reportMetrics(nodeId, metrics) {
    return this.request('POST', `/nodes/${encodeURIComponent(nodeId)}/metrics`, { metrics })
  }
  /** externalMetrics reads every source's metrics for a node (scope nodes:read). */
  externalMetrics(nodeId) { return this.request('GET', `/nodes/${encodeURIComponent(nodeId)}/metrics/external`) }
}

module.exports = {
  sign, signatureHeader, verifySignature, verifyIdentity, parseSigned, checkTransform, checkMetric, PanelClient,
  TOLERANCE, SUB_CONTENT_TYPES, METRIC_UNITS, METRIC_RESERVED,
}
