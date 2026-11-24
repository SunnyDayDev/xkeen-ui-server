<script lang="ts">
  import { tick, untrack } from 'svelte';

  type Action = 'disable' | 'remove';
  type Inbound = { id:string; tag:string; origin:'shared' | 'local'; disabled:boolean; dependenciesUnavailable?:boolean };
  type Dependency = { ruleId:string; label:string; origin:'shared' | 'local'; path:string };
  type View = { routerId:string; name:string; templateId:string; acceptedVersion:string; activeGeneration:string;
    draftGeneration:string; inbounds:Inbound[]; selected:string; dependencies:Dependency[] };
  type Reply = View & { state?:string };
  let { id, active, sessionEpoch, csrf, signedOut, refreshSession, back }: {
    id:string; active:boolean; sessionEpoch:number; csrf:string; signedOut:()=>void;
    refreshSession:()=>void; back:(event:MouseEvent,path:string)=>void;
  } = $props();

  const backPath = $derived('/ui/?' + new URLSearchParams({routerId:id}));
  let view = $state<View>();
  let choice = $state('');
  const selected = $derived(view?.inbounds.find(item => item.id === view?.selected));
  let work = $state<'read' | Action>();
  let confirming = $state(false);
  let blocked = $state(false);
  let notice = $state('');
  let saved = $state(false);
  let picker = $state<HTMLSelectElement>();
  let removeButton = $state<HTMLButtonElement>();
  let confirmButton = $state<HTMLButtonElement>();
  let reloadButton = $state<HTMLButtonElement>();
  let operation: AbortController | undefined;
  let observedEpoch: number | undefined;
  let refreshNeeded = true;
  const canChange = $derived(active && !work && !blocked && !!selected && !selected.dependenciesUnavailable && view?.dependencies.length === 0);

  function endpoint(element:string) {
    const query = new URLSearchParams({routerId:id});
    if (element) query.set('elementId',element);
    return '/ui/v1/routers/inbounds?' + query;
  }

  function refuse(state: string | undefined, status: number) {
    blocked = true;
    if (status === 401 && state === 'signed_out') {
      signedOut();
      return;
    }
    if (status === 403 && state === 'invalid_csrf') {
      notice = 'Состояние сессии изменилось. Перечитайте настройки.';
      refreshSession();
      return;
    }
    switch (state) {
      case 'inbound_in_use':
        notice = 'Вход используется. Перечитайте зависимости.';
        break;
      case 'draft_conflict':
        notice = 'Настройки изменились. Перечитайте зависимости и повторите действие.';
        break;
      case 'dependencies_unavailable':
        notice = 'Не удалось подтвердить зависимости. Перечитайте настройки.';
        break;
      case 'different_template':
        notice = 'У роутера есть черновик с другим шаблоном.';
        break;
      case 'router_or_element_unavailable':
        view = undefined;
        choice = '';
        notice = 'Роутер или вход недоступен. Перечитайте настройки.';
        break;
      default:
        notice = 'Не удалось подтвердить состояние входа. Перечитайте настройки.';
    }
  }

  async function request(action?: Action, requested = choice) {
    const current = view;
    if (!active || work || (action && (!canChange || !current || current.selected !== requested))) return;
    const controller = new AbortController();
    operation = controller;
    const epoch = sessionEpoch;
    const stillCurrent = () => operation === controller && active && sessionEpoch === epoch;
    work = action ?? 'read';
    confirming = false;
    notice = '';
    saved = false;
    let committed = false;
    let focus = false;
    try {
      const options = {cache:'no-store',credentials:'same-origin',redirect:'error',
        signal:AbortSignal.any([controller.signal,AbortSignal.timeout(10000)])} as const;
      if (action && current) {
        const response = await fetch(endpoint(requested),{
          ...options,method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},
          body:JSON.stringify({templateId:current.templateId,acceptedVersion:current.acceptedVersion,activeGeneration:current.activeGeneration,
            draftGeneration:current.draftGeneration,action})
        });
        const result = await response.json() as Reply;
        if (!stillCurrent()) return;
        if (!response.ok || result.state !== 'saved') {
          if (result.state === 'inbound_in_use' && Array.isArray(result.dependencies)) view = {...current,dependencies:result.dependencies};
          refuse(result.state,response.status);
          focus = true;
          return;
        }
        committed = true;
        if (action === 'remove') requested = choice = '';
      }

      const response = await fetch(endpoint(requested),options);
      const result = await response.json() as Reply;
      if (!stillCurrent()) return;
      if (!response.ok || result.routerId !== id || !Array.isArray(result.inbounds) ||
          !Array.isArray(result.dependencies) || (result.inbounds.length > 0 && !result.inbounds.some(item=>item.id===result.selected))) {
        refuse(result.state,response.status);
        if (committed && active) notice = 'Запись подтверждена. Перечитайте текущее состояние входов.';
        focus = !!action;
        return;
      }
      view = result;
      choice = result.selected;
      blocked = false;
      if (committed) {
        saved = true;
        focus = true;
        notice = action === 'disable' ? 'Черновик сохранён. Вход выключен.' : 'Черновик сохранён. Вход удалён.';
      }
    } catch {
      if (!stillCurrent()) return;
      blocked = true;
      focus = !!action;
      notice = committed ? 'Запись подтверждена. Перечитайте текущее состояние входов.'
        : action ? 'Результат сохранения неизвестен. Перечитайте настройки.' : 'Не удалось загрузить входы. Перечитайте настройки.';
    } finally {
      if (operation === controller) {
        operation = undefined;
        work = undefined;
        if (focus) {
          await tick();
          if (active && epoch === sessionEpoch) (blocked ? reloadButton : picker)?.focus();
        }
      }
    }
  }

  $effect(() => {
    const ready = active;
    const changed = observedEpoch !== sessionEpoch;
    observedEpoch = sessionEpoch;
    untrack(() => {
      if (operation && (changed || !ready)) {
        operation.abort();
        operation = undefined;
        if (work && work !== 'read') {
          blocked = true;
          notice = 'Результат сохранения неизвестен. Перечитайте настройки.';
        }
        work = undefined;
      }
      if (!ready) confirming = false;
      if (changed || !ready) refreshNeeded = true;
      if (ready && !blocked && (refreshNeeded || !view) && !operation) {
        refreshNeeded = false;
        void request();
      }
    });
  });
  $effect(() => () => { operation?.abort(); operation = undefined; });

  async function askRemove() {
    if (!canChange) return;
    confirming = true;
    notice = '';
    await tick();
    confirmButton?.focus();
  }

  async function cancelRemove() {
    confirming = false;
    await tick();
    removeButton?.focus();
  }
