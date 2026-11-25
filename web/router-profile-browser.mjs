import assert from 'node:assert/strict';

export async function checkRouterProfileSwitch(page,{origin,enrolledRouter}) {
  await page.goto(origin+'/ui/?'+new URLSearchParams({routerId:enrolledRouter}));
  const drawer=page.getByRole('complementary',{name:'Сведения о роутере'});
  await drawer.getByRole('link',{name:'Сменить профиль',exact:true}).press('Enter');
  await page.getByRole('heading',{name:'Смена профиля',exact:true}).waitFor();
  assert.equal(new URL(page.url()).searchParams.get('routerId'),enrolledRouter);
  await page.getByRole('link',{name:'Шаблоны',exact:true}).press('Enter');
}
