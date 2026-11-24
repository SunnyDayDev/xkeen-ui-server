import { checkTemplateCopy } from './template-copy-browser.mjs';
import { checkAgentKeys } from './agent-keys-browser.mjs';
import { checkExclusionsBrowser } from './exclusions-browser.mjs';
import { checkRouterOverview } from './router-overview-browser.mjs';
import { checkTemplateVariables } from './template-variables-browser.mjs';
import { checkRouterEnrollment } from './router-enrollment-browser.mjs';
import { checkAppendTemplateElement, checkTemplateEditor } from './template-editor-browser.mjs';
import { checkExternalAccess } from './external-access-browser.mjs';
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
const routerTemplate = 'edge / preparation';
const workerSocket = process.env.XKEEN_TEST_F09_VALIDATOR_SOCKET;
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
  const serverArgs = ['--listen', host, '--tls-cert', cert, '--tls-key', key,
    '--db-path', database, '--web-origin', origin, '--agent-origin', origin];
  if (workerSocket) serverArgs.push('--validator-socket', workerSocket);
  server = spawn(binary, serverArgs, {stdio: ['ignore', 'ignore', 'pipe']});
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

  await page.getByText('Роутеров с подтверждённым контактом пока нет.',{exact:true}).waitFor();
  await checkExternalAccess(page, () => admin('reset',first), first);
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
    const preparationFixture = [{name:'outbounds',elements:[{disabled:false,record:{
      id:'550e8400-e29b-41d4-a716-446655440000',
      content:'{"protocol":"${protocol}","tag":"${label}"}',
      variables:[{name:'protocol',type:'string'},{name:'label',type:'string'},
        {name:'timeoutSeconds',type:'number',default:5}]
    }}]}, {name:'inbounds',elements:[{disabled:true,record:{
      id:'550e8400-e29b-41d4-a716-446655440004', content:'{"tag":"${inactiveLabel}"}',
      variables:[{name:'inactiveLabel',type:'string'}]
    }}]}];
    sql.prepare('INSERT INTO templates(id, draft) VALUES (?,?)').run(routerTemplate,JSON.stringify(preparationFixture));
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

  const openCreate = page.getByRole('button', {name:'Создать шаблон'});
  const templateID = page.getByLabel('Идентификатор шаблона');
  const submitCreate = page.getByRole('button', {name:'Создать', exact:true});
  await openCreate.click();
  const exactID = ' Branch / # ';
  await templateID.fill(exactID);
  const deferList = async route => {
    if (route.request().method() === 'GET') {
      await route.fulfill({status:503, contentType:'application/json',body:JSON.stringify({state:'unavailable'})});
    } else {
      await new Promise(resolve => setTimeout(resolve,220));
      await route.continue();
    }
  };
  await page.route('**/ui/v1/templates', deferList);
  const atCreate = post.length;
  await submitCreate.click();
  const busyCreate = page.getByRole('button',{name:'Создаём…'});
  await busyCreate.waitFor();
  assert.ok(await busyCreate.isDisabled());
  await page.getByText('Не удалось загрузить шаблоны.').waitFor();
  await page.getByText('Шаблон создан:', {exact:false}).waitFor();
  assert.deepEqual(post.slice(atCreate), ['/ui/v1/templates']);
  await page.unroute('**/ui/v1/templates', deferList);
  await page.getByRole('button',{name:'Повторить',exact:true}).click();
  const savedID = page.getByRole('listitem').filter({hasText: 'Branch / #'});
  await savedID.getByText('Версия 1',{exact:true}).waitFor();
  assert.equal(await savedID.locator('bdi').textContent(), exactID);

  await openCreate.click();
  await templateID.fill(exactID);
  await submitCreate.click();
  await page.getByText('Шаблон с таким идентификатором уже существует.').waitFor();
  assert.equal(await templateID.inputValue(),exactID);
  if (process.env.XKEEN_BROWSER_SCREENSHOT) await page.screenshot({path:process.env.XKEEN_BROWSER_SCREENSHOT + '-create-wide.png'});
  await page.setViewportSize({width:360,height:640});
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth),360);
  if (process.env.XKEEN_BROWSER_SCREENSHOT) {
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    await page.screenshot({path:process.env.XKEEN_BROWSER_SCREENSHOT + '-create.png'});
  }
  await page.setViewportSize({width:1080,height:660});

  await page.route('**/ui/v1/templates', route => route.request().method() === 'POST' ? route.abort() : route.continue(), {times:1});
  await templateID.fill('unconfirmed write');
  await submitCreate.click();
  await page.getByText('Не удалось подтвердить создание. Проверьте список или повторите.').waitFor();
  assert.equal(await templateID.inputValue(), 'unconfirmed write');

  const retainedID = 'office-draft';
  await templateID.fill(retainedID);
  admin('reset', first);
  const deniedCreate = page.waitForResponse(response => response.url().endsWith('/ui/v1/templates') &&
    response.request().method() === 'POST' && response.status() === 401);
  await submitCreate.click();
  await deniedCreate;
  await page.getByRole('heading', {name:'Сессия истекла'}).waitFor();
  const beforeRelogin = post.length;
  await password.fill(first);
  await password.press('Enter');
  await templateID.waitFor();
  assert.equal(await templateID.inputValue(),retainedID);
  assert.deepEqual(post.slice(beforeRelogin), ['/ui/v1/login']);
  await page.getByText('branch-east',{exact:true}).waitFor();
  assert.equal(await page.locator('bdi').filter({hasText:retainedID}).count(),0);
  await page.getByRole('button',{name:'Отмена',exact:true}).click();

  async function holdNextCreateReply() {
    await page.evaluate(() => {
      delete window.releaseOldTemplateResponse;
      const fetchNext = window.fetch;
      window.fetch = async (url, options) => {
        const result = await fetchNext(url, options);
        if (url === '/ui/v1/templates' && options?.method === 'POST') {
          window.fetch = fetchNext;
          await new Promise(resolve => { window.releaseOldTemplateResponse = resolve; });
        }
        return result;
      };
    });
  }

  await openCreate.click();
  await templateID.fill('delayed-commit');
  await holdNextCreateReply();
  const replyHeld = page.waitForFunction(() => typeof window.releaseOldTemplateResponse === 'function');
  await submitCreate.click();
  await replyHeld;
  await page.getByRole('link',{name:'Обзор'}).click();
  await page.getByRole('link',{name:'Шаблоны'}).click();
  await page.getByText('delayed-commit',{exact:true}).waitFor();
  await openCreate.click();
  await templateID.fill('new form value');
  await page.evaluate(async () => {
    window.releaseOldTemplateResponse();
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  });
  await page.getByText('delayed-commit',{exact:true}).waitFor();
  assert.equal(await templateID.inputValue(),'new form value');
  assert.equal(await page.getByText('Шаблон создан:',{exact:false}).count(),0);
  await templateID.fill('before-session-rotation');
  await holdNextCreateReply();
  await submitCreate.click();
  await page.waitForFunction(() => typeof window.releaseOldTemplateResponse === 'function');
  const loginElsewhere = await page.evaluate(async pass => {
    const response = await fetch('/ui/v1/login', {
      method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({password:pass})
    });
    return response.status;
  }, first);
  assert.equal(loginElsewhere,200);
  const updatedAccess = page.waitForResponse(response => response.url().endsWith('/ui/v1/session'));
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await updatedAccess;
  await page.getByText('Не удалось подтвердить создание. Проверьте список или повторите.').waitFor();
  await page.evaluate(async () => {
    window.releaseOldTemplateResponse();
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  });
  assert.equal(await templateID.inputValue(), 'before-session-rotation');
  assert.equal(await page.getByText('Шаблон создан:',{exact:false}).count(),0);
  await checkExclusionsBrowser(page.context(),origin,'branch-east');
  await checkTemplateEditor(page, exactID, first, () => admin("reset", first), (id, sibling) => {
    const db = new DatabaseSync(database);
    try {
      const row = db.prepare('SELECT draft FROM templates WHERE id = ?').get(id);
      const draft = JSON.parse(new TextDecoder().decode(row.draft));
      draft[0].elements.push({disabled:false,record:{id:sibling,content:'{"tag":"sibling.example"}',variables:[]}});
      db.prepare('UPDATE templates SET draft = ?, version = version + 1 WHERE id = ?').run(
        new TextEncoder().encode(JSON.stringify(draft)), id);
    } finally {
      db.close();
    }
  }, process.env.XKEEN_BROWSER_SCREENSHOT);
  await checkTemplateVariables(page, first, () => admin("reset", first), process.env.XKEEN_BROWSER_SCREENSHOT);
  await page.reload();
  await checkAppendTemplateElement(page, exactID, first, () => admin("reset", first), process.env.XKEEN_BROWSER_SCREENSHOT);
  await checkTemplateCopy(page,exactID,database);
  await page.getByRole('link',{name:'Обзор'}).click();

  await page.getByRole('link',{name:'Подключить роутер'}).click();
  await page.getByRole('heading',{name:'Подключить роутер'}).waitFor();
  await page.getByLabel('Имя роутера').fill('Филиал Восток');
  const sourceChoice = page.getByLabel('Шаблон',{exact:true});
  await page.evaluate(() => {
    const fetchNext = window.fetch;
    window.fetch = async (target, options) => {
      const response = await fetchNext(target, options);
      if (typeof target === 'string' && target.startsWith('/ui/v1/routers/prepare?templateId=')) {
        window.fetch = fetchNext;
        await new Promise(resolve => { window.releaseOldPreparationLookup = resolve; });
      }
      return response;
    };
  });
  await sourceChoice.selectOption(routerTemplate);
  await page.waitForFunction(() => typeof window.releaseOldPreparationLookup === 'function');
  await sourceChoice.selectOption('');
  await page.evaluate(async () => {
    window.releaseOldPreparationLookup();
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  });
  assert.equal(await page.getByText('Черновик · версия 1',{exact:true}).count(),0,'cleared selection must ignore previous reply');
  await sourceChoice.selectOption(routerTemplate);
  await page.getByText('Черновик · версия 1',{exact:true}).waitFor();
  const prepare = page.getByRole('button',{name:'Проверить и подготовить'});
  const protocol = page.getByLabel('protocol',{exact:true});
  const inactiveField = page.getByLabel('inactiveLabel',{exact:true});
  assert.equal(await inactiveField.evaluate(input=>input.readOnly && !input.required),true);
  await page.getByLabel('label',{exact:true}).fill('example-direct');
  const beforeIncomplete = post.length;
  await prepare.click();
  assert.equal(post.length,beforeIncomplete,'required value must stop incomplete confirmation');
  assert.equal(await protocol.evaluate(input => input.validity.valueMissing),true);
  await protocol.fill('freedom');
  const duration = page.getByLabel('timeoutSeconds',{exact:true});
  await duration.fill('invalid');
  await prepare.click();
  await page.getByText('Проверьте значение и его тип.',{exact:true}).waitFor();
  assert.equal(await duration.inputValue(),'invalid');
  await duration.fill('9007199254740993');
  admin('reset', first);
  const revokedRouterForm = page.waitForResponse(response => response.url().endsWith('/ui/v1/routers/prepare') &&
    response.request().method()==='POST' && response.status()===401);
  await prepare.click();
  await revokedRouterForm;
  await page.getByRole('heading',{name:'Сессия истекла'}).waitFor();
  const postsBeforeRouterLogin = post.length;
  await password.fill(first);
  await password.press('Enter');
  await sourceChoice.waitFor();
  assert.equal(await sourceChoice.inputValue(),routerTemplate);
  assert.equal(await page.getByLabel('Имя роутера').inputValue(),'Филиал Восток');
  assert.equal(await duration.inputValue(),'9007199254740993');
  assert.deepEqual(post.slice(postsBeforeRouterLogin),['/ui/v1/login']);

  if (workerSocket) {
    await protocol.fill('not-a-supported-protocol');
    await prepare.click();
    await page.getByText('Xray отклонил конфигурацию. Проверьте значения шаблона.',{exact:true}).waitFor();
    await protocol.fill('freedom');
    await page.evaluate(() => {
      const fetchNext = window.fetch;
      window.fetch = async (target, options) => {
        const response = await fetchNext(target, options);
        if (target === '/ui/v1/routers/prepare' && options?.method === 'POST' && response.status === 201) {
          window.fetch = fetchNext;
          throw new Error('synthetic response loss after commit');
        }
        return response;
      };
    });
  }
  const exactValues = page.waitForRequest(request => request.method()==='POST' &&
    new URL(request.url()).pathname==='/ui/v1/routers/prepare');
  await prepare.click();
  assert.equal((await exactValues).postDataJSON().values.find(value => value.name==='timeoutSeconds').source,'9007199254740993');
  if (workerSocket) {
    await page.getByText('Не удалось подтвердить результат.',{exact:true}).waitFor();
    const beforeLookup = post.length;
    await page.getByRole('button',{name:'Проверить результат'}).click();
    await page.getByRole('heading',{name:'Роутер подготовлен'}).waitFor();
    await page.getByText('Желаемая ревизия 1',{exact:true}).waitFor();
    assert.equal(post.length,beforeLookup);
    const enrolledRouter = await checkRouterEnrollment(page, {origin, dir, cert, post});
    if (enrolledRouter) {
      await checkAgentKeys(page,{origin,dir,cert,database,enrolledRouter});
      await checkRouterOverview(page,{origin, database, enrolledRouter, resetSession: () => admin('reset',first), password:first});
    }
  } else {
    await page.getByText('Проверка Xray сейчас недоступна.',{exact:true}).waitFor();
    assert.equal(await duration.inputValue(),'9007199254740993');
  }
  await page.setViewportSize({width:360,height:740});
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth),360);
  if (process.env.XKEEN_BROWSER_SCREENSHOT) {
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    await page.screenshot({path:process.env.XKEEN_BROWSER_SCREENSHOT + '-router-preparation.png'});
  }
  await page.setViewportSize({width:1080,height:660});
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
  console.log('F04–F11 real HTTPS browser scenarios passed');
} finally {
  if (browser) await browser.close();
  if (server && server.exitCode === null) {
    server.kill('SIGTERM');
    await new Promise(resolve => server.once('exit',resolve));
  }
  rmSync(dir,{recursive:true,force:true});
}