</script>

{#if view}<p class="context"><bdi>{view.name}</bdi> / <bdi>{view.templateId}</bdi></p>{/if}
<h1>Входы роутера</h1>
{#if work === 'read' && !view}<p role="status">Загружаем входы…</p>{/if}
{#if view}
  {#if selected}
    <section class="inbound" aria-label="Зависимости входа">
      <label>Выбранный вход
        <select aria-label="Выбранный вход" bind:this={picker} bind:value={choice} disabled={!!work || confirming}
          onchange={(event) => request(undefined,event.currentTarget.value)}>
          {#each view.inbounds as inbound (inbound.id)}
            <option value={inbound.id}>{inbound.tag || inbound.id}</option>
          {/each}
        </select>
      </label>
      <p>{selected.origin === 'shared' ? 'Общий вход' : 'Локальный вход'} · {selected.disabled ? 'выключен' : 'включён'}</p>
      {#if view.dependencies.length}
        <h2>Используется в итоговом роутинге</h2>
        <ul>
          {#each view.dependencies as dependency}
            <li><bdi title={dependency.ruleId}>{dependency.label || dependency.ruleId}</bdi> · <code>{dependency.path}</code></li>
          {/each}
        </ul>
        <p>Перед выключением или удалением измените зависимые правила.</p>
      {:else if selected.dependenciesUnavailable}
        {#if !notice}<p role="alert">Не удалось подтвердить зависимости. Перечитайте настройки.</p>{/if}
      {:else if !blocked && !confirming && !saved}
        <p>Действующих зависимостей нет.</p>
      {/if}
      {#if confirming}
        <h2>Удалить этот вход из настроек роутера?</h2>
        <p>{selected.origin === 'shared' ? 'Общий вход будет исключён только для этого роутера.' : 'Локальный вход будет удалён из черновика.'}</p>
        <div class="actions">
          <button bind:this={confirmButton} disabled={!canChange} onclick={() => request('remove')}>Удалить</button>
          <button class="secondary" onclick={cancelRemove}>Отмена</button>
        </div>
      {:else if canChange}
        <div class="actions">
          {#if !selected.disabled}<button class="secondary" onclick={() => request('disable')}>Выключить</button>{/if}
          <button bind:this={removeButton} class="secondary" onclick={askRemove}>Удалить из настроек роутера</button>
        </div>
      {/if}
    </section>
  {:else}
    <p>Входов пока нет.</p>
  {/if}
{/if}
{#if notice}<p role={saved ? 'status' : 'alert'} class:notice={!saved} class:error={!saved}>{notice}</p>{/if}
<div class="actions">
  {#if blocked || selected?.dependenciesUnavailable}
    <button bind:this={reloadButton} disabled={!active || !!work} onclick={() => request()}>Перечитать настройки</button>
  {/if}
  <a href={backPath} onclick={(event) => back(event,backPath)}>К роутеру</a>
</div>

<style>
  .context { color:var(--muted); font-size:.86rem; overflow-wrap:anywhere; }
  .inbound { display:grid; gap:.7rem; border:1px solid var(--line); background:var(--surface); padding:1rem; border-radius:var(--radius); }
  p,h2,ul { margin:0; overflow-wrap:anywhere; }
  h2 { font-size:1.04rem; }
  label { display:grid; gap:.4rem; }
  select { width:100%; min-width:0; max-width:32rem; background:var(--surface); color:var(--ink); padding:.6rem; border:1px solid var(--line); border-radius:var(--radius); }
  .actions { display:flex; flex-wrap:wrap; align-items:center; gap:.75rem; }
  .secondary { background:var(--surface); color:var(--ink); border:1px solid var(--line); }
</style>
