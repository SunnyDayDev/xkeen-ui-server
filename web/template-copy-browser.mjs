import assert from 'node:assert/strict';
import { DatabaseSync } from 'node:sqlite';

export async function checkTemplateCopy(page,id,database) {
  const target=' copy / # branch ';
  const path='/ui/v1/templates/item?'+new URLSearchParams({templateId:id});
  const read=async pathname=>page.evaluate(async pathname=>(await fetch(pathname)).json(),pathname);
  await page.getByRole('button',{name:'Копировать шаблон'}).click();
  await page.getByRole('heading',{name:'Копирование шаблона'}).waitFor();
  const field=page.getByLabel('Идентификатор копии');
  await field.fill(target);
  const db=new DatabaseSync(database);
  try {
    db.prepare('UPDATE templates SET version=version+1 WHERE id=?').run(id);
  } finally { db.close(); }
  await page.getByRole('button',{name:'Создать копию'}).click();
  await page.getByText('Исходный шаблон изменился. Перечитайте текущую версию.',{exact:true}).waitFor();
  await page.getByRole('button',{name:'Перечитать'}).click();
  await page.getByRole('button',{name:'Создать копию'}).waitFor();
  assert.equal(await field.inputValue(),target);
  await page.setViewportSize({width:360,height:740});
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth),360);
  await page.setViewportSize({width:1080,height:660});
  await page.evaluate(()=>{
    const next=window.fetch;
    window.fetch=async (target,options)=>{
      const response=await next(target,options);
      if (String(target).startsWith('/ui/v1/templates/copy?') && response.status===201) {
        window.fetch=next;
        throw new Error('synthetic receipt transport failure');
      }
      return response;
    };
  });
  const expected=await read(path);
  await page.getByRole('button',{name:'Создать копию'}).click();
  await page.getByText('Не удалось подтвердить создание копии.',{exact:true}).waitFor();
  await page.getByRole('button',{name:'Проверить список'}).click();
  await page.getByText('Шаблон с этим ID найден. Источник записи не подтверждён.',{exact:true}).waitFor();
  await page.getByRole('link',{name:'Открыть шаблон'}).click();
  await page.getByRole('heading',{name:'Сырой JSON элемента'}).waitFor();
  const copyPath='/ui/v1/templates/item?'+new URLSearchParams({templateId:target});
  const copy=await read(copyPath);
  assert.equal(copy.id,target);
  assert.equal(copy.version,'1');
  assert.deepEqual(copy.sections,expected.sections);
  assert.deepEqual(copy.selected,expected.selected);
  await page.getByLabel('JSON',{exact:true}).fill('{"tag":"copied-only.example"}');
  const saved=page.waitForResponse(response=>response.url().includes('/ui/v1/templates/item?')&&
    response.request().method()==='POST'&&response.status()===200);
  await page.getByRole('button',{name:'Сохранить черновик'}).click();
  await saved;
  assert.equal((await read(path)).selected.source,expected.selected.source);

  await page.getByRole('button',{name:'Копировать шаблон'}).click();
  await field.fill('held-copy-example');
  await page.evaluate(()=>{
    const next=window.fetch;
    window.fetch=async (target,options)=>{
      const response=await next(target,options);
      if (String(target).startsWith('/ui/v1/templates/copy?') && response.status===201) {
        window.fetch=next;
        await new Promise(resolve=>{window.releaseCopyReceipt=resolve;});
      }
      return response;
    };
  });
  await page.getByRole('button',{name:'Создать копию'}).click();
  await page.waitForFunction(()=>typeof window.releaseCopyReceipt==='function');
  await page.getByRole('link',{name:'Обзор'}).click();
  await page.evaluate(()=>{window.releaseCopyReceipt();delete window.releaseCopyReceipt;});
  await page.getByRole('heading',{name:'Обзор'}).waitFor();
  assert.equal(await page.getByText('Копия сохранена:',{exact:false}).count(),0);
}
