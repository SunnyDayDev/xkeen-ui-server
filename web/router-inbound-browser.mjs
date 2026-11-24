import assert from 'node:assert/strict';
import { DatabaseSync } from 'node:sqlite';

export async function checkRouterInbound(page, routerId, database, resetSession, password) {
  const panel=page.getByRole('complementary',{name:'Сведения о роутере'});
  await panel.getByRole('link',{name:'Входы',exact:true}).click();
  await page.getByRole('heading',{name:'Входы роутера',exact:true}).waitFor();
  const used='550e8400-e29b-41d4-a716-446655440011';
  const free='550e8400-e29b-41d4-a716-446655440012';
  const unsupported='550e8400-e29b-41d4-a716-446655440004';
  const picker=page.getByLabel('Выбранный вход',{exact:true});
  await picker.waitFor();
  assert.equal(await picker.inputValue(),used);
  await page.getByText('Используется в итоговом роутинге',{exact:true}).waitFor();
  await page.getByText('common-rule',{exact:true}).waitFor();
  const disable=page.getByRole('button',{name:'Выключить',exact:true});
  const remove=page.getByRole('button',{name:'Удалить из настроек роутера',exact:true});
  assert.equal(await disable.count(),0);
  assert.equal(await remove.count(),0);

  await picker.selectOption(unsupported);
  await page.getByText('Не удалось подтвердить зависимости. Перечитайте настройки.',{exact:true}).waitFor();
  assert.equal(await remove.count(),0);
  await picker.selectOption(free);
  await disable.waitFor();
  const db=new DatabaseSync(database);
  try {
    const current=()=>db.prepare('SELECT generation, snapshot FROM router_settings WHERE id=?').get(routerId);
    db.prepare('UPDATE router_settings SET generation=generation+1 WHERE id=?').run(routerId);
    const before=current().generation;
    await disable.press('Enter');
    await page.getByText('Настройки изменились. Перечитайте зависимости и повторите действие.',{exact:true}).waitFor();
    assert.equal(current().generation,before);
    await page.getByRole('button',{name:'Перечитать настройки',exact:true}).press('Enter');
    await disable.waitFor();
    assert.equal(await picker.inputValue(),free);
    await disable.press('Enter');
    await page.getByText('Черновик сохранён. Вход выключен.',{exact:true}).waitFor();
    await remove.waitFor();
    assert.equal(current().generation,before+1);
    await remove.press('Enter');
    await page.getByRole('button',{name:'Отмена',exact:true}).press('Enter');
    assert.equal(current().generation,before+1);
    await remove.press('Enter');
    await page.getByRole('button',{name:'Удалить',exact:true}).press('Enter');
    await page.getByText('Черновик сохранён. Вход удалён.',{exact:true}).waitFor();
    assert.equal(current().generation,before+2);
    const stored=JSON.parse(new TextDecoder().decode(current().snapshot));
    assert.deepEqual(stored.Groups.find(group=>group.Name==='inbounds').Exclusions,
      [{TemplateID:stored.TemplateID,ElementID:free}]);
    assert.ok(!stored.DisabledInbounds?.includes(free));
    resetSession();
    await page.evaluate(()=>window.dispatchEvent(new Event('focus')));
    await page.getByRole('heading',{name:'Сессия истекла'}).waitFor();
    const field=page.getByLabel('Пароль администратора');
    await field.fill(password);
    await field.press('Enter');
    await picker.waitFor();
    assert.equal(await picker.getByRole('option').filter({hasText:'free-inbound'}).count(),0);
    assert.equal(current().generation,before+2);

    const groups = stored.Groups.find(group=>group.Name==='inbounds');
    groups.Exclusions = [used,unsupported].map(ElementID=>({TemplateID:stored.TemplateID,ElementID}));
    db.prepare('UPDATE router_settings SET snapshot=?,generation=generation+1 WHERE id=?')
      .run(Buffer.from(JSON.stringify(stored)),routerId);
    await page.reload();
    await picker.waitFor();
    assert.equal(await picker.inputValue(),free);
    await remove.press('Enter');
    await page.getByRole('button',{name:'Удалить',exact:true}).press('Enter');
    await page.getByText('Входов пока нет.',{exact:true}).waitFor();
    await page.waitForFunction(()=>document.activeElement?.textContent?.trim()==='К роутеру');
  } finally { db.close(); }
  await page.setViewportSize({width:320,height:740});
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth),320);
  if(process.env.XKEEN_BROWSER_SCREENSHOT) {
    await page.evaluate(()=>window.scrollTo(0,0));
    await page.screenshot({path:process.env.XKEEN_BROWSER_SCREENSHOT+'-inbounds-narrow.png'});
  }
  await page.setViewportSize({width:1080,height:660});
  await page.getByRole('link',{name:'К роутеру'}).click();
  await panel.getByText(routerId,{exact:true}).waitFor();
}
