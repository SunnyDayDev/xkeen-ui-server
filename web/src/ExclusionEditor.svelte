<script lang="ts">
 import {onMount} from 'svelte';
 import {ExactSource} from './exact-source';
 let {id,active,sessionEpoch,csrf,signedOut,refreshSession}: {id:string;active:boolean;sessionEpoch:number;csrf:string;signedOut:()=>void;refreshSession:()=>void}=$props();
 type Variable={name:string;default?:string};
 type Source={id:string;version:string;text:string;variables:Variable[]};
 const query=()=>new URLSearchParams({templateId:id}).toString();
 let base=$state<Source>();
 let source=new ExactSource('');
 let display=$state('');
 let variables=$state<Variable[]>([]);
 let preview=$state<string>();
 let status=$state('');
 let busy=$state(false);
 let stale=$state(false);
 let alive=true;
 onMount(()=>{void read();return ()=>{alive=false;}});
 async function call(method:string,path:string,body?:unknown){
  const epoch=sessionEpoch;
  const res=await fetch('/ui/v1/templates/xkeen-exclusions'+path+'?'+query(),{
   method,credentials:'same-origin',cache:'no-store',redirect:'error',
   headers:method==='POST'?{'Content-Type':'application/json','X-CSRF-Token':csrf}:{},
   signal:AbortSignal.timeout(15000),body:body===undefined?undefined:JSON.stringify(body)
  });
  if(!alive||!active||epoch!==sessionEpoch)return;
  if(res.status===401){signedOut();return;}
  if(res.status===403){refreshSession();return;}
  const data=await res.json();
  if(!alive||!active||epoch!==sessionEpoch)return;
  return {res,data};
 }
 function select(data:Source){
  base=data;source=new ExactSource(data.text);display=source.display;
  variables=data.variables.map(v=>({...v}));preview=undefined;stale=false;
 }
 async function read(){
  if(busy||!active)return;
  busy=true;
  try{const reply=await call('GET','');if(!reply)return;
   if(!reply.res.ok){status=reply.res.status===404?'Шаблон не найден.':'Не удалось загрузить список.';return;}
   select(reply.data);status=base?.version==='0'?'Настройка ещё не создана.':'';
  }catch{status='Не удалось загрузить список.';}finally{busy=false;}
 }
 function modified(){preview=undefined;status='';}
 async function submit(check:boolean){
  if(!base||!active||stale||busy)return;
  busy=true;
  try{
   const reply=await call('POST',check?'/preview':'',{id:base.id,version:base.version,text:source.value,variables});
   if(!reply)return;
   if(reply.res.status===409){stale=true;status='Настройки изменились. Ваш текст сохранён в форме.';return;}
   if(!reply.res.ok){preview=undefined;status=reply.data.line?'Строка '+reply.data.line+': '+(reply.data.code==='missing_value'?'нужно значение':'неверный адрес или переменная')+'.':'Проверьте имена переменных и значения.';return;}
   if(check){preview=reply.data.file;status='';}else{select(reply.data);status='Список сохранён.';}
  }catch{status='Ответ не получен. Перечитайте настройки перед повтором.';stale=true;}finally{busy=false;}
 }
</script>
<p class="editor-context"><bdi>{id}</bdi></p>
<h1>Исключения XKeen</h1>
{#if base}
<fieldset disabled={!active||busy}>
 <label>Список IP и сетей
  <textarea rows="7" value={display} oninput={(e)=>{source.replaceVisible(e.currentTarget.value);display=source.display;modified();}}></textarea>
 </label>
 <h2>Переменные</h2>
 {#each variables as variable,index}
  <div class="editor-coordinates">
   <label>Имя <input bind:value={variable.name} oninput={modified}/></label>
   <label><input type="checkbox" checked={variable.default!==undefined} onchange={(e)=>{if(e.currentTarget.checked)variable.default='';else delete variable.default;modified();}}/>Значение по умолчанию</label>
   {#if variable.default!==undefined}<label>IP или сеть <input bind:value={variable.default} oninput={modified}/></label>{/if}
   <button class="secondary" onclick={()=>{variables.splice(index,1);modified();}}>Удалить переменную</button>
  </div>
 {/each}
 <button class="secondary" onclick={()=>{variables.push({name:''});modified();}}>Добавить переменную</button>
 <h2>ip_exclude.lst</h2>
 {#if preview!==undefined}<pre>{preview||'Пустой файл'}</pre>{/if}
 <div class="editor-actions">
  {#if stale}<button onclick={read}>Перечитать</button>{:else}
   <button onclick={()=>submit(false)}>{base.version==='0'?'Создать список':'Сохранить'}</button>
   <button class="secondary" onclick={()=>submit(true)}>Проверить список</button>
  {/if}
 </div>
</fieldset>
{:else if !busy}<button onclick={read}>Загрузить список</button>{/if}
{#if status}<p role="status">{status}</p>{/if}
<a href={'/ui/templates/item?'+query()}>К шаблону</a>
