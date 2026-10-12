// Checks that npm installed every platform package the lockfile lists for this
// platform: the native bindings (lightningcss-linux-x64-musl,
// @rolldown/binding-linux-x64-musl, ...) are optional dependencies, and npm ci
// skips one it fails to fetch without failing. The build then fails much later,
// and in a Docker build the broken npm ci layer is cached, so a rerun fails too
// (#912). The Dockerfile runs this in the same step as npm ci.
//
//   node scripts/check-native.mjs [DIR]   the install in DIR (default .)
import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

const dir = process.argv[2] ?? '.'
const lock = JSON.parse(readFileSync(join(dir, 'package-lock.json'), 'utf8'))
const libc = process.report.getReport().header.glibcVersionRuntime ? 'glibc' : 'musl'

// matches(list, value): whether an os, cpu or libc field admits value; a field
// lists values, or excludes them with a leading !.
function matches(list, value) {
  if (!list) return true
  if (list.some((v) => v === `!${value}`)) return false
  return list.includes(value) || list.every((v) => v.startsWith('!'))
}

const missing = Object.entries(lock.packages ?? {})
  .filter(([path, pkg]) => path && pkg.optional && (pkg.os || pkg.cpu || pkg.libc))
  .filter(([, pkg]) => matches(pkg.os, process.platform) && matches(pkg.cpu, process.arch))
  .filter(([, pkg]) => process.platform !== 'linux' || matches(pkg.libc, libc))
  .filter(([path]) => !existsSync(join(dir, path, 'package.json')))
  .map(([path]) => path)

if (missing.length > 0) {
  console.error(`npm skipped ${missing.length} package(s) the lockfile lists for ${process.platform}-${process.arch}:`)
  for (const path of missing) console.error(`  ${path}`)
  console.error('Install again (npm ci); npm skips an optional package it fails to fetch.')
  process.exit(1)
}
