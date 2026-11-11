import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'

const read = (path) => readFileSync(new URL(path, import.meta.url), 'utf8')
const javascript = (source) => ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText
const { enterRun } = await import(`data:text/javascript;base64,${Buffer.from(javascript(read('../src/navigation.ts'))).toString('base64')}`)

function browser() {
  const events = []
  return {
    events,
    location: { pathname: '/', search: '?preview=1', reload() { events.push('reload') } },
    history: { replaceState(_state, _title, url) { events.push(url) } },
  }
}

test('entering a new run reloads its document with a fresh code, without storage', async () => {
  const b = browser()
  await enterRun('lab with spaces', async () => 'single-use-code', { browser: b })
  assert.equal(b.events.length, 2)
  const url = new URL(b.events[0], 'http://localhost:5173')
  assert.equal(url.search, '?preview=1')
  const fragment = new URLSearchParams(url.hash.slice(1))
  assert.equal(fragment.get('code'), 'single-use-code')
  assert.equal(fragment.get('lab'), 'lab with spaces')
  assert.equal(b.events[1], 'reload')
})

test('a failed code request leaves the current page and address intact', async () => {
  const b = browser()
  await assert.rejects(enterRun('demo', async () => { throw new Error('offline') }, { browser: b }), /offline/)
  assert.deepEqual(b.events, [])
})

// Run the actual component handlers with their API and browser boundaries
// replaced. This catches a launch button that fetches a run but never leaves
// the launch screen, as well as Home navigation that retains stale CSP.
const source = ts.createSourceFile('App.ts', read('../src/App.vue').split('<script setup lang="ts">')[1].split('</script>')[0], ts.ScriptTarget.Latest, true)
// "Begin this session" starts the run in the new document; Home only opens it.
const entered = { onLaunchReady: '/?preview=1#code=new-code&lab=demo&begin=1', openFromHome: '/?preview=1#code=new-code&lab=demo' }
for (const name of ['onLaunchReady', 'openFromHome']) {
  const handler = source.statements.find((node) => ts.isFunctionDeclaration(node) && node.name?.text === name)
  assert.ok(handler, `${name} exists`)
  const makeHandler = new Function('enterRun', 'getNavigationCode', 'showError', `${javascript(handler.getText(source))}; return ${name}`)
  test(`${name} enters the run through a new document`, async () => {
    const b = browser()
    const errors = []
    const invoke = makeHandler((lab, mint, opts) => enterRun(lab, mint, { ...opts, browser: b }), async () => 'new-code', (err) => errors.push(err))
    await invoke('demo')
    assert.deepEqual(errors, [])
    assert.deepEqual(b.events, [entered[name], 'reload'])
  })
  test(`${name} reports navigation failure`, async () => {
    const errors = []
    const failure = new Error('workspace unavailable')
    const b = browser()
    const invokeWithBrowser = makeHandler((lab, mint, opts) => enterRun(lab, mint, { ...opts, browser: b }), async () => { throw failure }, (err) => errors.push(err))
    await invokeWithBrowser('demo')
    assert.deepEqual(errors, [failure])
    assert.deepEqual(b.events, [])
  })
}
