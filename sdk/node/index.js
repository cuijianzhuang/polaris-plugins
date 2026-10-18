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
}

module.exports = { sign, signatureHeader, verifySignature, verifyIdentity, PanelClient, TOLERANCE }
