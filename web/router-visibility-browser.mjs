import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { request } from 'node:https';
import { join } from 'node:path';

export async function checkRouterVisibility(page, {origin, dir, cert, enrolledRouter, resetSession, password}) {
  const credential=JSON.parse(readFileSync(join(dir,'agent-recovery','credentials.json'),'utf8')).state;
  assert.equal(credential.RouterID,enrolledRouter);
  async function heartbeat() {
    await new Promise((resolve,reject) => {
      const req=request(origin+'/agent/v1/contact',{method:'POST',ca:readFileSync(cert),headers:{Authorization:'Bearer '+credential.Key}},res => {
        res.resume();
        res.on('end',()=>res.statusCode===204?resolve():reject(new Error('synthetic heartbeat failed')));
      });
      req.on('error',reject);
      req.end();
    });
  }

  const pathname='/ui/v1/routers/visibility';
  const actions=[];
  const log = req => {
    if (new URL(req.url()).pathname===pathname && req.method()==='POST') actions.push(req);
  };
  page.on('request',log);
  const viewport=page.viewportSize();
  await page.setViewportSize({width:320,height:740});
  try {
    await page.goto(origin+'/ui/');
    const list=page.getByRole('region',{name:'Подключённые роутеры'});
    const row=list.getByRole('link',{name:'Филиал Восток',exact:true});
    const drawer=page.getByRole('complementary',{name:'Сведения о роутере'});
    const offer=drawer.getByRole('button',{name:'Скрыть из списка',exact:true});
    const cancel=drawer.getByRole('button',{name:'Отмена',exact:true});
    const confirm=drawer.getByRole('button',{name:'Скрыть роутер',exact:true});
    await row.press('Enter');
    await offer.press('Enter');
    await cancel.waitFor();
    if(process.env.XKEEN_BROWSER_SCREENSHOT) await page.screenshot({path:process.env.XKEEN_BROWSER_SCREENSHOT+'-router-hide.png'});
    assert.equal(await cancel.evaluate(node=>node===document.activeElement&&node.matches(':focus-visible')),true);
    await page.keyboard.press('Enter');
    await offer.waitFor();
    assert.equal(await offer.evaluate(node=>node===document.activeElement),true);
    assert.equal(actions.length,0);

    await page.keyboard.press('Enter');
    await cancel.waitFor();
    await heartbeat();
    await page.keyboard.press('Shift+Tab');
    assert.equal(await confirm.evaluate(node=>node===document.activeElement),true);
    await page.keyboard.press('Enter');
    await page.getByText('Роутер скрыт. Следующий допустимый контакт вернёт его в список.',{exact:true}).waitFor();
    await drawer.waitFor({state:'detached'});
    assert.equal(await list.evaluate(node=>node===document.activeElement),true);
    await row.waitFor({state:'detached'});
    assert.equal(actions.length,1);
    await heartbeat();
    await row.waitFor({timeout:8000});
    assert.equal(await drawer.count(),0);

    await page.evaluate(path => {
      const original=window.fetch;
      window.fetch=async (target,options)=>{
        const result=await original(target,options);
        if(String(target).startsWith(path+'?')&&options?.method==='POST'&&result.status===200) {
          window.fetch=original;
          throw new Error('synthetic response loss after hide');
        }
        return result;
      };
    },pathname);
    await row.press('Enter');
    await offer.press('Enter');
    await confirm.press('Enter');
    const inspect=drawer.getByRole('button',{name:'Проверить состояние',exact:true});
    await inspect.waitFor();
    assert.equal(actions.length,2);
    await inspect.press('Enter');
    await page.getByText('Роутер сейчас скрыт.',{exact:true}).waitFor();
    await drawer.waitFor({state:'detached'});
    assert.equal(actions.length,2);
    await heartbeat();
    await row.waitFor({timeout:8000});
    assert.equal(await drawer.count(),0);
    await row.press('Enter');
    await offer.press('Enter');
    await page.evaluate(path => {
      const original=window.fetch;
      window.fetch=async (target,options)=>{
        const result=await original(target,options);
        if(String(target).startsWith(path+'?')&&options?.method==='POST'&&result.status===200) {
          window.fetch=original;
          await new Promise(resolve=>{window.releaseHideReply=resolve;});
        }
        return result;
      };
    },pathname);
    await confirm.press('Enter');
    await page.waitForFunction(()=>typeof window.releaseHideReply==='function');
    resetSession();
    await page.evaluate(()=>window.dispatchEvent(new Event('focus')));
    await page.getByRole('heading',{name:'Сессия истекла'}).waitFor();
    await page.evaluate(()=>window.releaseHideReply());
    const login=page.getByLabel('Пароль администратора');
    await login.fill(password);
    await login.press('Enter');
    await inspect.waitFor();
    assert.equal(actions.length,3);
    await inspect.press('Enter');
    await page.getByText('Роутер сейчас скрыт.',{exact:true}).waitFor();
    assert.equal(actions.length,3);
    await heartbeat();
    await row.waitFor({timeout:8000});
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth),320);
    await page.getByRole('link',{name:'Шаблоны',exact:true}).press('Enter');
  } finally {
    page.off('request',log);
    await page.setViewportSize(viewport);
  }
}
