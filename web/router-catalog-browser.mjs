import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

export async function checkRouterCatalog(page, routerId, resetSession, password) {
  const panel = page.getByRole('complementary', {name:'Сведения о роутере'});
  await panel.getByRole('link',{name:'Файлы',exact:true}).press('Enter');
  await page.getByRole('heading',{name:'Итоговые файлы',exact:true}).waitFor();
  const picker = page.getByLabel('Файл каталога');
  const current = page.getByRole('button',{name:'Действующие файлы',exact:true});
  const personal = page.getByRole('button',{name:'Собрать предпросмотр',exact:true});
  const download = page.getByRole('button',{name:'Скачать файл',exact:true});
  const responses = [];
  const track = request => {
    if (new URL(request.url()).pathname === '/ui/v1/routers/catalog') responses.push(request.url());
  };
  page.on('request',track);
  try {
    assert.equal(responses.length,0);
    const lookup = page.waitForResponse(response => response.url().includes('/ui/v1/routers/catalog?') && response.url().includes('source=desired'));
    await current.press('Enter');
    const exact = await (await lookup).json();
    assert.equal(exact.routerId,routerId);
    assert.equal(exact.desiredRevision,'1');
    const file = exact.files.find(file => file.name==='04_outbounds.json');
    assert.ok(file);
    const original = Buffer.from(file.content,'base64');
    assert.ok(original.includes('9007199254740993'));
    await picker.selectOption(file.name);
    const visible = await page.getByLabel('Содержимое файла').textContent();
    assert.equal(visible,new TextDecoder('utf-8',{ignoreBOM:true}).decode(original));
    const received = page.waitForEvent('download');
    await download.press('Enter');
    const saved = await received;
    assert.equal(saved.suggestedFilename(),file.name);
    assert.deepEqual(await readFile(await saved.path()),original);
    assert.equal(responses.length,1);
    await page.setViewportSize({width:320,height:740});
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth),320);

    const draftResponse = page.waitForResponse(response => response.url().includes('/ui/v1/routers/catalog?') && response.url().includes('source=personal'));
    await personal.press('Enter');
    const draft = await (await draftResponse).json();
    assert.equal(draft.source,'personal');
    await page.getByText('Предпросмотр не проверялся Xray и не меняет желаемую ревизию.',{exact:true}).waitFor();
    assert.equal(responses.length,2);

    await page.evaluate(() => {
      const original=window.fetch;
      window.fetch=async (target,options)=>{
        const response=await original(target,options);
        if(String(target).startsWith('/ui/v1/routers/catalog?') && response.status===200) {
          window.fetch=original;
          await new Promise(resolve=>{window.releaseCatalog=resolve;});
        }
        return response;
      };
    });
    await personal.press('Enter');
    await page.waitForFunction(()=>typeof window.releaseCatalog==='function');
    assert.equal(await download.count(),0);
    resetSession();
    await page.evaluate(()=>window.dispatchEvent(new Event('focus')));
    await page.getByRole('heading',{name:'Сессия истекла'}).waitFor();
    await page.evaluate(()=>{window.releaseCatalog();delete window.releaseCatalog;});
    const login = page.getByLabel('Пароль администратора');
    await login.fill(password);
    await login.press('Enter');
    await personal.waitFor();
    assert.equal(await download.count(),0);
    assert.equal(responses.length,3);
  } finally { page.off('request',track); }
  await page.setViewportSize({width:1080,height:660});
  await page.getByRole('link',{name:'К роутеру'}).press('Enter');
  await panel.getByText(routerId,{exact:true}).waitFor();
}
