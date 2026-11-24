import assert from 'node:assert/strict';
import { DatabaseSync } from 'node:sqlite';

export async function checkLocalRule(page, routerId, database) {
  const panel = page.getByRole('complementary',{name:'Сведения о роутере'});
  await panel.getByRole('link',{name:'Маршрутизация',exact:true}).click();
  await page.getByRole('heading',{name:'Маршрутизация роутера'}).waitFor();
  const common='550e8400-e29b-41d4-a716-446655440030';
  const disabled='550e8400-e29b-41d4-a716-446655440031';
  const initial = () => page.evaluate(async routerId => {
    const url='/ui/v1/routers/rules?'+new URLSearchParams({routerId});
    return {url,data:await (await fetch(url)).json()};
  },routerId);
  const first=await initial();
  assert.deepEqual(first.data.rules.map(({id,disabled})=>[id,disabled]),[[common,false],[disabled,true]]);
  await page.getByText('Общее · отключено',{exact:true}).waitFor();
  const add=page.getByRole('button',{name:'Добавить локальное правило',exact:true});
  await add.click();
  const position=page.getByLabel('Место в полном порядке',{exact:true});
  await position.selectOption(disabled);
  const input=page.getByLabel('JSON правила',{exact:true});
  await input.fill('{"broken":}');
  await page.getByRole('button',{name:'Отмена',exact:true}).click();
  assert.equal((await initial()).data.draftGeneration,first.data.draftGeneration);
  await add.click();
  await position.selectOption(disabled);
  await input.fill('{"broken":}');
  const save=page.getByRole('button',{name:'Добавить правило',exact:true});
  await save.click();
  await page.getByRole('alert').getByText(/JSON/).waitFor();
  assert.equal((await initial()).data.draftGeneration,first.data.draftGeneration);
  const competing=await page.evaluate(async ({url,data})=>{
    const session=await (await fetch('/ui/v1/session')).json();
    const res=await fetch(url,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':session.csrf},
      body:JSON.stringify({templateId:data.templateId,acceptedVersion:data.acceptedVersion,activeGeneration:data.activeGeneration,
        draftGeneration:data.draftGeneration,afterId:'',source:'{"ruleTag":"competing-local"}'})});
    return res.status;
  },first);
  assert.equal(competing,200);
  const exact=' {\r\n"ruleTag":"new-local",\n"weight":9007199254740993\r\n}\n';
  await input.evaluate((area,value)=>{
    area.setSelectionRange(0,area.value.length);
    const clipboardData=new DataTransfer();
    clipboardData.setData('text/plain',value);
    area.dispatchEvent(new ClipboardEvent('paste',{clipboardData,bubbles:true,cancelable:true}));
  },exact);
  await save.click();
  await page.getByText('Порядок изменился. Ваш JSON остался в форме.',{exact:true}).waitFor();
  await page.getByRole('button',{name:'Перечитать настройки'}).click();
  await save.waitFor();
  assert.equal(await position.inputValue(),disabled);
  assert.equal(await input.inputValue(),exact.replaceAll('\r\n','\n'));
  await page.setViewportSize({width:390,height:730});
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth),390);
  if(process.env.XKEEN_BROWSER_SCREENSHOT) {
    await page.evaluate(()=>window.scrollTo(0,0));
    await page.screenshot({path:process.env.XKEEN_BROWSER_SCREENSHOT+'-local-rule.png'});
  }
  await page.setViewportSize({width:1080,height:660});
  await save.click();
  await page.getByText('Черновик сохранён. Локальное правило добавлено.',{exact:true}).waitFor();
  const latest=(await initial()).data;
  assert.deepEqual(latest.rules.map(({label,origin})=>[label,origin]),[
    ['competing-local','local'],['common-rule','shared'],['disabled-rule','shared'],['new-local','local'],
  ]);
  assert.match(latest.rules[3].id,/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  const db=new DatabaseSync(database);
  try {
    const stored=db.prepare('SELECT snapshot, order_basis FROM router_settings WHERE id=?').get(routerId);
    const json=JSON.parse(new TextDecoder().decode(stored.snapshot));
    const local=json.Groups.find(group=>group.Name==='rules').Local.find(rule=>rule.ID===latest.rules[3].id);
    assert.deepEqual(Buffer.from(local.Content,'base64'),Buffer.from(exact));
    const orderBasis=JSON.parse(new TextDecoder().decode(stored.order_basis));
    assert.deepEqual(orderBasis.SharedRuleIDs,[common,disabled]);
  } finally { db.close(); }
  await page.reload();
  await page.getByText('new-local',{exact:true}).waitFor();
  await page.getByRole('link',{name:'К роутеру'}).click();
  await panel.getByText(routerId,{exact:true}).waitFor();
}
