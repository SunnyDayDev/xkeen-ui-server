import assert from 'node:assert/strict';
import { checkRouterReplacement } from './router-replacement-browser.mjs';

export async function checkRouterVariables(page, routerId, resetSession, password) {
  const panel = page.getByRole('complementary',{name:'Сведения о роутере'});
  await panel.getByRole('link',{name:'Переменные'}).click();
  await page.getByRole('heading',{name:'Персональные переменные'}).waitFor();
  const element = '550e8400-e29b-41d4-a716-446655440000';
  await page.getByLabel('Секция Xray').selectOption('outbounds');
  await page.getByLabel('Элемент',{exact:true}).selectOption(element);
  const number = page.getByRole('group',{name:'timeoutSeconds · number'});
  const inherited = number.getByRole('checkbox',{name:'Использовать шаблон'});
  const input = number.getByLabel('JSON');
  await inherited.waitFor();
  assert.equal(await inherited.isChecked(),false);
  assert.equal(await input.inputValue(),'9007199254740993');
  const save = page.getByRole('button',{name:'Сохранить черновик'});
  const snapshot = () => page.evaluate(async ({routerId,element}) => {
    const url='/ui/v1/routers/variables?'+new URLSearchParams({routerId,elementId:element});
    return {url,data:await (await fetch(url)).json()};
  },{routerId,element});
  await inherited.check();
  await save.click();
  const success = page.getByText('Черновик сохранён. Действующая конфигурация не изменилась.',{exact:true});
  await success.waitFor();
  const initial = await snapshot();
  const inheritedBefore = initial.data.selection.definitions.find(row=>row.name==='timeoutSeconds');
  assert.equal(inheritedBefore.defaultSource,'5');
  assert.equal(inheritedBefore.personalSource,undefined);
  await inherited.uncheck();
  await input.fill('not a literal');
  await save.click();
  await page.getByRole('alert').getByText(/JSON/).waitFor();
  assert.equal((await snapshot()).data.draftGeneration,initial.data.draftGeneration);
  await input.fill('null');
  await save.click();
  await page.getByText('Черновик сохранён. Действующая конфигурация не изменилась.',{exact:true}).waitFor();
  await number.getByText('JSON null недопустим при сборке.',{exact:true}).waitFor();
  const candidate = await snapshot();
  const stale = await page.evaluate(async ({url,data}) => {
    const session=await (await fetch('/ui/v1/session')).json();
    const res=await fetch(url,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':session.csrf},
      body:JSON.stringify({templateId:data.templateId,acceptedVersion:data.acceptedVersion,activeGeneration:data.activeGeneration,
        draftGeneration:data.draftGeneration,values:[{name:'timeoutSeconds',source:'1'}]})});
    return res.status;
  },candidate);
  assert.equal(stale,200);
  const exact = ' 9007199254740993\r\n';
  await input.fill(' ');
  await input.evaluate((area,value)=>{
    area.setSelectionRange(0,area.value.length);
    const clipboardData=new DataTransfer();
    clipboardData.setData('text/plain',value);
    area.dispatchEvent(new ClipboardEvent('paste',{clipboardData,bubbles:true,cancelable:true}));
  },exact);
  await save.click();
  await page.getByText('Настройки изменились. Ваши значения остались в форме.',{exact:true}).waitFor();
  assert.equal(await input.inputValue(),exact.replaceAll('\r\n','\n'));
  await page.getByRole('button',{name:'Перечитать настройки'}).click();
  await save.waitFor();
  resetSession();
  await page.evaluate(()=>window.dispatchEvent(new Event('focus')));
  await page.getByRole('heading',{name:'Сессия истекла'}).waitFor();
  const field=page.getByLabel('Пароль администратора');
  await field.fill(password);
  await field.press('Enter');
  await input.waitFor();
  assert.equal(await input.inputValue(),exact.replaceAll('\r\n','\n'));
  assert.equal((await snapshot()).data.selection.definitions.find(row=>row.name==='timeoutSeconds').personalSource,'1');
  await save.click();
  await page.getByText('Черновик сохранён. Действующая конфигурация не изменилась.',{exact:true}).waitFor();
  assert.equal((await snapshot()).data.selection.definitions.find(row=>row.name==='timeoutSeconds').personalSource,exact);
  if(process.env.XKEEN_BROWSER_SCREENSHOT) {
    await page.evaluate(()=>window.scrollTo(0,0));
    await page.screenshot({path:process.env.XKEEN_BROWSER_SCREENSHOT+'-router-variables-wide.png'});
  }
  await page.setViewportSize({width:390,height:730});
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth),390);
  if(process.env.XKEEN_BROWSER_SCREENSHOT) {
    await page.evaluate(()=>window.scrollTo(0,0));
    await page.screenshot({path:process.env.XKEEN_BROWSER_SCREENSHOT+'-router-variables.png'});
  }
  await page.setViewportSize({width:1080,height:660});
  await inherited.check();
  await save.click();
  await page.getByText('Черновик сохранён. Действующая конфигурация не изменилась.',{exact:true}).waitFor();
  assert.equal((await snapshot()).data.selection.definitions.find(row=>row.name==='timeoutSeconds').personalSource,undefined);
  await checkRouterReplacement(page,routerId,element,resetSession,password);
  await page.getByRole('link',{name:'К роутеру'}).click();
  await panel.getByText(routerId,{exact:true}).waitFor();
}
