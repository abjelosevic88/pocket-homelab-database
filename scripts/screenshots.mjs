// README screenshots and a browser smoke test, against a demo instance (DEMO_DATA=true seeds a shop database and a few servers):
//
//   DATA_DIR=/tmp/database-demo DEMO_DATA=true DOCKER_HOST=off ADMIN_EMAIL=demo@example.com ADMIN_PASSWORD=demodemo1 ADMIN_NAME=Maya PORT=18409 ./pocket-database
//   CDP=http://127.0.0.1:19271 BASE=http://127.0.0.1:18409 node scripts/screenshots.mjs   # any Chrome with remote debugging
//   SHOTS=0 … node scripts/screenshots.mjs                                                  # checks only
// playwright-core comes from web/node_modules, or set PW_FROM to a package.json next to an install of it.
import { createRequire } from 'node:module'
import assert from 'node:assert/strict'

const require = createRequire(process.env.PW_FROM || new URL('../web/package.json', import.meta.url))
let pw
try { pw = require('playwright') } catch { pw = require('playwright-core') }
const { chromium } = pw
const base = process.env.BASE || 'http://127.0.0.1:18409'
const shots = process.env.SHOTS !== '0'
const dir = new URL('../docs/screenshots/', import.meta.url).pathname
const browser = process.env.CDP ? await chromium.connectOverCDP(process.env.CDP) : await chromium.launch()
const errors = []

async function session({ phone = false, dark = false } = {}) {
  const ctx = await browser.newContext({
    viewport: phone ? { width: 390, height: 844 } : { width: 1440, height: 900 },
    deviceScaleFactor: phone ? 3 : 2, isMobile: phone, hasTouch: phone, colorScheme: dark ? 'dark' : 'light',
    userAgent: phone ? 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1' : undefined,
  })
  const page = await ctx.newPage()
  page.on('pageerror', e => errors.push(`pageerror: ${e}`))
  page.on('console', m => { if (m.type() === 'error' && !/Failed to load resource/.test(m.text())) errors.push(m.text()) })
  await page.goto(base + '/login')
  const res = await page.evaluate(() => fetch('/api/v1/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ email: 'demo@example.com', password: 'demodemo1' }) }).then(r => r.status))
  assert.equal(res, 200, 'demo sign-in')
  return { ctx, page }
}
async function go(page, path, wait = 1200) {
  await page.goto(base + path, { waitUntil: 'networkidle' })
  await page.addStyleTag({ content: '*, *::before, *::after { transition: none !important; animation: none !important; }' })
  await page.evaluate(() => document.fonts.ready)
  await page.waitForTimeout(wait)
}
async function shot(page, name) { if (shots) await page.screenshot({ path: dir + name + '.png' }) }
async function shopID(page) {
  return page.evaluate(() => fetch('/api/v1/connections').then(r => r.json()).then(l => l.find(c => c.name === 'Shop').id))
}
async function runSQL(page, sql) {
  await page.locator('.sql-area textarea').fill(sql)
  await page.getByRole('button', { name: /^Run$/ }).click()
  await page.locator('.results').waitFor()
  await page.waitForTimeout(500)
}
const REVENUE = "SELECT strftime('%Y-%m', created_at) AS month,\n       COUNT(*) AS orders,\n       ROUND(SUM(total), 2) AS revenue\nFROM orders\nWHERE status <> 'cancelled'\nGROUP BY month\nORDER BY month DESC\nLIMIT 12;"

// ---- desktop ----
{
  const { ctx, page } = await session()
  const id = await shopID(page)
  await go(page, '/')
  assert.ok((await page.content()).includes('Growth'), 'Overview shows growth')
  await shot(page, 'desktop-overview')
  await go(page, '/connections')
  await go(page, `/c/${id}/t/orders`)
  assert.ok(await page.locator('.dgrid tbody tr').count() > 10, 'orders has rows')
  await shot(page, 'desktop-table')
  await go(page, `/c/${id}/sql`)
  await runSQL(page, REVENUE)
  assert.ok(await page.locator('.results .dgrid tbody tr').count() >= 10, 'the query returns months')
  await shot(page, 'desktop-sql')
  await go(page, `/c/${id}/t/orders`)
  await page.getByRole('radio', { name: 'Structure' }).click()
  await page.waitForTimeout(400)
  await shot(page, 'desktop-structure')
  await go(page, `/c/${id}/safety`)
  await shot(page, 'desktop-safety')
  await go(page, '/activity')
  await shot(page, 'desktop-changes')
  await go(page, '/settings/safety')
  await ctx.close()
}
// ---- dark ----
{
  const { ctx, page } = await session({ dark: true })
  const id = await shopID(page)
  await go(page, `/c/${id}/sql`)
  await runSQL(page, REVENUE)
  await shot(page, 'desktop-sql-dark')
  await go(page, '/')
  await ctx.close()
}
// ---- phone ----
{
  const { ctx, page } = await session({ phone: true })
  const id = await shopID(page)
  await go(page, '/')
  await shot(page, 'phone-overview')
  await go(page, `/c/${id}`)
  await go(page, `/c/${id}/t/customers`)
  await shot(page, 'phone-table')
  await page.locator('.dgrid tbody tr').first().click()
  await page.waitForTimeout(500)
  await shot(page, 'phone-row')
  await page.keyboard.press('Escape')
  await go(page, `/c/${id}/sql`)
  await runSQL(page, REVENUE)
  await shot(page, 'phone-sql')
  await ctx.close()
}

await browser.close().catch(() => {})
if (errors.length) { console.error(errors.join('\n')); process.exit(1) }
console.log(shots ? 'screenshots written to docs/screenshots' : 'browser checks passed')
