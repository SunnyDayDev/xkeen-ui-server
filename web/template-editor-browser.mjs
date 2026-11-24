import assert from 'node:assert/strict';

export async function checkTemplateEditor(page, id, password, reset, addSibling, screenshotPrefix) {
  const selector = page.getByRole('link', {name:id.trim(), exact:true});
  await selector.click();
  await page.getByRole('heading', {name:'Первый элемент'}).waitFor();
  await page.getByLabel('Секция Xray').selectOption('outbounds');
  const input = page.getByLabel('JSON', {exact:true});
  const raw = '{\r\n  "tag": "example-outbound",\n  "путь": "orig",\r\n  "n": 9007199254740993\n}\r\n';
  const paste = async () => input.evaluate((area, value) => {
    area.focus();
    const clipboardData = new DataTransfer();
    clipboardData.setData('text/plain', value);
    area.dispatchEvent(new ClipboardEvent('paste',{clipboardData,bubbles:true,cancelable:true}));
  }, raw);
  await paste();
  assert.equal(await input.inputValue(),raw.replaceAll('\r\n','\n'));
  await input.evaluate(area => {
    const start = area.value.indexOf('orig');
    area.setSelectionRange(start, start + 4);
  });
  await page.keyboard.insertText('правка');
  assert.match(await input.inputValue(),/правка/);
  await input.press(process.platform === 'darwin' ? 'Meta+z' : 'Control+z');
  assert.equal(await input.inputValue(),raw.replaceAll('\r\n','\n'));
  await page.getByRole('button',{name:'Создать элемент'}).click();
  await page.getByRole('heading',{name:'Сырой JSON элемента'}).waitFor();
  const state = await page.evaluate(async id => {
    const response = await fetch('/ui/v1/templates/item?'+new URLSearchParams({templateId:id}));
    return response.json();
  },id);
  assert.equal(state.selected.source,raw);
  assert.equal(state.version,'2');

  await input.fill('{"слово": }');
  await page.getByRole('button',{name:'Сохранить черновик'}).click();
  await page.getByRole('alert').getByText(/Строка 1, столбец/).waitFor();
  assert.equal(await input.inputValue(),'{"слово": }');
  await input.fill('{"tag":"draft.example"}');
  const competing = await page.evaluate(async ({id, state}) => {
    const session = await (await fetch('/ui/v1/session')).json();
    const response = await fetch('/ui/v1/templates/item?'+new URLSearchParams({templateId:id}), {
      method:'POST', headers:{'Content-Type':'application/json','X-CSRF-Token':session.csrf},
      body:JSON.stringify({version:state.version,elementId:state.selected.id,source:'{"tag":"winner.example"}'})
    });
    return response.status;
  },{id,state});
  assert.equal(competing,200);
  await page.getByRole('button',{name:'Сохранить черновик'}).click();
  await page.getByText('Шаблон изменился в другой вкладке. Ваш JSON остался в редакторе.').waitFor();
  await page.getByRole('button',{name:'Показать актуальную версию'}).click();
  await page.getByText('{"tag":"winner.example"}',{exact:true}).waitFor();
  assert.equal(await input.inputValue(),'{"tag":"draft.example"}');
  await page.setViewportSize({width:390,height:730});
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth),390);
  if(screenshotPrefix) await page.screenshot({path:screenshotPrefix+'-editor.png'});
  await page.setViewportSize({width:1080,height:660});

  await page.getByRole('button',{name:'Продолжить от актуальной версии'}).click();
  let unblock, requestEntered;
  const gate = new Promise(resolve => { unblock = resolve; });
  const queued = new Promise(resolve => { requestEntered = resolve; });
  const hold = async route => {
    if (route.request().method() !== 'POST') return route.continue();
    requestEntered();
    await gate;
    await route.abort();
  };
  await page.route(/\/ui\/v1\/templates\/item\?/,hold);
  await page.getByRole('button',{name:'Сохранить черновик'}).click();
  await Promise.race([queued,new Promise((_,reject)=>setTimeout(()=>reject(new Error('save did not reach browser transport')),5000))]);
  const rotated = await page.evaluate(async pass => (await fetch('/ui/v1/login',{
    method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({password:pass})
  })).status,password);
  assert.equal(rotated,200);
  const statusRefreshed = page.waitForResponse(response => response.url().endsWith('/ui/v1/session'));
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await statusRefreshed;
  unblock();
  await page.getByText('Результат сохранения неизвестен. Проверьте актуальную версию.').waitFor();
  await page.unroute(/\/ui\/v1\/templates\/item\?/,hold);
  assert.ok(await input.isEnabled());
  assert.equal(await input.inputValue(),'{"tag":"draft.example"}');

  reset();
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await page.getByRole('heading',{name:'Сессия истекла'}).waitFor();
  let automaticSaves = 0;
  const countSaves = request => {
    if (request.method()==='POST' && request.url().includes('/ui/v1/templates/item?')) automaticSaves++;
  };
  page.on('request',countSaves);
  const enter = page.getByLabel('Пароль администратора');
  await enter.fill(password);
  await enter.press('Enter');
  await input.waitFor();
  assert.equal(await input.inputValue(),'{"tag":"draft.example"}');
  assert.equal(automaticSaves,0);
  page.off('request',countSaves);

  const sibling = '550e8400-e29b-41d4-a716-446655440019';
  addSibling(id, sibling);
  await page.getByRole('link',{name:'Шаблоны'}).click();
  await page.getByRole('link',{name:id.trim(),exact:true}).click();
  await input.waitFor();
  await input.fill('{"tag":"held.example"}');
  let releaseSelection, selectionEntered;
  const selectionGate = new Promise(resolve => { releaseSelection = resolve; });
  const queuedSelection = new Promise(resolve => { selectionEntered = resolve; });
  const selectsSibling = url => url.pathname === '/ui/v1/templates/item' && url.searchParams.get('elementId') === sibling;
  const holdSelection = async route => {
    selectionEntered();
    await selectionGate;
    await route.abort().catch(() => {});
  };
  await page.route(selectsSibling, holdSelection);
  await page.getByRole('combobox').nth(1).selectOption(sibling);
  await Promise.race([queuedSelection,new Promise((_,reject)=>setTimeout(()=>reject(new Error('selection did not reach browser transport')),5000))]);
  const rotatedSelection = await page.evaluate(async pass => (await fetch('/ui/v1/login',{
    method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({password:pass})
  })).status,password);
  assert.equal(rotatedSelection,200);
  const observedSelection = page.waitForResponse(response => response.url().endsWith('/ui/v1/session'));
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await observedSelection;
  releaseSelection();
  await input.waitFor();
  assert.equal(await input.inputValue(),'{"tag":"held.example"}');
  await page.unroute(selectsSibling,holdSelection);
}
