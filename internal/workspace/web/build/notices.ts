// Build plugin: writes dist/THIRD_PARTY_NOTICES.txt with the license of every
// package whose code or files actually end up in the bundle, and fails the
// build when one is not on the allowlist. Build-time tools (Vite,
// TypeScript, vue-tsc) never reach the bundle and are not listed.

import { existsSync, readFileSync, readdirSync } from 'node:fs'
import { isAbsolute, join } from 'node:path'
import type { Plugin } from 'vite'

const allowed = new Set(['MIT', 'ISC', 'BSD-2-Clause', 'BSD-3-Clause', 'Apache-2.0', 'OFL-1.1'])

/** The package directory for a path inside node_modules, or null. */
function packageDir(file: string, root: string): string | null {
  const m = /^(.*?node_modules\/(?:@[^/]+\/)?[^/]+)\//.exec(file.replace(/\\/g, '/').replace(/^\0/, ''))
  if (!m) return null
  return isAbsolute(m[1]!) ? m[1]! : join(root, m[1]!)
}

function licenseText(dir: string, license: string): string {
  const file = readdirSync(dir).find((f) => /^(licen[cs]e|copying)(\.|$)/i.test(f))
  return file ? readFileSync(join(dir, file), 'utf8').trim() : `Licensed under ${license}.`
}

export function thirdPartyNotices(): Plugin {
  let root = process.cwd()
  return {
    name: 'astrona-third-party-notices',
    apply: 'build',
    configResolved(config) {
      root = config.root
    },
    generateBundle(_options, bundle) {
      const dirs = new Set<string>()
      for (const item of Object.values(bundle)) {
        const files = item.type === 'chunk' ? item.moduleIds : (item.originalFileNames ?? [])
        for (const f of files) {
          const dir = packageDir(f, root)
          if (dir) dirs.add(dir)
        }
      }
      const sections: string[] = []
      const refused: string[] = []
      for (const dir of [...dirs].sort()) {
        const pkgFile = join(dir, 'package.json')
        if (!existsSync(pkgFile)) continue
        const pkg = JSON.parse(readFileSync(pkgFile, 'utf8'))
        const license: string = typeof pkg.license === 'string' ? pkg.license : (pkg.license?.type ?? 'UNKNOWN')
        if (!allowed.has(license)) refused.push(`${pkg.name}@${pkg.version}: ${license}`)
        sections.push(`${pkg.name} ${pkg.version} (${license})\n${pkg.homepage ?? ''}\n\n${licenseText(dir, license)}\n`)
      }
      if (refused.length) this.error(`licenses not on the allowlist: ${refused.join(', ')}`)
      if (sections.length === 0) this.error('no third-party packages found in the bundle; the notice scan is broken')

      const header = [
        'Third-party software in the Astrona browser workspace',
        '',
        'The workspace page embedded in astrona includes the packages below.',
        'Astrona itself is licensed under the Apache License 2.0.',
        '',
      ].join('\n')
      const rule = '\n' + '-'.repeat(72) + '\n\n'
      this.emitFile({
        type: 'asset',
        fileName: 'THIRD_PARTY_NOTICES.txt',
        source: header + rule + sections.join(rule),
      })
    },
  }
}
