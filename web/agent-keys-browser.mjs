import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { DatabaseSync } from 'node:sqlite';

export async function checkAgentKeys(page,{origin,dir,cert,database,enrolledRouter}) {
  const viewport=page.viewportSize();
  await page.setViewportSize({width:320,height:740});
  await page.getByRole('link',{name:'Обзор'}).press('Enter');
  await page.getByRole('link',{name:'Филиал Восток',exact:true}).press('Enter');
  await page.getByRole('link',{name:'Рабочий ключ'}).press('Enter');
  await page.getByRole('button',{name:'Выдать приглашение для замены'}).press('Enter');
  await page.getByText('Прежний ключ действует до успешной замены.',{exact:true}).waitFor();
  const endpoint='/ui/v1/routers/keys';
  await page.evaluate(endpoint=>{
    const next=window.fetch;
    window.fetch=async (target,options)=>{
      const response=await next(target,options);
      if (String(target).startsWith(endpoint+'?') && response.status===201) {
        window.fetch=next;
        throw new Error('synthetic receipt transport failure');
      }
      return response;
    };
  },endpoint);
  await page.getByRole('button',{name:'Выдать приглашение',exact:true}).press('Enter');
  await page.getByText('Не удалось подтвердить выдачу.',{exact:true}).waitFor();
  await page.getByRole('button',{name:'Проверить состояние'}).press('Enter');
  await page.getByText('Приглашение для рабочего ключа действует.',{exact:true}).waitFor();
  const secret=page.getByRole('textbox',{name:'Одноразовое приглашение'});
  assert.equal(await secret.count(),0);
  const db=new DatabaseSync(database);
  try {
    db.prepare('UPDATE agent_contacts SET contact_ns=? WHERE router_id=?')
      .run((BigInt(Date.now())-181000n)*1000000n,enrolledRouter);
  } finally {db.close();}
  await page.getByText('Связь: нет связи',{exact:true}).waitFor();
  await page.getByRole('button',{name:'Отозвать ключ',exact:true}).press('Enter');
  await page.getByText('До локальной настройки нового ключа агент не сможет синхронизироваться.',{exact:true}).waitFor();
  await page.getByRole('button',{name:'Отозвать доступ',exact:true}).press('Enter');
  await page.getByText('Рабочий ключ отозван.',{exact:true}).waitFor();
  assert.equal(await secret.count(),0);
  assert.equal(await page.getByText('Приглашение для рабочего ключа действует.',{exact:true}).count(),0);
  await page.getByRole('button',{name:'Восстановить подключение',exact:true}).press('Enter');
  await page.getByRole('button',{name:'Выдать приглашение',exact:true}).press('Enter');
  await secret.waitFor();
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth),320);
  const recovery=await secret.inputValue();
  assert.match(recovery,/^i1\./);
  assert.ok(!page.url().includes(recovery));
  const agentDir=join(dir,'agent-recovery');
  mkdirSync(agentDir,{mode:0o700});
  writeFileSync(join(agentDir,'invitation'),recovery+'\n',{mode:0o600});
  const agent=spawn(resolve(process.env.XKEEN_TEST_AGENT_BINARY),
    ['--server',origin,'--expected-router-id',enrolledRouter,'--state-dir',agentDir,'--ca-file',cert],{stdio:['ignore','ignore','pipe']});
  let safe=true;
  agent.stderr.on('data',data=>{if(data.includes(recovery)) safe=false;});
  try {
    await page.getByText('Агент зарегистрирован.',{exact:true}).waitFor({timeout:20000});
    await page.getByText('Связь: на связи',{exact:true}).waitFor({timeout:20000});
    await secret.waitFor({state:'detached'});
    assert.equal(safe,true);
  } finally {
    await page.setViewportSize(viewport);
    if (agent.exitCode===null && agent.signalCode===null) {
      const stopped=new Promise(resolve=>agent.once('exit',resolve));
      agent.kill('SIGTERM');
      await stopped;
    }
  }
}
