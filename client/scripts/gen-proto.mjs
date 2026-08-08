/**
 * Generates the client's protobuf runtime artifacts from the SERVER's schema.
 *
 * The Go gateway encodes every envelope body as protobuf (pkg/wire/protocodec.go
 * installs protoCodec in its package init), so `server/proto/synapse/v1/body.proto`
 * is the single source of truth for body shapes. Rather than hand-copying 80
 * message definitions into TypeScript — where they would silently drift from the
 * server — we parse the .proto at build time and emit:
 *
 *   generated/descriptor.json  the protobufjs JSON descriptor loaded at runtime
 *                              by protobufjs/light (no .proto parser in the bundle)
 *   generated/bodies.ts        TypeScript interfaces for every body message
 *
 * Run with:  npm run proto:gen
 */
import { writeFileSync, mkdirSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import protobuf from 'protobufjs'

const here = dirname(fileURLToPath(import.meta.url))
const PROTO = resolve(here, '../../server/proto/synapse/v1/body.proto')
const OUT_DIR = resolve(here, '../src/shared/api/protocol/generated')

/** proto scalar -> TS type. 64-bit fields arrive as numbers because the codec
 * decodes with `longs: Number`; bytes arrive base64-encoded as strings. */
const SCALARS = {
  double: 'number',
  float: 'number',
  int32: 'number',
  uint32: 'number',
  sint32: 'number',
  fixed32: 'number',
  sfixed32: 'number',
  int64: 'number',
  uint64: 'number',
  sint64: 'number',
  fixed64: 'number',
  sfixed64: 'number',
  bool: 'boolean',
  string: 'string',
  bytes: 'string',
}

const root = protobuf.loadSync(PROTO)
root.resolveAll()

const ns = root.lookup('synapse.v1')
const types = ns.nestedArray.filter((t) => t instanceof protobuf.Type)

mkdirSync(OUT_DIR, { recursive: true })
// Emitted as TypeScript rather than JSON so the same import works under the
// Next bundler and under plain Node (which would demand an import attribute
// for a .json module).
writeFileSync(
  resolve(OUT_DIR, 'descriptor.ts'),
  [
    '// GENERATED FILE — do not edit by hand.',
    '// Source: server/proto/synapse/v1/body.proto (regenerate: npm run proto:gen)',
    '',
    `export const descriptor = ${JSON.stringify(root.toJSON(), null, 2)} as const`,
    '',
    'export default descriptor',
    '',
  ].join('\n'),
)

const tsType = (field) => {
  const base = SCALARS[field.type] ?? field.type
  if (field.map) return `Record<string, ${base}>`
  if (field.repeated) return `${base}[]`
  // A singular message field has no default, so the decoder yields null.
  return SCALARS[field.type] ? base : `${base} | null`
}

const lines = [
  '// GENERATED FILE — do not edit by hand.',
  '// Source: server/proto/synapse/v1/body.proto (regenerate: npm run proto:gen)',
  '//',
  '// Every field is required here because the codec decodes with `defaults: true`,',
  '// so proto3 scalars are always materialised. Use `Encodable<T>` when building a',
  '// body to send — unset fields simply encode to their proto3 default.',
  '',
  'export type Encodable<T> = {',
  '  [K in keyof T]?: T[K] extends (infer U)[]',
  '    ? Encodable<U>[]',
  '    : T[K] extends object | null',
  '      ? Encodable<NonNullable<T[K]>> | null',
  '      : T[K]',
  '}',
  '',
]

for (const type of types) {
  const comment = type.comment ? `/** ${type.comment.replace(/\s*\n\s*/g, ' ')} */\n` : ''
  lines.push(`${comment}export interface ${type.name} {`)
  for (const field of type.fieldsArray) {
    if (field.comment) lines.push(`  /** ${field.comment.replace(/\s*\n\s*/g, ' ')} */`)
    lines.push(`  ${field.name}: ${tsType(field)}`)
  }
  lines.push('}', '')
}

mkdirSync(OUT_DIR, { recursive: true })
writeFileSync(resolve(OUT_DIR, 'bodies.ts'), lines.join('\n'))

console.log(`generated ${types.length} body types -> ${OUT_DIR}`)
