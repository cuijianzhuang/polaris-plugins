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
process.exit(bad ? 1 : 0)
