import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';

export async function checkRouterEnrollment(page, {origin, dir, cert, post}) {
  await page.getByRole('link', {name:'Подключить агента'}).click();
  await page.getByRole('heading', {name:'Подключение агента'}).waitFor();
  await page.getByText(origin, {exact:true}).waitFor();
  const issue = page.getByRole('button', {name:'Выдать приглашение',exact:true});
  await issue.click();
  const secret = page.getByRole('textbox',{name:'Одноразовое приглашение'});
  await secret.waitFor();
  const old = await secret.inputValue();
  assert.match(old,/^i1\./);
  assert.ok(!page.url().includes(old));
  assert.equal(await page.evaluate(token => [localStorage,sessionStorage].some(storage =>
    Object.values(storage).some(value => value.includes(token))),old),false);
  await page.setViewportSize({width:360,height:740});
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth),360);
  await page.setViewportSize({width:1080,height:660});
  await page.evaluate(() => window.dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true})));
  await secret.waitFor({state:'detached'});
  await page.reload();
  await page.getByText('Приглашение действует.',{exact:true}).waitFor();
  assert.equal(await secret.count(),0);

  const endpoint = '/ui/v1/routers/enroll';
  const before = post.length;
  await page.getByRole('button',{name:'Заменить приглашение'}).click();
  await page.evaluate(endpoint => {
    const original = window.fetch;
    window.fetch = async (target, options) => {
      const result = await original(target, options);
      if (String(target).startsWith(endpoint+'?') && options?.method === 'POST' && result.status === 201) {
        window.fetch = original;
        throw new Error('synthetic response loss after invitation commit');
      }
      return result;
    };
  },endpoint);
  await page.getByRole('button',{name:'Заменить',exact:true}).click();
  await page.getByText('Не удалось подтвердить выдачу.',{exact:true}).waitFor();
  await page.getByRole('button',{name:'Проверить состояние'}).click();
  await page.getByText('Приглашение действует.',{exact:true}).waitFor();
  assert.deepEqual(post.slice(before),[endpoint]);
  assert.equal(await secret.count(),0);
  await page.evaluate(endpoint => {
    const original = window.fetch;
    window.fetch = async (target, options) => {
      const result = await original(target, options);
      if (String(target).startsWith(endpoint+'?') && options?.method === 'POST' && result.status === 201) {
        window.fetch = original;
        const json = result.json.bind(result);
        result.json = async () => {
          const receipt = await json();
          receipt.expires = new Date(Date.parse(result.headers.get('Date'))+1700).toISOString();
          await new Promise(resolve => setTimeout(resolve,2000));
          return receipt;
        };
      }
      return result;
    };
    new MutationObserver(records => {
      if (records.some(record => [...record.addedNodes].some(node => node instanceof Element &&
          (node.id === 'enrollment-token' || node.querySelector('#enrollment-token'))))) {
        window.syntheticExpiredInvitationWasShown = true;
      }
    }).observe(document.body,{childList:true,subtree:true});
  },endpoint);
  await page.getByRole('button',{name:'Заменить приглашение'}).click();
  await page.getByRole('button',{name:'Заменить',exact:true}).click();
  await page.getByText('Приглашение действует.',{exact:true}).waitFor();
  assert.equal(await page.evaluate(() => !!window.syntheticExpiredInvitationWasShown),false);
  const enrollmentLocation = page.url();
  await page.evaluate(endpoint => {
    const original = window.fetch;
    window.fetch = async (target, options) => {
      const result = await original(target, options);
      if (String(target).startsWith(endpoint+'?') && options?.method === 'POST' && result.status === 201) {
        window.fetch = original;
        await new Promise(resolve => { window.releaseSyntheticReceipt = resolve; });
      }
      return result;
    };
  },endpoint);
  await page.getByRole('button',{name:'Заменить приглашение'}).click();
  await page.getByRole('button',{name:'Заменить',exact:true}).click();
  await page.waitForFunction(() => typeof window.releaseSyntheticReceipt === 'function');
  await page.getByRole('link',{name:'Обзор'}).click();
  await page.evaluate(() => { window.releaseSyntheticReceipt(); delete window.releaseSyntheticReceipt; });
  assert.equal(await secret.count(),0);
  await page.goto(enrollmentLocation);
  await page.getByText('Приглашение действует.',{exact:true}).waitFor();
  assert.equal(await secret.count(),0);
  await page.getByRole('button',{name:'Заменить приглашение'}).click();
  await page.getByRole('button',{name:'Заменить',exact:true}).click();
  await secret.waitFor();
  const fresh = await secret.inputValue();
  assert.match(fresh,/^i1\./);
  assert.notEqual(fresh,old);
  if (!process.env.XKEEN_TEST_AGENT_BINARY) return;
  const agentDir = join(dir,'router-agent');
  mkdirSync(agentDir,{mode:0o700});
  writeFileSync(join(agentDir,'invitation'),fresh+'\n',{mode:0o600});
  const agent = spawn(resolve(process.env.XKEEN_TEST_AGENT_BINARY),
    ['--server',origin,'--state-dir',agentDir,'--ca-file',cert],{stdio:['ignore','ignore','pipe']});
  let safe = true;
  agent.stderr.on('data',data => { if (data.includes(fresh)) safe = false; });
  try {
    await page.getByText('Агент зарегистрирован.',{exact:true}).waitFor({timeout:20000});
    await secret.waitFor({state:'detached'});
    await page.getByText('Применение: нет подтверждения',{exact:true}).waitFor();
    assert.equal(safe,true);
  } finally {
    const end = new Promise(resolve => agent.once('exit',resolve));
    if (agent.exitCode === null && agent.signalCode === null) {
      agent.kill('SIGTERM');
      await end;
    }
  }
}
