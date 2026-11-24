import assert from 'node:assert/strict';

export async function checkRouterReplacement(page, routerId, element, resetSession, password) {
  const variableForm = page.getByRole('heading',{name:'Персональные переменные'});
  const selectedLabel = page.getByRole('group',{name:'label · string'});
  const modeLink = page.getByRole('link',{name:'Свой JSON',exact:true});
  await modeLink.press('Enter');
  const heading = page.getByRole('heading',{name:'Свой JSON элемента'});
  await heading.waitFor();
  assert.equal(new URL(page.url()).searchParams.get('elementId'),element);
  const input = page.getByLabel('JSON',{exact:true});
  const save = page.getByRole('button',{name:'Сохранить черновик'});
  const success = page.getByText('Черновик сохранён. Действующая конфигурация не изменилась.',{exact:true});
  const snapshot = () => page.evaluate(async ({routerId,element}) => {
    const url='/ui/v1/routers/replacement?'+new URLSearchParams({routerId,elementId:element});
    return {url,data:await (await fetch(url)).json()};
  },{routerId,element});
  assert.equal(await input.inputValue(),'');
  await input.fill('{"broken":}');
  await save.press('Enter');
  await page.getByRole('alert').getByText(/JSON/).waitFor();
  const first = await snapshot();
  assert.equal(first.data.mode,'variables');
  const competitor = await page.evaluate(async ({url,data}) => {
    const session=await (await fetch('/ui/v1/session')).json();
    const res=await fetch(url,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':session.csrf},
      body:JSON.stringify({templateId:data.templateId,acceptedVersion:data.acceptedVersion,activeGeneration:data.activeGeneration,
        draftGeneration:data.draftGeneration,action:'replace',source:'"competitor"'})});
    return res.status;
  },first);
  assert.equal(competitor,200);
  const exact = '{\r\n"literal":"$' + '{label}",\n"enabled":false\r\n}\n';
  await input.evaluate((area,value)=>{
    area.setSelectionRange(0,area.value.length);
    const clipboardData=new DataTransfer();
    clipboardData.setData('text/plain',value);
    area.dispatchEvent(new ClipboardEvent('paste',{clipboardData,bubbles:true,cancelable:true}));
  },exact);
  await save.press('Enter');
  await page.getByText('Настройки изменились. Ваш JSON остался в редакторе.',{exact:true}).waitFor();
  await page.getByRole('button',{name:'Перечитать настройки'}).press('Enter');
  await save.waitFor();
  assert.equal(await input.inputValue(),exact.replaceAll('\r\n','\n'));
  resetSession();
  await page.evaluate(()=>window.dispatchEvent(new Event('focus')));
  await page.getByRole('heading',{name:'Сессия истекла'}).waitFor();
  const auth=page.getByLabel('Пароль администратора');
  await auth.fill(password);
  await auth.press('Enter');
  await input.waitFor();
  assert.equal(await input.inputValue(),exact.replaceAll('\r\n','\n'));
  assert.equal((await snapshot()).data.source,'"competitor"');
  await save.press('Enter');
  await success.waitFor();
  assert.equal((await snapshot()).data.source,exact);
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth),320);
  if(process.env.XKEEN_BROWSER_SCREENSHOT) {
    await page.evaluate(()=>window.scrollTo(0,0));
    await page.screenshot({path:process.env.XKEEN_BROWSER_SCREENSHOT+'-replacement.png'});
  }
  const returnButton=page.getByRole('button',{name:'Вернуть переменные',exact:true});
  await returnButton.press('Enter');
  await page.getByRole('button',{name:'Отмена',exact:true}).press('Enter');
  assert.equal((await snapshot()).data.source,exact);
  await returnButton.press('Enter');
  await returnButton.press('Enter');
  await success.waitFor();
  const returned=(await snapshot()).data;
  assert.equal(returned.mode,'variables');
  assert.equal(returned.source,undefined);
  await page.getByRole('link',{name:'К переменным'}).press('Enter');
  await variableForm.waitFor();
  assert.equal(await selectedLabel.getByLabel('JSON').inputValue(),'"example-direct"');
  await modeLink.press('Enter');
  await input.fill('null');
  await save.press('Enter');
  await success.waitFor();
  assert.equal((await snapshot()).data.source,'null');
  await page.getByRole('button',{name:'Сбросить переопределение',exact:true}).press('Enter');
  await page.getByRole('button',{name:'Сбросить',exact:true}).press('Enter');
  await success.waitFor();
  assert.equal((await snapshot()).data.hasOverride,false);
  await page.getByRole('link',{name:'К переменным'}).press('Enter');
  await variableForm.waitFor();
  assert.equal(await selectedLabel.getByRole('checkbox',{name:'Использовать шаблон'}).isChecked(),true);
}
