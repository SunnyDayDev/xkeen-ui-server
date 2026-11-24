import assert from 'node:assert/strict';

export async function checkRouterName(page,{origin,enrolledRouter,resetSession,password}) {
  const resource='/ui/v1/routers/name?'+new URLSearchParams({routerId:enrolledRouter});
  const viewport=page.viewportSize();
  const original='Филиал Восток';
  const actions=[];
  const log = request => {
    if (new URL(request.url()).pathname==='/ui/v1/routers/name' && request.method()==='POST') actions.push(request);
  };
  page.on('request',log);
  await page.setViewportSize({width:320,height:740});
  try {
    await page.goto(origin+'/ui/');
    const list=page.getByRole('region',{name:'Подключённые роутеры'});
    const row=name=>list.getByRole('link',{name,exact:true});
    await row(original).press('Enter');
    const drawer=page.getByRole('complementary',{name:'Сведения о роутере'});
    const edit=drawer.getByRole('button',{name:'Изменить имя',exact:true});
    const save=drawer.getByRole('button',{name:'Сохранить имя',exact:true});
    const input=drawer.getByRole('textbox',{name:'Отображаемое имя'});
    const read=drawer.getByRole('button',{name:'Проверить текущее имя',exact:true});
    await edit.press('Enter');
    await input.waitFor();
    if(process.env.XKEEN_BROWSER_SCREENSHOT) await page.screenshot({path:process.env.XKEEN_BROWSER_SCREENSHOT+'-router-name.png'});
    assert.equal(await input.evaluate(node=>node===document.activeElement&&node.matches(':focus-visible')),true);
    assert.equal(await input.inputValue(),original);
    await drawer.getByRole('button',{name:'Отмена',exact:true}).press('Enter');
    await edit.waitFor();
    assert.equal(await edit.evaluate(node=>node===document.activeElement),true);
    assert.equal(actions.length,0);

    await edit.press('Enter');
    await input.fill('   ');
    await input.press('Tab');
    assert.equal(await save.evaluate(node=>node===document.activeElement),true);
    await page.keyboard.press('Enter');
    await page.getByText('Введите допустимое отображаемое имя.',{exact:true}).waitFor();
    assert.equal(await input.inputValue(),'   ');

    const rival=await page.evaluate(async path => {
      const session=await (await fetch('/ui/v1/session')).json();
      const {activeGeneration,version}=await (await fetch(path)).json();
      const saved=await fetch(path,{method:'POST',headers:{'X-CSRF-Token':session.csrf,'Content-Type':'application/json'},
        body:JSON.stringify({activeGeneration,version,name:'Имя из другой вкладки'})});
      return saved.status;
    },resource);
    assert.equal(rival,200);
    const exact='  Филиал Восток · Новое имя  ';
    await input.fill(exact);
    await save.press('Enter');
    await page.getByText('Имя изменилось. Введённый текст сохранён.',{exact:true}).waitFor();
    assert.equal(await input.inputValue(),exact);
    await read.press('Enter');
    await page.getByText('Текущее имя: Имя из другой вкладки',{exact:true}).waitFor();
    assert.equal(await input.inputValue(),exact);
    await save.press('Enter');
    const changed=row(exact.trim());
    await changed.waitFor();
    assert.equal(await changed.locator('bdi').textContent(),exact);
    await drawer.getByRole('heading',{name:exact.trim(),exact:true}).waitFor();
    assert.equal(new URL(page.url()).searchParams.get('routerId'),enrolledRouter);

    await edit.press('Enter');
    await input.fill(original);
    await page.evaluate(path => {
      const original=window.fetch;
      window.fetch=async (target,options)=>{
        const result=await original(target,options);
        if(String(target)===path&&options?.method==='POST'&&result.status===200) {
          window.fetch=original;
          throw new Error('synthetic response loss after rename');
        }
        return result;
      };
    },resource);
    await save.press('Enter');
    await page.getByText('Не удалось подтвердить сохранение. Проверьте текущее имя.',{exact:true}).waitFor();
    const beforeRead=actions.length;
    await read.press('Enter');
    await page.getByText('Текущее имя: '+original,{exact:true}).waitFor();
    assert.equal(actions.length,beforeRead);
    assert.equal(await input.inputValue(),original);
    await drawer.getByRole('button',{name:'Отмена',exact:true}).press('Enter');
    await row(original).waitFor();
    await edit.press('Enter');
    const interrupted='Имя во время истечения сессии';
    await input.fill(interrupted);
    await page.evaluate(path => {
      const original=window.fetch;
      window.fetch=async (target,options)=>{
        const result=await original(target,options);
        if(String(target)===path&&options?.method==='POST'&&result.status===200) {
          window.fetch=original;
          await new Promise(resolve=>{window.releaseNameReply=resolve;});
        }
        return result;
      };
    },resource);
    await save.press('Enter');
    await page.waitForFunction(()=>typeof window.releaseNameReply==='function');
    resetSession();
    await page.evaluate(()=>window.dispatchEvent(new Event('focus')));
    await page.getByRole('heading',{name:'Сессия истекла'}).waitFor();
    await page.evaluate(()=>window.releaseNameReply());
    const login=page.getByLabel('Пароль администратора');
    await login.fill(password);
    await login.press('Enter');
    await read.waitFor();
    assert.equal(await input.inputValue(),interrupted);
    const beforeManual=actions.length;
    await read.press('Enter');
    await page.getByText('Текущее имя: '+interrupted,{exact:true}).waitFor();
    assert.equal(actions.length,beforeManual);
    await input.fill(original);
    await save.press('Enter');
    await row(original).waitFor();
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth),320);
    await page.getByRole('link',{name:'Шаблоны',exact:true}).press('Enter');
  } finally {
    page.off('request',log);
    await page.setViewportSize(viewport);
  }
}
