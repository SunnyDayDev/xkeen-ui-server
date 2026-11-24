import assert from 'node:assert/strict';
import { execFileSync, spawn } from 'node:child_process';
import { createHash, X509Certificate } from 'node:crypto';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:net';
import { DatabaseSync } from 'node:sqlite';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { chromium } from 'playwright-core';

const binary = resolve(process.env.XKEEN_TEST_SERVER_BINARY ?? '../.build/xkeen-ui-server');
const dir = mkdtempSync(join(tmpdir(), 'xkeen-web-'));
const database = join(dir, 'server.sqlite');
const secret = join(dir, 'password');
const cert = join(dir, 'certificate.pem');
const key = join(dir, 'key.pem');
let browser;
let server;
const first = 'synthetic browser password';
const replacement = 'synthetic replacement password';
const sleep = () => new Promise(resolve => setTimeout(resolve, 30));
function admin(action, password) {
  writeFileSync(secret, password + '\n', {mode: 0o600});
  execFileSync(binary, ['admin', action, '--db-path', database, '--password-file', secret]);
}
try {
  execFileSync('openssl', ['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
    '-subj', '/CN=127.0.0.1', '-addext', 'subjectAltName=IP:127.0.0.1', '-keyout', key, '-out', cert],
  { stdio: 'ignore' });
  const pub = new X509Certificate(readFileSync(cert)).publicKey.export({ type: 'spki', format: 'der' });
  const pin = createHash('sha256').update(pub).digest('base64');
  const reservation = createServer();
  await new Promise(resolve => reservation.listen(0, '127.0.0.1', resolve));
  const port = reservation.address().port;
  await new Promise(resolve => reservation.close(resolve));
  const host = `127.0.0.1:${port}`;
  const origin = `https://${host}`;
  server = spawn(binary, ['--listen', host, '--tls-cert', cert, '--tls-key', key,
    '--db-path', database, '--web-origin', origin], {stdio: ['ignore', 'ignore', 'pipe']});
  let log = '';
  server.stderr.on('data', data => { log += data; });
  for (let i = 0; i < 160 && !log.includes('"listening"') && server.exitCode === null; i++) await sleep();
  assert.ok(log.includes('"listening"'), 'HTTPS binary did not become ready');

  browser = await chromium.launch({ executablePath: process.env.CHROME_EXECUTABLE,
    args: [`--ignore-certificate-errors-spki-list=${pin}`] });
  const page = await browser.newPage({viewport: {width: 1080, height: 660}});
  page.setDefaultTimeout(5000);
  const post = [];
  page.on('request', request => { if (request.method() === 'POST') post.push(new URL(request.url()).pathname); });
  await page.goto(origin + '/ui/');
  await page.getByRole('heading', {name:'Доступ ещё не настроен'}).waitFor();
  admin('setup', first);
  await page.getByRole('button', {name:'Проверить настройку'}).click();

  const password = page.getByLabel('Пароль администратора');
  await page.route('**/ui/v1/login', async route => {
    await new Promise(resolve => setTimeout(resolve, 220));
    await route.continue();
  }, {times: 1});
  await password.fill('wrong browser password');
  await password.press('Enter');
  await page.getByText('Проверяем пароль.').waitFor();
  await page.getByText('Не удалось войти. Проверьте пароль и попробуйте ещё раз.').waitFor();
  await password.fill(first);
  await password.press('Enter');
  await page.getByRole('heading', {name:'Обзор'}).waitFor();
  assert.equal(await page.getByRole('link',{name:'Обзор'}).getAttribute('aria-current'),'page');

  const templates = page.getByRole('link', {name:'Шаблоны'});
  await page.route('**/ui/v1/templates', async route => {
    await new Promise(resolve => setTimeout(resolve, 220));
    await route.continue();
  }, {times: 1});
  await templates.click();
  await page.getByText('Загрузка шаблонов…').waitFor();
  await page.getByText('Шаблонов пока нет.').waitFor();
  assert.equal(new URL(page.url()).pathname, '/ui/templates');
  assert.equal(await templates.getAttribute('aria-current'),'page');

  const legacyID = '<literal>/' + 'Д'.repeat(96) + ' branch';
  const sql = new DatabaseSync(database);
  try {
    sql.prepare('INSERT INTO templates(id, draft, version) VALUES (?,?,?)').run(legacyID, '[]', 9223372036854775807n);
    sql.prepare('INSERT INTO templates(id, draft, version) VALUES (?,?,?)').run('branch-east', '[]', 2);
  } finally { sql.close(); }
  await page.goBack();
  await page.getByRole('heading', {name:'Обзор'}).waitFor();
  await page.route('**/ui/v1/templates', route => route.fulfill({status:503, contentType:'application/json',
    body:JSON.stringify({state:'unavailable'})}), {times: 1});
  await page.goForward();
  await page.getByText('Не удалось загрузить шаблоны.').waitFor();
  await page.getByRole('button', {name:'Повторить'}).click();
  await page.getByText(legacyID, {exact:true}).waitFor();
  await page.getByText('Версия 9223372036854775807', {exact:true}).waitFor();
  await page.reload();
  await page.getByText('branch-east', {exact:true}).waitFor();
  assert.equal(await page.locator('.console-header').count(), 1);
  if (process.env.XKEEN_BROWSER_SCREENSHOT) await page.screenshot({path:process.env.XKEEN_BROWSER_SCREENSHOT + '-templates-wide.png'});
  await page.setViewportSize({width:360,height:640});
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth), 360);
  if (process.env.XKEEN_BROWSER_SCREENSHOT) {
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    await page.screenshot({path:process.env.XKEEN_BROWSER_SCREENSHOT + '-templates.png'});
  }
  await page.setViewportSize({width:1080,height:660});
  await page.getByRole('link',{name:'Обзор'}).click();
  admin('reset', first);
  const inactive = page.waitForResponse(response => response.url().endsWith('/ui/v1/templates') && response.status() === 401);
  await templates.click();
  await inactive;
  await page.getByRole('heading', {name:'Сессия истекла'}).waitFor();
  assert.equal(await page.getByText('branch-east', {exact:true}).isVisible(), false);
  await password.fill(first);
  await password.press('Enter');
  await page.getByText('branch-east', {exact:true}).waitFor();
  await page.getByRole('link',{name:'Обзор'}).click();

  await page.evaluate(() => {
    const main = document.querySelector('main');
    main.dataset.tabBuffer = 'synthetic';
    const retained = document.createElement('input');
    retained.setAttribute('aria-label', 'Synthetic focus target');
    retained.value = 'draft fixture';
    main.append(retained);
    retained.focus();
  });
  admin('reset', replacement);
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await page.getByRole('heading',{name:'Сессия истекла'}).waitFor();
  assert.equal(await page.getByRole('button',{name:'Выйти'}).count(),0);
  const before = post.length;
  await password.fill(replacement);
  await password.press('Enter');
  await page.getByRole('heading',{name:'Обзор'}).waitFor();
  assert.equal(await page.locator('main').getAttribute('data-tab-buffer'), 'synthetic');
  assert.deepEqual(post.slice(before), ['/ui/v1/login']);
  assert.equal(await page.evaluate(() => document.activeElement.getAttribute('aria-label')), 'Synthetic focus target');
  assert.equal(await page.getByLabel('Synthetic focus target').inputValue(), 'draft fixture');

  await page.setViewportSize({width: 360, height: 640});
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth), 360);
  if (process.env.XKEEN_BROWSER_SCREENSHOT) await page.screenshot({path:process.env.XKEEN_BROWSER_SCREENSHOT + '-active.png'});
  await page.route('**/ui/v1/logout', route => route.abort(), {times: 1});
  await page.getByRole('button',{name:'Выйти'}).click();
  await page.getByText('Сервер сейчас недоступен. Попробуйте ещё раз.').waitFor();
  await page.getByRole('heading', {name:'Обзор'}).waitFor();
  await page.getByRole('button',{name:'Выйти'}).click();
  await page.getByText('Вы вышли из консоли.').waitFor();
  assert.equal(await page.locator('main[data-tab-buffer]').count(),0);
  if (process.env.XKEEN_BROWSER_SCREENSHOT) await page.screenshot({path:process.env.XKEEN_BROWSER_SCREENSHOT + '-login.png'});
  for (let attempt = 0; attempt < 5; attempt++) {
    await password.fill('wrong browser password');
    await password.press('Enter');
    await page.locator('form[aria-busy="false"]').waitFor();
  }
  await page.getByText('Слишком много попыток входа. Повторите позже.').waitFor();
  console.log('F04–F05 real HTTPS browser scenarios passed');
} finally {
  if (browser) await browser.close();
  if (server && server.exitCode === null) {
    server.kill('SIGTERM');
    await new Promise(resolve => server.once('exit',resolve));
  }
  rmSync(dir,{recursive:true,force:true});
}
