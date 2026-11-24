<script lang="ts">
  import { onMount } from 'svelte';
  let {id, csrf, signedOut, refreshSession}: {id:string; csrf:string; signedOut:()=>void; refreshSession:()=>void}=$props();
  type Snapshot={name:string; templateId:string; acceptedVersion:string; activeGeneration:string; draftGeneration:string; elementId:string; source:string; variables:{Name:string; Default?:string|null}[]; values?:Record<string,string>; replacement?:string|null; file?:string; issue?:{Code:string; Line:number}; hasOverride:boolean; bypassUnavailable?:boolean};
  let snapshot=$state<Snapshot>();
  let values=$state<Record<string,string>>({});
  let text=$state('');
  let literal=$state(false);
  let confirm=$state('');
  let message=$state('');
  let busy=$state(false);
  let conflict=$state(false);
  const controller=new AbortController();
  const url=$derived('/ui/v1/routers/exclusions?'+new URLSearchParams({routerId:id}));
  async function request(action=''){
    if(busy || (action && conflict)) return;
    busy=true;
    try {
      const body=snapshot && {templateId:snapshot.templateId,elementId:snapshot.elementId,acceptedVersion:snapshot.acceptedVersion,activeGeneration:snapshot.activeGeneration,draftGeneration:snapshot.draftGeneration,action,...(action==='values'?{values}:action==='replace'?{text}:{})};
      const res=await fetch(url,{credentials:'same-origin',redirect:'error',cache:'no-store',signal:controller.signal,...(action?{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:JSON.stringify(body)}:{})});
      if(controller.signal.aborted)return;
      if(res.status===401){signedOut();return}
      if(res.status===403){refreshSession();return}
      if(res.status===409){conflict=true;message='Настройки изменились. Перечитайте актуальные данные.';return}
      if(res.status===404){snapshot=undefined;values={};text='';message='Роутер или общий список недоступен.';return}
      if(!res.ok)throw Error();
      const result:Snapshot=await res.json();
      if(controller.signal.aborted)return;
      snapshot=result; values={...result.values};literal=typeof result.replacement==='string';text=result.replacement??result.source;
      conflict=false;confirm='';message=action?'Черновик сохранён.':'';
    } catch {if(!controller.signal.aborted)message='Не удалось получить или сохранить настройки.'}
    finally{busy=false}
  }
  onMount(()=>{void request();return ()=>controller.abort()});
</script>
<section class="overview-card">
  <h1>Персональные исключения XKeen</h1>
  {#if snapshot}
    <p>{snapshot.name} · {snapshot.templateId}</p>
    <fieldset disabled={busy || conflict}>
      {#if literal}
        <label>Свой список<textarea bind:value={text} rows="7"></textarea></label>
        <button onclick={()=>request('replace')}>Сохранить список</button>
        {#if snapshot.replacement!=null}<button class="secondary" onclick={()=>confirm='return'}>Вернуться к значениям</button>
        {:else}<button class="secondary" onclick={()=>literal=false}>Отмена</button>{/if}
      {:else}
        <pre>{snapshot.source}</pre>
        {#each snapshot.variables??[] as variable}
          <label><input type="checkbox" checked={Object.hasOwn(values,variable.Name)} onchange={e=>{if(e.currentTarget.checked) values[variable.Name]=variable.Default??'';else delete values[variable.Name]}}>Своё значение {variable.Name}</label>
          {#if Object.hasOwn(values,variable.Name)}<label>{variable.Name}<input bind:value={values[variable.Name]} /></label>{/if}
        {/each}
        <button onclick={()=>request('values')}>Сохранить значения</button>
        <button class="secondary" onclick={()=>{literal=true;text=snapshot?.file??''}}>Заменить список</button>
      {/if}
      {#if snapshot.hasOverride}<button class="secondary" onclick={()=>confirm='reset'}>Сбросить настройки</button>{/if}
      {#if confirm}
        <p>{confirm==='reset'?'Удалить собственный список и персональные значения?':'Удалить собственный список и вернуть сохранённые значения?'}</p>
        <button onclick={()=>request(confirm)}>Подтвердить</button><button class="secondary" onclick={()=>confirm=''}>Отмена</button>
      {/if}
    </fieldset>
    {#if snapshot.issue}<p role="alert">Ошибка списка {snapshot.issue.Code} (строка {snapshot.issue.Line}).</p>
    {:else}<h2>Сохранённый ip_exclude.lst</h2><pre>{snapshot.file}</pre>{/if}
    {#if snapshot.bypassUnavailable}<p>Публикация заблокирована: обход для указанных адресов обеспечить нельзя.</p>{/if}
  {/if}
  {#if message}<p role="status">{message}</p>{/if}
  {#if conflict || (!snapshot && !busy)}<button onclick={()=>request()}>Перечитать</button>{/if}
  <a href={'/ui/?'+new URLSearchParams({routerId:id})}>К роутеру</a>
</section>
<style>fieldset{display:grid;gap:.7rem;border:0;padding:0;min-width:0}textarea{width:100%;box-sizing:border-box}pre{white-space:pre-wrap;overflow-wrap:anywhere}</style>
