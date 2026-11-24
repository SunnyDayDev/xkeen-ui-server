import assert from 'node:assert/strict';

export async function checkTemplateVariables(page, password, reset, screenshotPrefix) {
  const input = page.getByLabel('JSON',{exact:true});
  const saveRaw = page.getByRole('button',{name:'Сохранить черновик'});
  const notice = page.getByText('Черновик сохранён.',{exact:true});
  const ref = name => '$'+'{'+name+'}';
  const base = {tag:'variable.example',port:ref('port'),flag:ref('flag'),object:ref('object'),list:ref('list')};
  await input.fill(JSON.stringify({...base,old:ref('old')}));
  await saveRaw.click();
  await notice.waitFor();
  const raw = JSON.stringify(base);
  await input.fill(raw);
  await saveRaw.click();
  await notice.waitFor();
  await page.getByRole('button',{name:'Переменные',exact:true}).click();
  await page.getByRole('heading',{name:'Переменные элемента'}).waitFor();
  if(screenshotPrefix) await page.screenshot({path:screenshotPrefix+'-variables-wide.png'});

  const type = name => page.getByLabel('Тип '+name,{exact:true});
  const value = name => page.getByLabel('Значение по умолчанию '+name,{exact:true});
  const save = page.getByRole('button',{name:'Сохранить определения'});
  const saved = page.getByText('Определения сохранены.',{exact:true});
  const snapshot = () => page.evaluate(async () => {
    const id = new URL(location.href).searchParams.get('templateId');
    const template = await (await fetch('/ui/v1/templates/item?'+new URLSearchParams({templateId:id}))).json();
    const url = '/ui/v1/templates/variables?'+new URLSearchParams({templateId:id,elementId:template.selected.id});
    return {url, data:await (await fetch(url)).json()};
  });
  assert.equal(await value('port').inputValue(),'""');
  await type('port').selectOption('number');
  await value('port').fill('');
  await type('flag').selectOption('boolean');
  await value('flag').fill('false');
  await type('object').selectOption('object');
  await value('object').fill('{}');
  await type('list').selectOption('array');
  await value('list').fill('[]');
  await save.click();
  await saved.waitFor();
  const first = await snapshot();
  const rows = new Map(first.data.definitions.map(row=>[row.name,row]));
  assert.equal(rows.get('port').required,true);
  assert.equal(rows.get('port').defaultSource,undefined);
  assert.equal(rows.get('old').referenced,false);
  for (const [name,text] of [['flag','false'],['object','{}'],['list','[]']]) {
    assert.equal(rows.get(name).required,false);
    assert.equal(rows.get(name).defaultSource,text);
  }

  await value('port').fill('{');
  await save.click();
  await page.getByRole('alert').getByText(/синтаксис JSON/i).waitFor();
  assert.equal(await value('port').inputValue(),'{');
  assert.equal((await snapshot()).data.version,first.data.version);
  await value('port').fill('"443"');
  await save.click();
  await saved.waitFor();
  await page.getByText('Дефолт должен соответствовать выбранному типу.',{exact:true}).waitFor();
  const invalid = await snapshot();
  assert.equal(invalid.data.definitions.find(row=>row.name==='port').issue,'type_mismatch');

  await value('port').fill('0');
  reset();
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await page.getByRole('heading',{name:'Сессия истекла'}).waitFor();
  const auth = page.getByLabel('Пароль администратора');
  await auth.fill(password);
  await auth.press('Enter');
  await value('port').waitFor();
  assert.equal(await value('port').inputValue(),'0');
  assert.equal((await snapshot()).data.version,invalid.data.version);

  const raced = await page.evaluate(async ({url,data}) => {
    const session = await (await fetch('/ui/v1/session')).json();
    const definitions = data.definitions.map(({name,type,defaultSource})=>({name,type,defaultSource}));
    definitions.find(row=>row.name==='port').defaultSource='1';
    return (await fetch(url,{method:'POST',
      headers:{'Content-Type':'application/json','X-CSRF-Token':session.csrf},
      body:JSON.stringify({version:data.version,definitions})})).status;
  },invalid);
  assert.equal(raced,200);
  await save.click();
  await page.getByText('Шаблон изменился. Ваши определения остались в редакторе.').waitFor();
  assert.equal(await value('port').inputValue(),'0');
  await page.setViewportSize({width:390,height:730});
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth),390);
  if(screenshotPrefix) {
    await page.evaluate(()=>window.scrollTo(0,0));
    await page.screenshot({path:screenshotPrefix+'-variables.png'});
  }
  await page.setViewportSize({width:1080,height:660});
  await page.getByRole('button',{name:'К JSON',exact:true}).click();
  assert.equal(await input.inputValue(),raw);
}
