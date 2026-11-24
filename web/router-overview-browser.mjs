import { checkLocalRule } from './router-local-rule-browser.mjs';
import { checkRouterVariables } from "./router-variable-browser.mjs";
import assert from 'node:assert/strict';
import { DatabaseSync } from 'node:sqlite';

export async function checkRouterOverview(page,{origin,database,enrolledRouter,resetSession,password}) {
  const overview=page.getByRole('link',{name:'Обзор'});
  const selected=page.getByRole('link',{name:'Филиал Восток',exact:true});
  await page.route('**/ui/v1/routers', async route => {
    await new Promise(resolve=>setTimeout(resolve,200));
    await route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({state:'unavailable'})});
  },{times:1});
  await overview.click();
  await page.getByText('Загружаем роутеры…',{exact:true}).waitFor();
  await page.getByText('Не удалось загрузить список роутеров.',{exact:true}).waitFor();
  await page.getByRole('button',{name:'Повторить'}).click();
  await selected.click();
  assert.equal(new URL(page.url()).searchParams.get('routerId'),enrolledRouter);
  const drawer=page.getByRole('complementary',{name:'Сведения о роутере'});
  await drawer.getByText(enrolledRouter,{exact:true}).waitFor();
  await drawer.getByText('Связь: на связи',{exact:true}).waitFor();
  await drawer.getByText('Применение: нет подтверждения',{exact:true}).waitFor();
  await checkRouterVariables(page, enrolledRouter, resetSession, password);
  await checkLocalRule(page,enrolledRouter,database);
  await drawer.getByRole('button',{name:'Закрыть панель'}).click();
  assert.equal(await selected.evaluate(link=>document.activeElement===link),true);
  await page.goBack();
  await drawer.getByText(enrolledRouter,{exact:true}).waitFor();
  await page.setViewportSize({width:360,height:740});
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth),360);
  if (process.env.XKEEN_BROWSER_SCREENSHOT) await page.screenshot({path:process.env.XKEEN_BROWSER_SCREENSHOT+'-router-overview-mobile.png'});
  await page.setViewportSize({width:1080,height:660});

  const db=new DatabaseSync(database);
  try {
    db.prepare('UPDATE agent_contacts SET contact_ns=?, applied=1, uncertain=1 WHERE router_id=?')
      .run((BigInt(Date.now())-181000n)*1000000n,enrolledRouter);
    await drawer.getByText('Связь: нет связи',{exact:true}).waitFor();
    await drawer.getByText('Последняя подтверждённая ревизия 1',{exact:true}).waitFor();
    await drawer.getByText('Текущее состояние Xray неизвестно.',{exact:true}).waitFor();
    if (process.env.XKEEN_BROWSER_SCREENSHOT) await page.screenshot({path:process.env.XKEEN_BROWSER_SCREENSHOT+'-router-overview.png'});
    await page.evaluate(() => {
      const original = window.fetch;
      window.fetch = async (target, options) => {
        const response = await original(target, options);
        if (target === '/ui/v1/routers' && response.status === 200) {
          window.fetch = original;
          await new Promise(resolve => { window.releaseSyntheticOverview = resolve; });
        }
        return response;
      };
      window.dispatchEvent(new Event('focus'));
    });
    await page.waitForFunction(() => typeof window.releaseSyntheticOverview === 'function');
    resetSession();
    await page.evaluate(() => window.dispatchEvent(new Event('focus')));
    await page.getByRole('heading',{name:'Сессия истекла'}).waitFor();
    assert.equal(await drawer.count(),0);
    db.prepare('INSERT INTO hidden_router_visibility(router_id) VALUES (?)').run(enrolledRouter);
    await page.evaluate(() => { window.releaseSyntheticOverview(); delete window.releaseSyntheticOverview; });
    const pass = page.getByLabel('Пароль администратора');
    await pass.fill(password);
    await pass.press('Enter');
    await drawer.getByText('Роутер недоступен в текущем списке.',{exact:true}).waitFor();
    assert.equal(await drawer.getByText(enrolledRouter,{exact:true}).count(),0);
    assert.equal(await selected.count(),0);
    await page.goto(origin+'/ui/?routerId='+encodeURIComponent(enrolledRouter));
    await drawer.getByText('Роутер недоступен в текущем списке.',{exact:true}).waitFor();
    await page.goto(origin+'/ui/routers/variables?routerId='+encodeURIComponent(enrolledRouter));
    await page.getByText('Роутер недоступен в текущем обзоре.',{exact:true}).waitFor();
    assert.equal(await page.getByLabel('Секция Xray').count(),0);
    await page.getByRole('link',{name:'К роутеру'}).click();
    await drawer.getByText('Роутер недоступен в текущем списке.',{exact:true}).waitFor();

  } finally { db.close(); }
}
