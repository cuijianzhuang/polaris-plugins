// Validates every example manifest against schema/polaris-plugin.schema.json.
'use strict'
const fs = require('fs')
const path = require('path')
const Ajv = require('ajv/dist/2020')

const root = path.join(__dirname, '..')
const ajv = new Ajv({ allErrors: true, strict: false })
const validate = ajv.compile(JSON.parse(fs.readFileSync(path.join(root, 'schema/polaris-plugin.schema.json'), 'utf8')))

let bad = 0
for (const f of ['examples/external-notify/polaris-plugin.json', 'examples/hello/polaris-plugin.json']) {
  const m = JSON.parse(fs.readFileSync(path.join(root, f), 'utf8'))
  if (!validate(m)) { bad++; console.error(f, JSON.stringify(validate.errors, null, 1)) } else console.log('ok', f)
}
process.exit(bad ? 1 : 0)
