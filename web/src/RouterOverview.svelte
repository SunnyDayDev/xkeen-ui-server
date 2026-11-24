<script lang="ts">
  import { onMount, tick } from 'svelte';

  type Router = {
    routerId: string; name: string; templateId: string; contact: 'online' | 'offline';
    desiredRevision: string; appliedRevision?: string; runtime?: 'unknown' | 'verified';
  };
  type Props = {
    selectedId: string; openRouter: (event: MouseEvent, path: string) => void;
    openInbounds: (event: MouseEvent, path: string) => void;
    openCatalog: (event: MouseEvent, path: string) => void;
    openEnrollment: (event: MouseEvent, path: string) => void;
    closeRouter: () => void; openVariables: (event: MouseEvent, path: string) => void; openRules: (event: MouseEvent, path: string) => void; signedOut: () => void;
  };
  let {selectedId, openRouter, openEnrollment, closeRouter, openVariables, openRules, openInbounds, openCatalog, signedOut}: Props = $props();

  let rows = $state<Router[]>();
  let failed = $state(false);
  let group = $state<HTMLElement>();
  let panel = $state<HTMLElement>();
  let focusTarget = $state<HTMLElement>();
  let pending: AbortController | undefined;
  let detail = $derived(rows?.find(row => row.routerId === selectedId));

  async function load(explicit = false) {
    if (pending || document.hidden || (failed && !explicit)) return;
    if (explicit) failed = false;
    const controller = new AbortController();
    pending = controller;
    try {
      const response = await fetch('/ui/v1/routers', {
        cache: 'no-store', signal: AbortSignal.any([controller.signal,AbortSignal.timeout(8000)]),
      });
      if (pending !== controller) return;
      if (response.status === 401) {
        rows = undefined;
        signedOut();
        return;
      }
      if (!response.ok) throw new Error('router list unavailable');
      const list = (await response.json()).routers;
      if (pending !== controller) return;
      if (!Array.isArray(list)) throw new Error('router list unavailable');
      rows = list;
    } catch {
      if (pending === controller) {
        rows = undefined;
        failed = true;
      }
    } finally {
      if (pending === controller) pending = undefined;
    }
  }

  async function close() {
    const former = selectedId;
    closeRouter();
    await tick();
    const options = group?.querySelectorAll<HTMLAnchorElement>('a[data-router]') ?? [];
    const selected = Array.from(options).find(link => link.dataset.router === former);
    (selected ?? focusTarget)?.focus();
  }

  $effect(() => {
    if (selectedId && panel) {
      panel.focus();
    }
  });

  onMount(() => {
    void load();
    const check = () => { void load(); };
    const timer = setInterval(check,2500);
    window.addEventListener('focus', check);
    document.addEventListener('visibilitychange', check);
    return () => {
      clearInterval(timer);
      pending?.abort();
      pending = undefined;
      window.removeEventListener('focus', check);
      document.removeEventListener('visibilitychange', check);
    };
  });
</script>

{#snippet status(router: Router)}
  <p>Связь: {router.contact === 'online' ? 'на связи' : 'нет связи'}</p>
  <p>Желаемая ревизия {router.desiredRevision}</p>
  {#if router.appliedRevision}
    <p>Последняя подтверждённая ревизия {router.appliedRevision}</p>
  {:else}
    <p>Применение: нет подтверждения</p>
  {/if}
  {#if router.runtime === 'unknown'}<p>Текущее состояние Xray неизвестно.</p>{/if}
{/snippet}

<div class:with-detail={!!selectedId && !!rows} class="router-overview">
  <section class="overview-card" aria-label="Подключённые роутеры" bind:this={focusTarget} tabindex="-1">
    {#if failed}
      <p role="alert">Не удалось загрузить список роутеров.</p>
      <button onclick={() => { void load(true); }}>Повторить</button>
    {:else if !rows}
      <p role="status">Загружаем роутеры…</p>
    {:else if rows.length === 0}
      <p>Роутеров с подтверждённым контактом пока нет.</p>
    {:else}
      <ul bind:this={group}>
        {#each rows as router (router.routerId)}
          {@const path = '/ui/?' + new URLSearchParams({routerId:router.routerId})}
          <li>
            <a href={path} data-router={router.routerId} aria-current={router.routerId === selectedId ? 'true' : undefined}
              onclick={event => openRouter(event,path)}><bdi>{router.name}</bdi></a>
            {@render status(router)}
          </li>
        {/each}
      </ul>
    {/if}
  </section>
  {#if selectedId && rows}
    <aside class="overview-card router-detail" aria-label="Сведения о роутере" tabindex="-1" bind:this={panel}>
      {#if detail}
        <h2><bdi>{detail.name}</bdi></h2>
      {:else}
        <h2>Роутер недоступен в текущем списке.</h2>
      {/if}
      <button type="button" class="secondary" onclick={close}>Закрыть панель</button>
      {#if detail}
        {@const path = '/ui/routers/variables?' + new URLSearchParams({routerId:detail.routerId})}
        {@const keys = '/ui/routers/enroll?' + new URLSearchParams({routerId: detail.routerId})}
        <p>ID: <bdi>{detail.routerId}</bdi></p>
        <p>Шаблон: <bdi>{detail.templateId}</bdi></p>
        {@render status(detail)}
        <a href={path} onclick={(event) => openVariables(event,path)}>Переменные</a>
        <a href={'/ui/routers/exclusions?' + new URLSearchParams({routerId:detail.routerId})}>Исключения XKeen</a>
        <a href={keys} onclick={event => openEnrollment(event,keys)}>Рабочий ключ</a>
        <a href={'/ui/routers/rules?' + new URLSearchParams({routerId:detail.routerId})}
          onclick={(event) => openRules(event,event.currentTarget.pathname+event.currentTarget.search)}>Маршрутизация</a>
        <a href={'/ui/routers/inbounds?' + new URLSearchParams({routerId:detail.routerId})}
          onclick={(event) => openInbounds(event,event.currentTarget.pathname+event.currentTarget.search)}>Входы</a>
        <a href={'/ui/routers/catalog?' + new URLSearchParams({routerId:detail.routerId})}
          onclick={(event) => openCatalog(event,event.currentTarget.pathname+event.currentTarget.search)}>Файлы</a>
      {/if}
    </aside>
  {/if}
</div>

<style>
  .router-overview { display:grid; gap:1rem; margin-top:1rem; align-items:start; }
  .router-overview.with-detail { grid-template-columns:minmax(0,1fr) minmax(0,21rem); }
  .overview-card {
    display:grid; gap:.55rem; min-width:0; border:1px solid var(--line); background:var(--surface);
    border-radius:.5rem; padding:1rem; overflow-wrap:anywhere;
  }
  .overview-card h2 { font-size:1.05rem; margin:0; }
  .overview-card p { margin:0; }
  .overview-card button { justify-self:start; width:auto; margin:0; }
  ul { display:grid; margin:0; padding:0; list-style:none; }
  li { display:grid; gap:.35rem; padding:.75rem 0; }
  li:first-child { padding-top:0; }
  li:last-child { padding-bottom:0; }
  li + li { border-top:1px solid var(--line); }
  li a { justify-self:start; color:var(--accent); font-weight:600; }
  @media (max-width: 720px) {
    .router-overview.with-detail { grid-template-columns:minmax(0,1fr); }
    .router-detail { grid-row:1; }
  }
</style>
