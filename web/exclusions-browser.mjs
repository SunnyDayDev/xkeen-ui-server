import assert from 'node:assert/strict';

export async function checkExclusionsBrowser(parent, origin, id) {
  const context=await parent.browser().newContext({storageState:await parent.storageState()});
  const page = await context.newPage();
  await page.setViewportSize({width:320,height:650});
  const editor = origin + '/ui/templates/item?' + new URLSearchParams({templateId:id});
  try {
    await page.goto(editor);
    await page.getByRole('link',{name:'Исключения XKeen'}).press('Enter');
    await page.getByText('Настройка ещё не создана.').waitFor();
    const text=page.getByLabel('Список IP и сетей');
    await text.fill('192.0.2.1\n${site.net}');
    await text.press('Tab');
    const add=page.getByRole('button',{name:'Добавить переменную'});
    assert.equal(await add.evaluate(node=>node===document.activeElement),true);
    await page.keyboard.press('Enter');
    await page.getByLabel('Имя',{exact:true}).fill('site.net');
    await page.getByRole('button',{name:'Проверить список'}).press('Enter');
    await page.getByText('Строка 2: нужно значение.').waitFor();
    await page.getByRole('button',{name:'Создать список'}).click();
    await page.getByText('Список сохранён.').waitFor();
    await page.reload();
    await page.getByRole('button',{name:'Сохранить',exact:true}).waitFor();
    assert.equal(await text.inputValue(),'192.0.2.1\n${site.net}');
    await page.getByLabel('Значение по умолчанию').press('Space');
    await page.keyboard.press('Tab');
    assert.equal(await page.getByLabel('IP или сеть',{exact:true}).evaluate(node=>node===document.activeElement),true);
    await page.getByLabel('IP или сеть',{exact:true}).fill('2001:db8::/32');
    await page.getByRole('button',{name:'Проверить список'}).press('Enter');
    await page.locator('pre').getByText('2001:db8::/32',{exact:false}).waitFor();
    const rival=await context.newPage();
    try {
      await rival.goto(page.url());
      await rival.getByRole('button',{name:'Сохранить',exact:true}).waitFor();
      await rival.getByLabel('Список IP и сетей').fill('198.51.100.0/24');
      await rival.getByRole('button',{name:'Сохранить',exact:true}).click();
      await rival.getByText('Список сохранён.').waitFor();
    } finally { await rival.close(); }
    await page.getByRole('button',{name:'Сохранить',exact:true}).press('Enter');
    await page.getByText('Настройки изменились. Ваш текст сохранён в форме.').waitFor();
    assert.equal(await text.inputValue(),'192.0.2.1\n${site.net}');
    const reread=page.getByRole('button',{name:'Перечитать'});
    await reread.waitFor();
    assert.equal(await reread.evaluate(node=>node===document.activeElement&&node.matches(':focus-visible')),true,'stale keyboard focus');
    await page.keyboard.press('Tab');
    assert.equal(await page.getByRole('link',{name:'К шаблону'}).evaluate(node=>node===document.activeElement),true);
    await page.keyboard.press('Shift+Tab');
    assert.equal(await reread.evaluate(node=>node===document.activeElement),true);
    await page.keyboard.press('Enter');
    await page.waitForFunction(()=>document.querySelector('textarea')?.value==='198.51.100.0/24');
    const save=page.getByRole('button',{name:'Сохранить',exact:true});
    assert.equal(await save.evaluate(node=>node===document.activeElement&&node.matches(':focus-visible')),true,'reread keyboard focus');
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth),320);
  } finally { await context.close(); }
}
