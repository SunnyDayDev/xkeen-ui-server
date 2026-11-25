<script module lang="ts">
  export type ProfileValueEdit = {elementId:string; name:string; source:string|null};
  export type ProfileSelection = {target:string; values:ProfileValueEdit[]; uncertain:boolean};
</script>

<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { ExactSource } from './exact-source';

  type SeenRouter = {routerId:string; name:string; templateId:string; desiredRevision:string; appliedRevision?:string};
  type Field = {elementId:string; section:string; name:string; type:string; required:boolean; defaultSource?:string; personalSource?:string; issue?:string};
  type Scope = {activeTemplateId:string; targetTemplateId:string; restoring:boolean; fields:Field[]};
  type Preview = {receipt:string; changed:boolean; restoring:boolean; changes:{name:string; kind:string}[]; order:{state:string; added:number; removed:number}};
  type Result = {templateId:string; desiredRevision:string; changed:boolean};
  let {id, csrf, retained, retain, signedOut, refreshSession, back}: {
    id:string; csrf:string; retained?:ProfileSelection; retain:(selection?:ProfileSelection)=>void;
    signedOut:()=>void; refreshSession:()=>void; back:(event:MouseEvent,path:string)=>void;
  } = $props();

  let router = $state<SeenRouter>();
  let templates = $state<string[]>([]);
  let scope = $state<Scope>();
  let preview = $state<Preview>();
  let result = $state<Result>();
  let inspected = $state(false);
  let confirming = $state(false);
  let stale = $state(false);
  let message = $state('');
  let busy = $state(false);
  let prepareButton = $state<HTMLButtonElement>();
  let selectProfile = $state<HTMLSelectElement>();
  let checkButton = $state<HTMLButtonElement>();
  let proceedButton = $state<HTMLButtonElement>();
  let cancelButton = $state<HTMLButtonElement>();
  let backLink = $state<HTMLAnchorElement>();
  let controller:AbortController|undefined;
  let alive = true;
  const confirmationId = $props.id();

  const overviewURL = () => '/ui/?'+new URLSearchParams({routerId:id});
  function callURL(target?:string) {
    const query=new URLSearchParams({routerId:id});
    if (target) query.set('targetTemplateId',target);
    return '/ui/v1/routers/profile?'+query;
  }

  async function restoreFocus(started:HTMLElement|undefined, target:()=>HTMLElement|undefined) {
    if (!started) return;
    await tick();
    if (alive && (document.activeElement===started || document.activeElement===document.body)) target()?.focus();
  }

  async function operate(run:(signal:AbortSignal)=>Promise<void>) {
    if (busy || !alive) return;
    const current = new AbortController();
    controller = current;
    busy = true;
    try {
      await run(AbortSignal.any([current.signal,AbortSignal.timeout(30000)]));
    } catch {
      if (alive && controller===current) message = 'Не удалось прочитать данные. Проверьте текущее состояние.';
    } finally {
      if (controller===current) {
        controller=undefined;
        busy=false;
      }
    }
  }

  async function fetchJSON(url:string, signal:AbortSignal, body?:unknown) {
    const response=await fetch(url,{
      method:body===undefined?'GET':'POST',cache:'no-store',credentials:'same-origin',redirect:'error',signal,
      headers:body===undefined?{}:{'Content-Type':'application/json','X-CSRF-Token':csrf},
      body:body===undefined?undefined:JSON.stringify(body),
    });
    if (!alive || signal.aborted) return;
    const value=await response.json();
    if (!alive || signal.aborted) return;
    return {status:response.status,value};
  }

  function access(status:number) {
    if (status===401) signedOut();
    if (status===403) refreshSession();
    return status===401 || status===403;
  }

  async function readCurrent(signal:AbortSignal) {
    const reply=await fetchJSON('/ui/v1/routers',signal);
    if (!reply || access(reply.status)) return;
    if (reply.status!==200 || !Array.isArray(reply.value.routers)) throw new Error('router state unavailable');
    router=reply.value.routers.find((candidate:SeenRouter)=>candidate.routerId===id);
    if (!router) message='Роутер недоступен в текущем обзоре.';
    return router;
  }

  async function open() {
    await operate(async signal => {
      if (!await readCurrent(signal)) return;
      const reply=await fetchJSON('/ui/v1/templates',signal);
      if (!reply || access(reply.status)) return;
      if (reply.status!==200 || !Array.isArray(reply.value.templates)) throw new Error('template list unavailable');
      templates=reply.value.templates.map((entry:{id:string})=>entry.id);
    });
  }

  function clearPlan() {
    preview=undefined;
    confirming=false;
    result=undefined;
    inspected=false;
  }

  async function selectTarget(target:string, preserve=false) {
    if (busy) return;
    clearPlan();
    scope=undefined;
    stale=false;
    message='';
    if (!preserve) retain(target?{target,values:[],uncertain:false}:undefined);
    if (!target) return;
    await operate(async signal=>{
      const reply=await fetchJSON(callURL(target),signal);
      if (!reply || access(reply.status)) return;
      if (reply.status===404) {
        message='Целевой профиль не принят или роутер недоступен.';
        return;
      }
      if (reply.status===409) {
        const current=await readCurrent(signal);
        if (!current) return;
        retain();
        message='Активный профиль изменился. Выберите другую цель.';
        return;
      }
      if (reply.status!==200 || reply.value.targetTemplateId!==target || !Array.isArray(reply.value.fields)) throw new Error('profile state unavailable');
      const targetScope=reply.value as Scope;
      if (router) router={...router,templateId:targetScope.activeTemplateId};
      scope=targetScope;
    });
  }

  function fieldValue(field:Field) {
    const stored=retained?.values.find(v=>v.elementId===field.elementId&&v.name===field.name);
    return stored?stored.source??'':field.personalSource??'';
  }

  function update(field:Field, source:string|null) {
    if (!retained || !scope || busy) return;
    clearPlan();
    message='';
    const values=retained.values.filter(v=>v.elementId!==field.elementId || v.name!==field.name);
    values.push({elementId:field.elementId,name:field.name,source});
    retain({...retained,values});
  }

  function edit(field:Field, area:HTMLTextAreaElement) {
    const source=new ExactSource(fieldValue(field));
    source.replaceVisible(area.value);
    update(field,source.value);
  }

  function paste(field:Field, event:ClipboardEvent & {currentTarget:HTMLTextAreaElement}) {
    const text=event.clipboardData?.getData('text/plain');
    if (text===undefined) return;
    event.preventDefault();
    const area=event.currentTarget;
    const start=area.selectionStart;
    const source=new ExactSource(fieldValue(field));
    source.splice(start,area.selectionEnd,text);
    area.value=source.display;
    const end=start+text.replace(/\r\n?/g,'\n').length;
    area.setSelectionRange(end,end);
    update(field,source.value);
  }

  async function submit(action:'preview'|'confirm', started:HTMLElement) {
    if (!scope || !retained || busy || (action==='confirm' && (!preview || !confirming))) return;
    const selected=retained;
    const currentPlan=preview;
    const posting=action==='confirm';
    message='';
    stale=false;
    if (posting) retain({...selected,uncertain:true});
    await operate(async signal=>{
      try {
        const reply=await fetchJSON(callURL(),signal,{
          action,targetTemplateId:selected.target,values:selected.values,
          ...(posting?{receipt:currentPlan?.receipt}:{}),
        });
        if (!reply) return;
        if (reply.status===401 || reply.status===403) {
          if (posting) retain({...selected,uncertain:false});
          clearPlan();
          access(reply.status);
          return;
        }
        if (reply.status===409) {
          retain({...selected,uncertain:false});
          clearPlan();
          stale=true;
          message='Состояние изменилось. Введённые значения сохранены.';
          return;
        }
        if (reply.status===422 || reply.status===400) {
          retain({...selected,uncertain:false});
          clearPlan();
          message=reply.value.state==='catalog_rejected'
            ? 'Xray отклонил каталог. Проверьте значения или выберите другой профиль.'
            : reply.value.state==='bypass_unavailable'
              ? 'Для исключений этого профиля требуется подтверждённая поддержка XKeen.'
              : 'Проверьте обязательные значения и совместимость настроек.';
          return;
        }
        if (reply.status!==200) throw new Error('profile response unavailable');
        if (posting) {
          if (reply.value.templateId!==selected.target || typeof reply.value.desiredRevision!=='string') throw new Error('invalid profile result');
          clearPlan();
          retain();
          result=reply.value;
          if (router && result) router={...router,templateId:result.templateId,desiredRevision:result.desiredRevision};
        } else {
          if (typeof reply.value.receipt!=='string' || !Array.isArray(reply.value.changes) || !reply.value.order) throw new Error('invalid profile plan');
          preview=reply.value;
        }
      } catch {
        if (alive) {
          clearPlan();
          message=posting?'Не удалось подтвердить смену. Проверьте текущее назначение.':'Проверка сейчас недоступна.';
        }
      }
    });
    await restoreFocus(started,()=>result?backLink:retained?.uncertain||stale?checkButton:preview?proceedButton:prepareButton);
  }

  async function inspect(started:HTMLElement) {
    await operate(async signal=>{
      const current=await readCurrent(signal);
      if (!current) return;
      const previous=retained;
      const resumable=previous && previous.target!==current.templateId;
      retain(resumable?{...previous,uncertain:false}:undefined);
      clearPlan();
      scope=undefined;
      stale=!!resumable;
      message='';
      inspected=true;
    });
    await restoreFocus(started,()=>inspected?selectProfile:checkButton);
  }

  function confirmation(next:boolean, started:HTMLElement) {
    confirming=next;
    if (!next) preview=undefined;
    void restoreFocus(started,()=>next?cancelButton:prepareButton);
  }

  onMount(()=>{
    void open();
    const pageHide=()=>{
      controller?.abort();
      controller=undefined;
      busy=false;
      clearPlan();
    };
    window.addEventListener('pagehide',pageHide);
    if (retained?.target&&!retained.uncertain) {
      // Session restoration requires an explicit read of fresh target coordinates.
      stale=true;
    }
    return ()=>{
      alive=false;
      controller?.abort();
      window.removeEventListener('pagehide',pageHide);
    };
  });
