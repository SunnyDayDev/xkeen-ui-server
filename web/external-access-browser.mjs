import assert from 'node:assert/strict';

export async function checkExternalAccess(page, reset, password) {
  await page.getByRole('link', {name:'Настройки'}).click({timeout:2500});
  const enabled=page.getByRole('checkbox',{name:'Внешний API'});
  const protectedMode=page.getByRole('checkbox',{name:'Авторизация API'});
  const save=page.getByRole('button',{name:'Сохранить настройки'});
  await enabled.waitFor();
  assert.equal(await enabled.isChecked(),false);
  assert.equal(await protectedMode.isChecked(),true);
  await enabled.check(); await protectedMode.uncheck(); await save.click();
  await page.getByRole('heading',{name:'Токен не требуется'}).waitFor();
  await protectedMode.check(); await save.click();
  await page.getByRole('button',{name:'Выпустить токен'}).click();
  const secret=page.getByLabel('Новый токен');
  const before=await secret.inputValue(); assert.equal(before.length,43);
  await page.getByRole('button',{name:'Скрыть токен'}).click();
  assert.equal(await secret.count(),0);
  await page.getByRole('button',{name:'Заменить токен'}).click();
  await page.getByRole('button',{name:'Подтвердить замену'}).click();
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
}
