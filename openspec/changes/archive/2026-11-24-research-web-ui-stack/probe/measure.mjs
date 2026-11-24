import { readdirSync, readFileSync } from 'node:fs'
import { gzipSync } from 'node:zlib'
for (const dir of ['htmxdist', 'sveltedist', 'reactdist']) {
  const files = readdirSync(dir, { recursive: true, withFileTypes: true })
    .filter(entry => entry.isFile())
    .map(entry => readFileSync(entry.parentPath + '/' + entry.name))
  const raw = files.reduce((total, file) => total + file.length, 0)
  const gzip = files.reduce((total, file) => total + gzipSync(file).length, 0)
  console.log(dir, files.length, raw, gzip)
}
