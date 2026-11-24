import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { chromium } from 'playwright-core'

const browser = await chromium.launch({
  executablePath: process.env.CHROME_EXECUTABLE || undefined,
})
try {
  for (const stack of ['htmx', 'svelte', 'react']) {
    const child = spawn('./probe-' + stack)
    try {
      const [address] = await once(child.stdout, 'data')
      const page = await browser.newPage()
      try {
        await page.goto('http://' + address.toString().trim())
        const draft = '{ "stillDraft": 9007199254740993 }'
        await page.locator('#draft').fill(draft)
        await page.locator('#swap').press('Enter')
        await page.waitForFunction(() => document.querySelector('#rows li')?.textContent === 'Rule B')
        await page.locator('#check').press('Enter')
        await page.waitForFunction(() => document.querySelector('#status')?.textContent === 'Ready')
        if (await page.locator('#draft').inputValue() !== draft) {
          throw new Error('draft changed')
        }
        console.log(stack + ': PASS browser keyboard, status, exact draft')
      } finally {
        await page.close()
      }
    } finally {
      child.kill()
    }
  }
} finally {
  await browser.close()
}
