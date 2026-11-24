import assert from 'node:assert/strict';

export async function checkExclusionsBrowser(parent, origin, id) {
  const context=await parent.browser().newContext({storageState:await parent.storageState()});
  const page = await context.newPage();
  const editor = origin + '/ui/templates/item?' + new URLSearchParams({templateId:id});
  try {
    await page.goto(editor);
    await page.getByRole('link',{name:'Исключения XKeen'}).click();
    await page.getByText('Настройка ещё не создана.').waitFor();
    const text=page.getByLabel('Список IP и сетей');
    await text.fill('192.0.2.1\n${site.net}');
    await page.getByRole('button',{name:'Добавить переменную'}).click();
    await page.getByLabel('Имя',{exact:true}).fill('site.net');
    await page.getByRole('button',{name:'Проверить список'}).click();
    await page.getByText('Строка 2: нужно значение.').waitFor();
    await page.getByRole('button',{name:'Создать список'}).click();
    await page.getByText('Список сохранён.').waitFor();
    await page.reload();
    await page.getByRole('button',{name:'Сохранить',exact:true}).waitFor();
    assert.equal(await text.inputValue(),'192.0.2.1\n${site.net}');
    await page.getByLabel('Значение по умолчанию').check();
    await page.getByLabel('IP или сеть',{exact:true}).fill('2001:db8::/32');
    await page.getByRole('button',{name:'Проверить список'}).click();
    await page.locator('pre').getByText('2001:db8::/32',{exact:false}).waitFor();
    const rival=await context.newPage();
    try {
      await rival.goto(page.url());
      await rival.getByRole('button',{name:'Сохранить',exact:true}).waitFor();
      await rival.getByLabel('Список IP и сетей').fill('198.51.100.0/24');
      await rival.getByRole('button',{name:'Сохранить',exact:true}).click();
      await rival.getByText('Список сохранён.').waitFor();
    } finally { await rival.close(); }
    await page.getByRole('button',{name:'Сохранить',exact:true}).click();
    await page.getByText('Настройки изменились. Ваш текст сохранён в форме.').waitFor();
    assert.equal(await text.inputValue(),'192.0.2.1\n${site.net}');
    await page.getByRole('button',{name:'Перечитать'}).click();
    await page.waitForFunction(()=>document.querySelector('textarea')?.value==='198.51.100.0/24');
    await page.setViewportSize({width:360,height:650});
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth),360);
  } finally { await context.close(); }
}
