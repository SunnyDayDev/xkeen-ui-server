import assert from 'node:assert/strict';
import {DatabaseSync} from 'node:sqlite';

export async function checkRouterExclusions(parent,origin,database) {
  const db=new DatabaseSync(database);
  const template='branch-east';
  const routers=['north.example','south.example'];
  const ctx=await parent.browser().newContext({storageState:await parent.storageState()});
  const page=await ctx.newPage();
  page.setDefaultTimeout(5000);
  try {
    // Seed accepted snapshots in this browser run's disposable SQLite database.
    db.prepare('UPDATE shared_xkeen_exclusions SET source=?,variables=? WHERE template_id=?')
      .run('${network}',Buffer.from(JSON.stringify([{name:'network',default:'192.0.2.4'}])),template);
    db.prepare('INSERT INTO accepted_templates SELECT id,version,draft FROM templates WHERE id=?').run(template);
    for (const id of routers) {
      const draft={Name:id,TemplateID:template,Groups:null,PersonalOrder:null,DisabledRules:null,DisabledInbounds:null};
      db.prepare('INSERT INTO active_router_publications VALUES (?,?,1,?,NULL)').run(id,template,Buffer.from(JSON.stringify(draft)));
      db.prepare('INSERT INTO desired_router_revisions VALUES (?,1,?,?)').run(id,'xray',Buffer.from('[]'));
      db.prepare('INSERT INTO agent_contacts VALUES (?,?,NULL,0,0)').run(id,BigInt(Date.now())*1000000n);
    }
    async function open(id) {
      await page.goto(origin+'/ui/?'+new URLSearchParams({routerId:id}));
      await page.getByRole('link',{name:'Исключения XKeen',exact:true}).click();
      await page.getByRole('button',{name:'Сохранить значения'}).waitFor();
    }
    const file=()=>page.locator('pre').last().innerText();
    await open(routers[0]);
    await page.getByLabel('Своё значение network').check();
    await page.getByLabel('network',{exact:true}).fill('198.51.100.7');
    await page.getByRole('button',{name:'Сохранить значения'}).click();
    await page.locator('pre').last().getByText('198.51.100.7',{exact:false}).waitFor();
    await page.getByRole('button',{name:'Заменить список'}).click();
    await page.getByLabel('Свой список').fill('');
    await page.getByRole('button',{name:'Сохранить список'}).click();
    await page.getByRole('button',{name:'Вернуться к значениям'}).click();
    await page.getByRole('button',{name:'Подтвердить'}).click();
    await page.getByRole('button',{name:'Сохранить значения'}).waitFor();
    assert.equal((await file()).trim(),'198.51.100.7');
    await open(routers[1]);
    assert.equal((await file()).trim(),'192.0.2.4');
    await open(routers[0]);
    await page.getByRole('button',{name:'Сбросить настройки'}).click();
    await page.getByRole('button',{name:'Подтвердить'}).click();
    await page.locator('pre').last().getByText('192.0.2.4',{exact:false}).waitFor();
    assert.equal(await page.getByLabel('Своё значение network').isChecked(),false);
  } finally {db.close();await ctx.close()}
}
