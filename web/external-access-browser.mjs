import assert from 'node:assert/strict';

export async function checkExternalAccess(page, reset, password) {
  const viewport=page.viewportSize();
  await page.setViewportSize({width:320,height:650});
  await page.getByRole('link', {name:'Настройки'}).press('Enter',{timeout:2500});
  const enabled=page.getByRole('checkbox',{name:'Внешний API'});
  const protectedMode=page.getByRole('checkbox',{name:'Авторизация API'});
  const save=page.getByRole('button',{name:'Сохранить настройки'});
  await enabled.waitFor();
  assert.equal(await enabled.isChecked(),false);
  assert.equal(await protectedMode.isChecked(),true);
  await enabled.press('Space');
  await page.keyboard.press('Tab');
  assert.equal(await protectedMode.evaluate(node=>node===document.activeElement),true);
  await page.keyboard.press('Space');
  await page.keyboard.press('Tab');
  assert.equal(await save.evaluate(node=>node.matches(':focus-visible')),true);
  await page.keyboard.press('Enter');
  await page.getByRole('heading',{name:'Токен не требуется'}).waitFor();
  await protectedMode.check(); await save.click();
  await page.getByRole('button',{name:'Выпустить токен'}).click();
  const secret=page.getByLabel('Новый токен');
  const before=await secret.inputValue(); assert.equal(before.length,43);
  await page.getByRole('button',{name:'Скрыть токен'}).press('Enter');
  assert.equal(await secret.count(),0);
  const replace=page.getByRole('button',{name:'Заменить токен'});
  assert.equal(await replace.evaluate(node=>node===document.activeElement&&node.matches(':focus-visible')),true,'token conceal focus');
  await page.keyboard.press('Enter');
  const cancel=page.getByRole('button',{name:'Отмена',exact:true});
  await cancel.waitFor();
  assert.equal(await cancel.evaluate(node=>node===document.activeElement&&node.matches(':focus-visible')),true,'token cancellation focus');
  await page.keyboard.press('Enter');
  await replace.waitFor();
  assert.equal(await replace.evaluate(node=>node===document.activeElement),true,'token action focus after cancellation');
  await page.keyboard.press('Enter');
  await cancel.waitFor();
  await page.keyboard.press('Shift+Tab');
  const confirm=page.getByRole('button',{name:'Подтвердить замену'});
  assert.equal(await confirm.evaluate(node=>node===document.activeElement),true,'token confirmation focus');
  await page.keyboard.press('Enter');
  const after=await secret.inputValue(); assert.notEqual(after,before);
  async function expire() {
    reset();
    await page.evaluate(() => window.dispatchEvent(new Event('focus')));
    await page.getByRole('heading',{name:'Сессия истекла'}).waitFor();
    assert.equal(await page.locator('#issued-token').count(),0);
  }
  async function loginAgain() {
    await page.getByLabel('Пароль администратора').fill(password);
    await page.getByLabel('Пароль администратора').press('Enter');
    await page.getByRole('button',{name:'Заменить токен'}).waitFor();
    assert.equal(await page.locator('#issued-token').count(),0);
  }
  await expire();
  await loginAgain();
  await page.evaluate(() => {
    const original = window.fetch;
    window.fetch = async (...args) => {
      const reply = await original(...args);
      if (String(args[0]).endsWith('/external-api/token')) {
        window.fetch = original;
        window.fixtureIssuedStatus = reply.status;
        await new Promise(resolve => {window.releaseFixtureIssue = resolve;});
      }
      return reply;
    };
  });
  await page.getByRole('button',{name:'Заменить токен'}).click();
  await page.getByRole('button',{name:'Подтвердить замену'}).click();
  await page.waitForFunction(() => window.fixtureIssuedStatus === 200);
  await expire();
  await page.evaluate(() => window.releaseFixtureIssue());
  await loginAgain();
  await page.getByRole('link',{name:'Обзор'}).click();
  assert.equal(await secret.count(),0);
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth),320);
  await page.setViewportSize(viewport);
}
