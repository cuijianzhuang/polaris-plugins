'use strict'
const test = require('node:test')
const assert = require('node:assert')
const v = require('../../testdata/vectors.json')
const sdk = require('./index')

test('sign matches the shared vector', () => {
  assert.strictEqual(sdk.sign(v.secret, v.timestamp, v.body), v.signature)
})
test('verifySignature', () => {
  const opt = { now: v.now }
  assert.ok(sdk.verifySignature(v.header, v.secret, v.body, opt))
  assert.ok(!sdk.verifySignature(v.header, 'other', v.body, opt))
  assert.ok(!sdk.verifySignature(v.header, v.secret, v.body + ' ', opt))
  assert.ok(!sdk.verifySignature(v.header, v.secret, v.body, { now: v.now + 1000 }))
  assert.ok(!sdk.verifySignature('garbage', v.secret, v.body, opt))
  assert.ok(!sdk.verifySignature('', v.secret, v.body, opt))
})
test('signatureHeader round-trips', () => {
  assert.ok(sdk.verifySignature(sdk.signatureHeader('k', 'body'), 'k', 'body'))
})
test('verifyIdentity', () => {
  const o = { audience: 'hello', scope: 'user', now: v.now }
  assert.strictEqual(sdk.verifyIdentity(v.jwt.valid, v.secret, o).sub, 'u1')
  assert.strictEqual(sdk.verifyIdentity(v.jwt.wrongSecret, v.secret, o), null)
  assert.strictEqual(sdk.verifyIdentity(v.jwt.expired, v.secret, o), null)
  assert.strictEqual(sdk.verifyIdentity(v.jwt.wrongAudience, v.secret, o), null)
  assert.strictEqual(sdk.verifyIdentity(v.jwt.wrongIssuer, v.secret, o), null)
  assert.strictEqual(sdk.verifyIdentity(v.jwt.valid, v.secret, { ...o, scope: 'admin' }), null)
  assert.strictEqual(sdk.verifyIdentity('a.b', v.secret, o), null)
})
test('PanelClient sends token and idempotency key', async () => {
  let seen
  const c = new sdk.PanelClient('http://p/api/v1/', 'plt_x', {
    fetch: async (url, init) => { seen = { url, init }; return { ok: true, status: 200, text: async () => '{"id":"u1"}' } },
  })
  const u = await c.renew('u 1', 30, { idempotencyKey: 'k1' })
  assert.strictEqual(u.id, 'u1')
  assert.strictEqual(seen.url, 'http://p/api/v1/users/u%201/renew')
  assert.strictEqual(seen.init.headers.Authorization, 'Bearer plt_x')
  assert.strictEqual(seen.init.headers['Idempotency-Key'], 'k1')
  assert.deepStrictEqual(JSON.parse(seen.init.body), { days: 30, resetTraffic: false })
})

const ext = require('../../testdata/extensions.json')
test('checkTransform follows the shared cases', () => {
  for (const c of ext.transform.cases) {
    const err = sdk.checkTransform(ext.transform.request, c.response)
    assert.strictEqual(err === null, c.ok, `${c.name}: ${err}`)
  }
})
test('checkMetric follows the shared cases', () => {
  const now = new Date(ext.metrics.now)
  for (const c of ext.metrics.cases) {
    const err = sdk.checkMetric(c.metric, now)
    assert.strictEqual(err === null, c.ok, `${JSON.stringify(c.metric)}: ${err}`)
  }
})
test('parseSigned', () => {
  assert.strictEqual(sdk.parseSigned(v.header, v.secret, v.body, { now: v.now }).id, 'evt_1')
  assert.strictEqual(sdk.parseSigned(v.header, 'x', v.body, { now: v.now }), null)
  const h = sdk.signatureHeader('k', 'not json')
  assert.strictEqual(sdk.parseSigned(h, 'k', 'not json'), null)
})
test('PanelClient.reportMetrics', async () => {
  let seen
  const c = new sdk.PanelClient('http://p/api/v1', 't', {
    fetch: async (url, init) => { seen = { url, init }; return { ok: true, status: 200, text: async () => '{"stored":1}' } },
  })
  await c.reportMetrics('n/1', [{ name: 'latency', unit: 'ms', value: 3 }])
  assert.strictEqual(seen.url, 'http://p/api/v1/nodes/n%2F1/metrics')
  assert.strictEqual(seen.init.method, 'POST')
  assert.deepStrictEqual(JSON.parse(seen.init.body), { metrics: [{ name: 'latency', unit: 'ms', value: 3 }] })
})