</script>

<h1>Смена профиля</h1>
{#if router}<p><bdi>{router.name}</bdi> · <bdi>{router.routerId}</bdi></p>{/if}
<section class="profile-switch" aria-label="Смена профиля">
  {#if router}
    <p><strong>{inspected?'Текущий профиль:':'Действующий профиль:'} <bdi>{router.templateId}</bdi></strong></p>
  {/if}
  {#if message}<p role="status">{message}</p>{/if}
  {#if busy}<p role="status">Проверяем состояние…</p>{/if}
  {#if result}
    <p>Желаемая ревизия {result.desiredRevision}.</p>
    {#if router?.appliedRevision}<p>Последняя подтверждённая агентом ревизия {router.appliedRevision}.</p>{:else}<p>Подтверждения применения от агента пока нет.</p>{/if}
  {:else if retained?.uncertain && !busy}
    {#if !message}<p role="status">Не удалось подтвердить смену. Проверьте текущее назначение.</p>{/if}
    <button class="secondary" bind:this={checkButton} onclick={event=>inspect(event.currentTarget)}>Проверить текущий профиль</button>
  {:else if router}
    <label>Новый профиль
      <select bind:this={selectProfile} value={retained?.target??''} disabled={busy}
        onchange={event=>selectTarget(event.currentTarget.value)}>
        <option value="">Выберите профиль</option>
        {#each templates.filter(template=>template!==router?.templateId) as template (template)}
          <option value={template}>{template}</option>
        {/each}
      </select>
    </label>
    {#if stale && retained?.target}
      <button class="secondary" bind:this={checkButton} disabled={busy} onclick={event=>{
        const started=event.currentTarget;
        const target=retained?.target;
        if (target) void selectTarget(target,true).then(()=>restoreFocus(started,()=>prepareButton??selectProfile));
      }}>Перечитать профиль</button>
    {:else if preview}
      <p>{preview.restoring?'Используются сохранённые настройки профиля.':'Профиль начнётся со своими настройками.'}</p>
      {#if preview.order.state==='reconciled'}
        <p>Добавлено общих правил: {preview.order.added}. Исчезло: {preview.order.removed}. Относительный порядок локальных правил сохранён.</p>
      {:else if preview.order.state==='inherit'}<p>Порядок наследуется от текущего принятого шаблона.</p>{/if}
      <p>{preview.changed?'Желаемая ревизия изменится.':'Желаемая ревизия сохранится.'} Агент подтвердит применение отдельно.</p>
      {#if preview.changes.length}<ul>{#each preview.changes as file (file.name)}<li><bdi>{file.name}</bdi></li>{/each}</ul>{/if}
      {#if confirming}
        <p id={confirmationId}><strong>Подтвердить смену профиля?</strong></p>
        <button aria-describedby={confirmationId} disabled={busy} onclick={event=>submit('confirm',event.currentTarget)}>Подтвердить смену</button>
        <button class="secondary" bind:this={cancelButton} disabled={busy} onclick={event=>confirmation(false,event.currentTarget)}>Отмена</button>
      {:else}
        <button bind:this={proceedButton} disabled={busy} onclick={event=>confirmation(true,event.currentTarget)}>Перейти к подтверждению</button>
        <button class="secondary" disabled={busy} onclick={event=>confirmation(false,event.currentTarget)}>Изменить выбор</button>
      {/if}
    {:else if scope}
      <p>{scope.restoring?'Для этого профиля сохранены персональные настройки и порядок.':'У этого профиля ещё нет сохранённых настроек.'}</p>
      {#each scope.fields as field (JSON.stringify([field.elementId,field.name]))}
        <fieldset disabled={busy}>
          <legend><bdi>{field.section}</bdi> · <bdi>{field.elementId}</bdi></legend>
          <label><bdi>{field.name}</bdi> · {field.type}{field.required?' · обязательное значение':''}
            <textarea rows="2" spellcheck={false} aria-label={field.name} value={new ExactSource(fieldValue(field)).display}
              placeholder={field.defaultSource??'Введите JSON'}
              oninput={event=>edit(field,event.currentTarget)} onpaste={event=>paste(field,event)}></textarea>
          </label>
          {#if !field.required && (field.personalSource!==undefined || fieldValue(field)!=='')}
            <button class="secondary" onclick={()=>update(field,null)}>Использовать шаблон</button>
          {/if}
        </fieldset>
      {/each}
      <button bind:this={prepareButton} disabled={busy} onclick={event=>submit('preview',event.currentTarget)}>Подготовить смену</button>
    {/if}
  {:else if !busy}
    <button class="secondary" onclick={open}>Повторить чтение</button>
  {/if}
  <a href={overviewURL()} bind:this={backLink} data-profile-back onclick={event=>back(event,overviewURL())}>К роутеру</a>
</section>

<style>
  .profile-switch { display:grid; gap:.75rem; max-width:56rem; padding:1rem; border:1px solid var(--line); border-radius:var(--radius); background:white; }
  .profile-switch :where(p,legend,li) { margin:0; overflow-wrap:anywhere; }
  .profile-switch :where(button,a) { justify-self:start; }
  .profile-switch label { display:grid; gap:.4rem; min-width:0; }
  .profile-switch :where(select,textarea) { box-sizing:border-box; width:100%; min-width:0; padding:.55rem; font:inherit; }
  .profile-switch fieldset { box-sizing:border-box; min-width:0; padding:.65rem; border:1px solid var(--line); }
  .profile-switch :where(select,textarea):focus-visible { outline:2px solid #497de5; outline-offset:3px; }
  button.secondary { background:white; color:var(--ink); border:1px solid var(--line); }
</style>
