// Validates every example manifest against schema/polaris-plugin.schema.json,
// and checks that the schema rejects manifests the panel would refuse.
'use strict'
const fs = require('fs')
const path = require('path')
const Ajv = require('ajv/dist/2020')

const root = path.join(__dirname, '..')
const ajv = new Ajv({ allErrors: true, strict: false })
const validate = ajv.compile(JSON.parse(fs.readFileSync(path.join(root, 'schema/polaris-plugin.schema.json'), 'utf8')))

let bad = 0
for (const dir of fs.readdirSync(path.join(root, 'examples')).sort()) {
  const f = path.join('examples', dir, 'polaris-plugin.json')
  if (!fs.existsSync(path.join(root, f))) continue
  const m = JSON.parse(fs.readFileSync(path.join(root, f), 'utf8'))
  if (!validate(m)) { bad++; console.error(f, JSON.stringify(validate.errors, null, 1)) } else console.log('ok', f)
}

const base = { id: 'x-plugin', name: 'x', version: '1.0.0' }
const rejected = {
  'built-in format name': { subscription: { formats: [{ name: 'clash', title: 'c', contentType: 'text/plain' }] } },
  'html content type': { subscription: { formats: [{ name: 'myfmt', title: 'c', contentType: 'text/html' }] } },
  'format name too short': { subscription: { formats: [{ name: 'a', title: 'c', contentType: 'text/plain' }] } },
  'unknown scope': { scopes: ['nodes:write'] },
}
for (const [what, extra] of Object.entries(rejected)) {
  if (validate({ ...base, ...extra })) { bad++; console.error('schema accepted:', what) } else console.log('rejected as expected:', what)
}
// The registry: schema, unique ids, and entries for this repository's
// examples agree with their manifests (the panel shows these scopes before
// anyone deploys the plugin, so they must not understate what it asks for).
const reg = JSON.parse(fs.readFileSync(path.join(root, 'registry/index.json'), 'utf8'))
const validateReg = ajv.compile(JSON.parse(fs.readFileSync(path.join(root, 'schema/registry.schema.json'), 'utf8')))
if (!validateReg(reg)) { bad++; console.error('registry/index.json', JSON.stringify(validateReg.errors, null, 1)) }
const seen = new Set()
const same = (a, b) => JSON.stringify([...(a || [])].sort()) === JSON.stringify([...(b || [])].sort())
for (const e of reg.plugins || []) {
  if (seen.has(e.id)) { bad++; console.error('registry: duplicate id', e.id) }
  seen.add(e.id)
  const local = /^https:\/\/github\.com\/cuijianzhuang\/polaris-plugins\/tree\/main\/examples\/([^/]+)$/.exec(e.repo || '')
  if (!local) continue
  const dir = path.join(root, 'examples', local[1])
  if (!fs.existsSync(dir)) { bad++; console.error('registry:', e.id, 'points to a missing example', local[1]); continue }
  const mf = path.join(dir, 'polaris-plugin.json')
  if (e.type === 'tool') {
    if (fs.existsSync(mf)) { bad++; console.error('registry:', e.id, 'has a manifest, so its type should be "plugin"') }
    continue
  }
  if (!fs.existsSync(mf)) { bad++; console.error('registry:', e.id, 'is a plugin without polaris-plugin.json'); continue }
  const m = JSON.parse(fs.readFileSync(mf, 'utf8'))
  const problems = []
  if (m.id !== e.id) problems.push(`id ${e.id} != manifest ${m.id}`)
  if (!same(m.scopes, e.scopes)) problems.push(`scopes ${JSON.stringify(e.scopes || [])} != manifest ${JSON.stringify(m.scopes || [])}`)
  if (!same(m.events, e.events)) problems.push(`events ${JSON.stringify(e.events || [])} != manifest ${JSON.stringify(m.events || [])}`)
  if ((m.minPanel || '') !== (e.minPanel || '')) problems.push(`minPanel ${e.minPanel || '-'} != manifest ${m.minPanel || '-'}`)
  if (problems.length) { bad++; console.error('registry:', e.id + ':', problems.join('; ')) } else console.log('ok registry', e.id)
}
const badEntry = { id: 'x', name: 'x', description: 'x', author: 'x', repo: 'http://insecure', type: 'plugin', kind: 'community', category: 'other' }
if (validateReg({ version: 1, plugins: [badEntry] })) { bad++; console.error('registry schema accepted an http:// repo') } else console.log('rejected as expected: http repo')

process.exit(bad ? 1 : 0)
