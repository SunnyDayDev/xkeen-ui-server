import assert from 'node:assert/strict';
import { DatabaseSync } from 'node:sqlite';

export async function checkRouterRuleOrder(page, routerId, database, resetSession, password) {
  const panel = page.getByRole('complementary',{name:'Сведения о роутере'});
  await panel.getByRole('link',{name:'Маршрутизация',exact:true}).click();
  const list=page.getByRole('list',{name:'Полный порядок правил'});
  await list.waitFor();
  const item=label=>list.getByRole('listitem').filter({has:page.getByText(label,{exact:true})});
  const moveUp=label=>item(label).getByRole('button',{name:'Выше: '+label,exact:true});
  const order=async()=>await list.locator('bdi').allTextContents();
  const expected=['competing-local','common-rule','disabled-rule','new-local'];
  assert.deepEqual(await order(),expected);

  await moveUp('new-local').press('Enter');
  await page.waitForFunction(()=>[...document.querySelectorAll('ol[aria-label="Полный порядок правил"] bdi')][2]?.textContent==='new-local');
  await moveUp('new-local').press('Enter');
  await page.waitForFunction(()=>[...document.querySelectorAll('ol[aria-label="Полный порядок правил"] bdi')][1]?.textContent==='new-local');
  await moveUp('new-local').press('Enter');
  await page.waitForFunction(()=>[...document.querySelectorAll('ol[aria-label="Полный порядок правил"] bdi')][0]?.textContent==='new-local');
  await page.waitForFunction(()=>document.activeElement?.closest('li')?.querySelector('bdi')?.textContent==='new-local');
  assert.deepEqual(await order(),['new-local','competing-local','common-rule','disabled-rule']);

  await item('disabled-rule').dragTo(item('common-rule'));
  await page.waitForFunction(()=>[...document.querySelectorAll('ol[aria-label="Полный порядок правил"] bdi')][2]?.textContent==='disabled-rule');
  const moved=['new-local','competing-local','disabled-rule','common-rule'];
  assert.deepEqual(await order(),moved);
  await item('disabled-rule').getByText('Общее · отключено',{exact:true}).waitFor();

  const db=new DatabaseSync(database);
  try {
    db.prepare('UPDATE router_settings SET generation=generation+1 WHERE id=?').run(routerId);
    await moveUp('disabled-rule').press('Enter');
    await page.getByText('Порядок изменился. Перечитайте настройки и повторите перемещение.',{exact:true}).waitFor();
    assert.deepEqual(await order(),moved);
    await page.getByRole('button',{name:'Перечитать настройки',exact:true}).press('Enter');
    const reset=page.getByRole('button',{name:'Вернуть порядок шаблона',exact:true});
    await reset.waitFor();
    await reset.press('Enter');
    const confirm=page.getByRole('button',{name:'Вернуть порядок',exact:true});
    await confirm.waitFor();
    assert.deepEqual(await order(),moved);
    await page.getByRole('button',{name:'Отмена',exact:true}).press('Enter');
    await reset.press('Enter');
    await confirm.press('Enter');
    await page.getByText('Порядок шаблона восстановлен.',{exact:true}).waitFor();
    const restored=['new-local','competing-local','common-rule','disabled-rule'];
    assert.deepEqual(await order(),restored);
    await page.reload();
    await item('new-local').waitFor();
    assert.deepEqual(await order(),restored);

    const generation = () => db.prepare('SELECT generation FROM router_settings WHERE id=?').get(routerId).generation;
    const beforeReset = generation();
    let signalRequest;
    let releaseResponse;
    const pending = new Promise(resolve=>{signalRequest=resolve;});
    const held = new Promise(resolve=>{releaseResponse=resolve;});
    await page.route('**/ui/v1/routers/rules?*',async route=>{
      signalRequest();
      await held;
      await route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({state:'unavailable'})}).catch(()=>{});
    },{times:1});
    try {
      await moveUp('disabled-rule').press('Enter');
      await pending;
      resetSession();
      await page.evaluate(()=>window.dispatchEvent(new Event('focus')));
      await page.getByRole('heading',{name:'Сессия истекла'}).waitFor();
      const field=page.getByLabel('Пароль администратора');
      await field.fill(password);
      await field.press('Enter');
      await page.getByText('Результат сохранения неизвестен. Перечитайте настройки.',{exact:true}).waitFor();
      releaseResponse();
      await page.getByRole('button',{name:'Перечитать настройки',exact:true}).press('Enter');
      await moveUp('disabled-rule').waitFor();
      assert.deepEqual(await order(),restored);
      assert.equal(generation(),beforeReset);
    } finally { releaseResponse(); }
  } finally { db.close(); }
  await page.setViewportSize({width:320,height:740});
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth),320);
  if(process.env.XKEEN_BROWSER_SCREENSHOT) {
    await page.evaluate(()=>window.scrollTo(0,0));
    await page.screenshot({path:process.env.XKEEN_BROWSER_SCREENSHOT+'-rule-order-narrow.png'});
  }
  await page.setViewportSize({width:1080,height:660});
  await page.getByRole('link',{name:'К роутеру'}).click();
  await panel.getByText(routerId,{exact:true}).waitFor();
}
