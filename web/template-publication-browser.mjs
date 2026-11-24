import assert from 'node:assert/strict';
import { DatabaseSync } from 'node:sqlite';

export async function checkTemplatePublication(page, {origin, database, resetSession, password}) {
  const templateId='f18 / example';
  const query=new URLSearchParams({templateId});
  const editor='/ui/templates/item?'+query;
  const item='/ui/v1/templates/item?'+query;
  const publication='/ui/v1/templates/publication?'+query;
  async function call(path, body, status=200) {
    const message = await page.evaluate(async ({path,body}) => {
      const session=(await (await fetch('/ui/v1/session')).json()).csrf;
      const response=await fetch(path, body===undefined ? {} : {
        method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':session},body:JSON.stringify(body),
      });
      return {status:response.status,received:await response.json()};
    }, {path,body});
    assert.equal(message.status,status,JSON.stringify(message.received));
    return message.received;
  }

  await call('/ui/v1/templates',{id:templateId},201);
  const initial='{"protocol":"freedom","tag":"shared-f18.example"}';
  const first=await call(item,{version:'1',section:'outbounds',source:initial});
  const elementId=first.selected?.id ?? first.elementId;
  assert.ok(elementId);
  async function attach(name) {
    const source=await call('/ui/v1/routers/prepare?'+query);
    const prepared=await call('/ui/v1/routers/prepare',{
      routerId:source.routerId,name,templateId,source:source.source,version:source.version,values:[],
    },201);
    return prepared.routerId;
  }
  const online=await attach('F18 Восток');
  const offline=await attach('F18 Север');
  const db=new DatabaseSync(database);
  try {
    const mark = db.prepare('INSERT INTO agent_contacts(router_id,contact_ns,report,applied,uncertain) VALUES (?, ?, NULL, 0, 0)');
    mark.run(online,BigInt(Date.now())*1000000n);
    mark.run(offline,(BigInt(Date.now())-181000n)*1000000n);
    // A hidden row remains in the current C07 membership.
    db.prepare('INSERT INTO hidden_router_visibility(router_id) VALUES (?)').run(offline);
  } finally { db.close(); }
  const revised='{"protocol":"blackhole","tag":"shared-f18.example"}';
  const revisedAgain='{"protocol":"blackhole","tag":"shared-f18.example","fixturePrecision":9007199254740993}';
  await call(item,{version:'2',elementId,source:revised});
  await page.goto(origin+editor);
  await page.getByRole('button',{name:'Просмотреть последствия публикации',exact:true}).press('Enter');
  const preview=page.getByRole('heading',{name:'Последствия публикации',exact:true});
  await preview.waitFor();
  await page.getByText('F18 Восток',{exact:true}).waitFor();
  const off=page.getByRole('listitem').filter({hasText:'F18 Север'});
  await off.getByText('Нет связи',{exact:true}).waitFor();
  await off.getByText('04_outbounds.json',{exact:true}).waitFor();
  await page.setViewportSize({width:320,height:740});
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth),320);
  const inspect=async (accepted,desired) => {
    const current=await call(publication);
    assert.equal(current.acceptedVersion,accepted);
    assert.deepEqual(new Map(current.members.map(item=>[item.routerId,item.desiredRevision])),new Map([[online,desired],[offline,desired]]));
    assert.ok(current.members.every(member=>member.appliedRevision===undefined));
  };
  await inspect('2','1');
  const proceed=page.getByRole('button',{name:'Перейти к подтверждению',exact:true});
  const publish=page.getByRole('button',{name:'Подтвердить публикацию',exact:true});
  await proceed.press('Enter');
  await publish.waitFor();
  await inspect('2','1');
  await call(item,{version:'3',elementId,source:revisedAgain});
  await publish.press('Enter');
  await page.getByText('Настройки или состав изменились. Подготовьте новый предпросмотр.',{exact:true}).waitFor();
  await inspect('2','1');

  await page.getByRole('button',{name:'Подготовить снова',exact:true}).press('Enter');
  await proceed.press('Enter');
  await page.evaluate(() => {
    const next=window.fetch;
    window.fetch=async (target,options)=>{
      const response=await next(target,options);
      if(String(target).startsWith('/ui/v1/templates/publication?') && options?.body?.includes('"action":"confirm"') && response.status===200) {
        window.fetch=next;
        throw new Error('synthetic response loss after F18 commit');
      }
      return response;
    };
  });
  await publish.press('Enter');
  await page.getByText('Не удалось подтвердить результат.',{exact:true}).waitFor();
  await inspect('4','2');
  await page.getByRole('button',{name:'Проверить состояние',exact:true}).press('Enter');
  await page.getByText('Текущее принятое состояние · версия 4',{exact:true}).waitFor();
  assert.equal(await publish.count(),0);

  await call(item,{version:'4',elementId,source:'{"protocol":"not-a-supported-protocol","tag":"shared-f18.example"}'});
  await page.goto(origin+editor);
  const review = page.getByRole('button',{name:'Просмотреть последствия публикации',exact:true});
  await review.press('Enter');
  await page.getByText('Xray отклонил каталог одного из роутеров. Публикация недоступна.',{exact:true}).waitFor();
  assert.equal(await publish.count(),0);
  await inspect('4','2');

  await call(item,{version:'5',elementId,source:'  '+revisedAgain});
  await page.getByRole('button',{name:'Подготовить снова',exact:true}).press('Enter');
  await proceed.waitFor();
  await page.getByText('Файлы действующего каталога совпадают.',{exact:true}).first().waitFor();
  const afterExpiry = [];
  const track = request => {
    if(request.method()==='POST' && request.url().includes('/ui/v1/templates/publication?')) afterExpiry.push(request.url());
  };
  page.on('request',track);
  try {
    resetSession();
    await page.evaluate(()=>window.dispatchEvent(new Event('focus')));
    await page.getByRole('heading',{name:'Сессия истекла'}).waitFor();
    const login=page.getByLabel('Пароль администратора');
    await login.fill(password);
    await login.press('Enter');
    const prepare=page.getByRole('button',{name:'Подготовить предпросмотр',exact:true});
    await prepare.waitFor();
    assert.equal(afterExpiry.length,0);
    assert.equal(await publish.count(),0);
    await prepare.press('Enter');
    await proceed.press('Enter');
    await publish.press('Enter');
    await page.getByText('Снимок выпуска принят · версия 6',{exact:true}).waitFor();
    assert.equal(afterExpiry.length,2);
  } finally { page.off('request',track); }
  await inspect('6','2');

  await page.setViewportSize({width:1080,height:660});
  await page.getByRole('link',{name:'К шаблону',exact:true}).press('Enter');
  await page.getByRole('heading',{name:'Сырой JSON элемента',exact:true}).waitFor();
}
