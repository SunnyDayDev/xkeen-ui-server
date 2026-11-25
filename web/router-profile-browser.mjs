import assert from 'node:assert/strict';

export async function checkRouterProfileSwitch(page,{origin,resetSession,password}) {
  const home='f18 / example';
  const office='Z-Office + example';
  const query=new URLSearchParams({templateId:office});
  async function call(path, body, status=200) {
    const reply=await page.evaluate(async ({path,body})=>{
      const session=(await (await fetch('/ui/v1/session')).json()).csrf;
      const response=await fetch(path,body===undefined?{}:{
        method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':session},body:JSON.stringify(body),
      });
      return {status:response.status,body:await response.json()};
    },{path,body});
    assert.equal(reply.status,status,JSON.stringify(reply.body));
    return reply.body;
  }
  await call('/ui/v1/templates',{id:office},201);
  await call('/ui/v1/templates/item?'+query,{version:'1',section:'outbounds',source:'{"protocol":"blackhole","tag":"${officeTag}"}'});
  const publication='/ui/v1/templates/publication?'+query;
  const officePlan=await call(publication,{action:'preview',version:'2'});
  await call(publication,{action:'confirm',receipt:officePlan.receipt});
  const list=await call('/ui/v1/routers');
  const current=list.routers.find(item=>item.name==='F18 Восток');
  assert.equal(current?.templateId,home);
  const scope=new URLSearchParams({routerId:current.routerId});
  const data=async ()=>(await call('/ui/v1/routers')).routers.find(item=>item.routerId===current.routerId);

  const rules='/ui/v1/routers/rules?'+scope;
  const before=await call(rules);
  const added=await call(rules,{
    templateId:before.templateId,acceptedVersion:before.acceptedVersion,activeGeneration:before.activeGeneration,
    draftGeneration:before.draftGeneration,afterId:'',
    source:'{"type":"field","ruleTag":"saved-local","ip":["192.0.2.0/24"],"outboundTag":"shared-f18.example"}',
  });
  const localRule=added.rules.find(rule=>rule.label==='saved-local');
  assert.ok(localRule);
  const homeQuery=new URLSearchParams({templateId:home});
  const homePublication='/ui/v1/templates/publication?'+homeQuery;
  const publishNewHome=async (version,tag)=>{
    const revised=await call('/ui/v1/templates/item?'+homeQuery,{
      version,appendSection:'rules',
      source:JSON.stringify({type:'field',ruleTag:tag,ip:['198.51.100.0/24'],outboundTag:'shared-f18.example'}),
    });
    const checked=await call(homePublication,{action:'preview',version:revised.version});
    await call(homePublication,{action:'confirm',receipt:checked.receipt});
    return revised.version;
  };

  await page.setViewportSize({width:320,height:740});
  await page.goto(origin+'/ui/?'+scope);
  const drawer=page.getByRole('complementary',{name:'Сведения о роутере'});
  const entry=drawer.getByRole('link',{name:'Сменить профиль',exact:true});
  await entry.press('Enter');
  await page.getByRole('heading',{name:'Смена профиля',exact:true}).waitFor();
  assert.equal(new URL(page.url()).searchParams.get('routerId'),current.routerId);
  const choose=page.getByLabel('Новый профиль');
  await choose.getByRole('option',{name:office,exact:true}).waitFor({state:'attached'});
  await choose.press('z');
  await page.getByText('У этого профиля ещё нет сохранённых настроек.',{exact:true}).waitFor();
  assert.equal(await choose.inputValue(),office);
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth),320);
  const prepare=page.getByRole('button',{name:'Подготовить смену',exact:true});
  const proceed=page.getByRole('button',{name:'Перейти к подтверждению',exact:true});
  const confirm=page.getByRole('button',{name:'Подтвердить смену',exact:true});
  const cancel=page.getByRole('button',{name:'Отмена',exact:true});
  await prepare.press('Enter');
  await page.getByText('Проверьте обязательные значения и совместимость настроек.',{exact:true}).waitFor();
  assert.equal((await data()).templateId,home);
  await page.getByRole('textbox',{name:'officeTag'}).fill('"office.example"');
  await prepare.press('Enter');
  await page.getByText('04_outbounds.json',{exact:true}).waitFor();
  await proceed.press('Enter');
  await page.waitForFunction(()=>document.activeElement?.textContent?.trim()==='Отмена' && document.activeElement.matches(':focus-visible'));
  assert.equal((await data()).templateId,home);
  await cancel.press('Enter');
  await page.waitForFunction(()=>document.activeElement?.textContent?.trim()==='Подготовить смену');
  assert.equal(await confirm.count(),0);
  assert.equal((await data()).templateId,home);
  await prepare.press('Enter');
  await proceed.press('Enter');
  await page.waitForFunction(()=>document.activeElement?.textContent?.trim()==='Отмена' && document.activeElement.matches(':focus-visible'));
  await cancel.press('Shift+Tab');
  await page.waitForFunction(()=>document.activeElement?.textContent?.trim()==='Подтвердить смену');
  await page.keyboard.press('Enter');
  await page.getByText('Подтверждения применения от агента пока нет.',{exact:true}).waitFor();
  assert.equal((await data()).templateId,office);
  assert.ok((await call(rules)).rules.every(rule=>rule.id!==localRule.id));
  const nextVersion=await publishNewHome('7','new-shared-rule');
  const back=page.getByRole('link',{name:'К роутеру',exact:true});
  await back.press('Enter');
  await entry.waitFor();
  await page.waitForFunction(()=>document.activeElement?.textContent?.trim()==='Сменить профиль');
  await entry.press('Enter');
  await choose.selectOption(home);
  await page.getByText('Для этого профиля сохранены персональные настройки и порядок.',{exact:true}).waitFor();
  await prepare.press('Enter');
  await page.getByText('Добавлено общих правил: 1. Исчезло: 0. Относительный порядок локальных правил сохранён.',{exact:true}).waitFor();
  await proceed.press('Enter');
  await publishNewHome(nextVersion,'next-shared-rule');
  await confirm.press('Enter');
  await page.getByText('Состояние изменилось. Введённые значения сохранены.',{exact:true}).waitFor();
  assert.equal((await data()).templateId,office);
  await page.getByRole('button',{name:'Перечитать профиль',exact:true}).press('Enter');
  await prepare.press('Enter');
  await page.getByText('Добавлено общих правил: 2. Исчезло: 0. Относительный порядок локальных правил сохранён.',{exact:true}).waitFor();
  await proceed.press('Enter');
  await confirm.waitFor();
  await page.evaluate(()=>{
    const next=window.fetch;
    window.fetch=async (target,options)=>{
      const response=await next(target,options);
      if(String(target).startsWith('/ui/v1/routers/profile?') && options?.body?.includes('"action":"confirm"') && response.status===200) {
        window.fetch=next;
        throw new Error('synthetic profile response loss after commit');
      }
      return response;
    };
  });
  const commands=[];
  const record=request=>{
    if(request.method()==='POST' && request.url().includes('/ui/v1/routers/profile?')) commands.push(request.postData());
  };
  page.on('request',record);
  try {
    await confirm.press('Enter');
    await page.getByText('Не удалось подтвердить смену. Проверьте текущее назначение.',{exact:true}).waitFor();
    await page.getByRole('button',{name:'Проверить текущий профиль',exact:true}).press('Enter');
    await page.getByText('Текущий профиль: '+home,{exact:true}).waitFor();
    assert.equal((await data()).templateId,home);
    const restored=await call(rules);
    assert.ok(restored.rules.some(rule=>rule.id===localRule.id));
    assert.ok(restored.rules.some(rule=>rule.label==='new-shared-rule'));
    assert.ok(restored.rules.some(rule=>rule.label==='next-shared-rule'));
    assert.equal(commands.length,1);
  } finally { page.off('request',record); }
  await choose.selectOption(office);
  const officeValue=page.getByRole('textbox',{name:'officeTag'});
  await officeValue.waitFor();
  assert.equal(await officeValue.inputValue(),'"office.example"');
  await prepare.press('Enter');
  await proceed.press('Enter');
  await page.evaluate(()=>{
    const next=window.fetch;
    window.fetch=async (target,options)=>{
      const response=await next(target,options);
      if(String(target).startsWith('/ui/v1/routers/profile?') && options?.body?.includes('"action":"confirm"') && response.status===200) {
        window.fetch=next;
        await new Promise(resolve=>{window.releaseProfileReply=resolve;});
      }
      return response;
    };
  });
  page.on('request',record);
  try {
    await confirm.press('Enter');
    await page.waitForFunction(()=>typeof window.releaseProfileReply==='function');
    assert.equal((await data()).templateId,office);
    resetSession();
    await page.evaluate(()=>window.dispatchEvent(new Event('focus')));
    await page.getByRole('heading',{name:'Сессия истекла'}).waitFor();
    await page.evaluate(()=>{window.releaseProfileReply();delete window.releaseProfileReply;});
    const login=page.getByLabel('Пароль администратора',{exact:true});
    await login.fill(password);
    await login.press('Enter');
    const recover=page.getByRole('button',{name:'Проверить текущий профиль',exact:true});
    await recover.waitFor();
    assert.equal(await confirm.count(),0);
    await recover.press('Enter');
    await page.getByText('Текущий профиль: '+office,{exact:true}).waitFor();
    assert.equal(commands.length,2);
  } finally { page.off('request',record); }
  await page.setViewportSize({width:1080,height:660});
  console.log('G03 real browser profile switch, reconciliation, and recovery passed');
}
