<script lang="ts">
  import { tick, untrack } from 'svelte';

  type Props = {
    id: string; active: boolean; sessionEpoch: number; csrf: string;
    signedOut: () => void; refreshSession: () => void; saved: () => void;
    openTemplate: (event: MouseEvent, path: string) => void;
  };
  let {id, active, sessionEpoch, csrf, signedOut, refreshSession, saved, openTemplate}: Props = $props();
  let version = $state('');
  let target = $state('');
  let resultId = $state('');
  let state = $state<'ready' | 'stale' | 'unknown' | 'found' | 'saved'>('ready');
  let error = $state('');
  let work = $state<'save' | 'refresh' | 'lookup'>();
  let request: AbortController | undefined;
  let input = $state<HTMLInputElement>();
  const endpoint = () => '/ui/v1/templates/copy?' + new URLSearchParams({templateId:id});
  const editor = (id: string) => '/ui/templates/item?' + new URLSearchParams({templateId:id});
  const canonicalVersion = (value: string) => /^[1-9][0-9]*$/.test(value);
  let initialized = false;
  type CopyReply = {id?: string; version?: string; state?: string; templates?: {id: string}[]};

  async function exchange(operation: 'save' | 'refresh' | 'lookup', path: string, payload?: object) {
    if (!active || request) return;
    const controller = new AbortController();
    const epoch = sessionEpoch;
    request = controller;
    work = operation;
    let reply: {status:number; body:CopyReply | null} | undefined;
    try {
      const response = await fetch(path,{
        cache:'no-store', credentials:'same-origin', redirect:'error',
        signal:AbortSignal.any([controller.signal,AbortSignal.timeout(10000)]),
        ...(payload ? {method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:JSON.stringify(payload)} : {}),
      });
      reply = {status:response.status,body:await response.json()};
    } catch {
      // A transport failure can follow a committed copy.
    }
    if (!active || epoch!==sessionEpoch || request!==controller) return;
    request = undefined;
    work = undefined;
    if (reply?.status===401) {
      signedOut();
      return;
    }
    if (reply?.status===403 && reply.body?.state==='invalid_csrf') {
      error='Состояние сессии изменилось. Повторите действие явно.';
      refreshSession();
      return;
    }
    return reply ?? {status:0,body:null};
  }

  async function refresh() {
    error='';
    const reply=await exchange('refresh','/ui/v1/templates/item?'+new URLSearchParams({templateId:id}));
    if (!reply) return;
    if (reply.status===200 && reply.body?.id===id && canonicalVersion(reply.body?.version ?? '')) {
      version=reply.body.version ?? '';
      state='ready';
      await tick();
      input?.focus();
    } else {
      error=reply.status===404 ? 'Исходный шаблон недоступен.' : 'Не удалось прочитать текущую версию.';
    }
  }

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (!id || !target || !version || state!=='ready' || work) return;
    error='';
    const exact = target;
    const reply=await exchange('save',endpoint(),{targetId:exact,version});
    if (!reply) return;
    if (reply.status===201 && reply.body?.id===exact && reply.body.version==='1') {
      resultId=exact;
      state='saved';
      saved();
    } else if (reply.status===409 && reply.body?.state==='template_exists') {
      error='Этот ID уже занят.';
    } else if (reply.status===409 && reply.body?.state==='source_changed') {
      state='stale';
    } else if (reply.status===404) {
      error='Исходный шаблон недоступен.';
    } else if (reply.status===400 || reply.status===413 || reply.status===415) {
      error='Проверьте идентификатор копии.';
    } else {
      resultId=exact;
      state='unknown';
    }
  }

  async function lookup() {
    error='';
    const reply=await exchange('lookup','/ui/v1/templates');
    if (!reply) return;
    if (reply.status!==200 || !Array.isArray(reply.body?.templates)) {
      error='Не удалось прочитать список. Повторите проверку.';
    } else if (reply.body.templates.some((item: {id:string})=>item.id===resultId)) {
      state='found';
    } else {
      error='Шаблон с этим ID пока не найден.';
    }
  }

  $effect(() => {
    const canRead=active;
    const epoch=sessionEpoch;
    untrack(() => {
      if (request) {
        request.abort();
        request=undefined;
        if (work==='save') {
          resultId=target;
          state='unknown';
        }
        work=undefined;
      }
      if (canRead && !initialized && epoch>=0) {
        initialized=true;
        const initial=new URLSearchParams(location.search).get('version') ?? '';
        if (canonicalVersion(initial)) version=initial;
        else void refresh();
      }
    });
  });
  $effect(() => () => { request?.abort(); request=undefined; });
</script>

<h1>Копирование шаблона</h1>
<section class="copy-card" aria-label="Копирование сохранённой версии">
  {#if state==='saved'}
    <p>Копия сохранена: <bdi>{resultId}</bdi> · версия 1</p>
  {:else if state==='found'}
    <p>Шаблон с этим ID найден. Источник записи не подтверждён.</p>
  {:else}
    <p>Исходный шаблон: <bdi>{id}</bdi>{version ? ' · версия ' + version : ''}</p>
  {/if}
  {#if error}<p role="alert">{error}</p>{/if}
  {#if state==='saved' || state==='found'}
    {@const path=editor(resultId)}
    <a href={path} onclick={event => openTemplate(event,path)}>Открыть шаблон</a>
  {:else if state==='unknown'}
    <p role="status">Не удалось подтвердить создание копии.</p>
    <button disabled={!!work || !active} onclick={lookup}>Проверить список</button>
  {:else if state==='stale'}
    <p role="alert">Исходный шаблон изменился. Перечитайте текущую версию.</p>
    <button disabled={!!work || !active} onclick={refresh}>Перечитать</button>
  {:else if !version}
    {#if work}<p role="status">Загружаем версию…</p>
    {:else}<button onclick={refresh}>Перечитать</button>{/if}
  {:else}
    <form onsubmit={submit}>
      <label for="copy-template-id">Идентификатор копии</label>
      <input id="copy-template-id" placeholder="Например, edge-next" required bind:this={input} bind:value={target} readonly={!!work} />
      <button type="submit" disabled={!!work || !active}>{work==='save' ? 'Создаём копию…' : 'Создать копию'}</button>
    </form>
  {/if}
  {#if state!=='saved' && state!=='found'}
    {@const path=editor(id)}
    <a href={path} onclick={event => openTemplate(event,path)}>Отмена</a>
  {/if}
</section>

<style>
  .copy-card {
    display:grid; gap:.75rem; max-width:42rem; padding:1rem;
    border:1px solid var(--line); background:var(--surface); border-radius:.5rem;
    overflow-wrap:anywhere;
  }
  .copy-card p { margin:0; }
  .copy-card form { display:grid; gap:.5rem; }
  .copy-card input { padding:.65rem; border:1px solid var(--line); border-radius:.4rem; width:100%; min-width:0; }
  .copy-card button { justify-self:start; width:auto; margin:0; }
</style>
