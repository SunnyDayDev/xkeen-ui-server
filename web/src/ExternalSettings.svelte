<script lang="ts">
  import { onMount } from 'svelte';

  let { csrf, refreshSession }: {csrf: string; refreshSession: () => Promise<void>} = $props();
  type Settings = {enabled: boolean; protected: boolean; hasToken: boolean; version: string};
  let state = $state<Settings>();
  let enabled = $state(false);
  let protectedMode = $state(true);
  let changed = $derived(state && (enabled !== state.enabled || protectedMode !== state.protected));
  let busy = $state(false);
  let confirmReplace = $state(false);
  let secret = $state('');
  let message = $state('');
  let needsReload = $state(false);
  let alive = true;
  const lifetime = new AbortController();

  function valid(v: Settings) {
    return v && typeof v.enabled === 'boolean' && typeof v.protected === 'boolean'
      && typeof v.hasToken === 'boolean' && typeof v.version === 'string' && /^[1-9][0-9]{0,18}$/.test(v.version);
  }

  async function request(action: 'read' | 'save' | 'issue') {
    if (busy || !alive) return;
    busy = true;
    message = '';
    secret = '';
    confirmReplace = false;
    try {
      const writing = action !== 'read';
      const headers: Record<string,string> = {Accept: 'application/json'};
      let body: string | undefined;
      if (writing) {
        if (!state) return;
        headers['X-CSRF-Token'] = csrf;
        headers['Content-Type'] = 'application/json';
        body = JSON.stringify(action === 'save' ? {version:state.version,enabled,protected:protectedMode} : {version:state.version});
      }
      const res = await fetch('/ui/v1/settings/external-api' + (action === 'issue' ? '/token' : ''), {
        method: action === 'read' ? 'GET' : action === 'save' ? 'PATCH' : 'POST', headers, body,
        cache:'no-store',credentials:'same-origin',redirect:'error',
        signal:AbortSignal.any([lifetime.signal, AbortSignal.timeout(10000)])
      });
      if (!alive) return;
      if (res.status === 401 || res.status === 403) {
        state = undefined; needsReload = true; message = 'Проверьте сессию и обновите настройки.';
        await refreshSession(); return;
      }
      if (res.status === 409) {
        needsReload = true; message = 'Настройки изменились в другой вкладке. Обновите их перед сохранением.'; return;
      }
      if (!res.ok) throw new Error();
      const result = await res.json();
      if (!alive) return;
      if (!valid(result) || (action === 'issue' && (typeof result.token !== 'string' || !/^[A-Za-z0-9_-]{43}$/.test(result.token)))) throw new Error();
      state = {enabled:result.enabled,protected:result.protected,hasToken:result.hasToken,version:result.version};
      enabled = state.enabled; protectedMode = state.protected; needsReload = false;
      if (action === 'issue') secret = result.token;
      if (action === 'save') message = 'Настройки сохранены.';
    } catch {
      if (alive) { needsReload = true; message = action === 'read' ? 'Не удалось прочитать настройки.' : 'Ответ не получен. Обновите состояние перед следующим действием.'; }
    } finally { if (alive) busy = false; }
  }
  async function copy() {
    try { await navigator.clipboard.writeText(secret); if (alive) message = 'Токен скопирован.'; }
    catch { if (alive) message = 'Выделите токен и скопируйте вручную.'; }
  }
  onMount(() => {
    void request('read');
    return () => { alive = false; lifetime.abort(); secret = ''; };
  });
</script>

<div class="settings">
  <header><h1>Настройки</h1><p>Доступ внешних приложений к управлению сервисом</p></header>
  {#if message}<p class="notice" role="status">{message}</p>{/if}
  {#if needsReload}<button disabled={busy} onclick={() => request('read')}>Обновить настройки</button>{/if}
  {#if state}
    <fieldset disabled={busy || needsReload}>
      <legend>Внешний доступ</legend>
      <label><span>Внешний API</span><input type="checkbox" bind:checked={enabled} /></label>
      <label><span>Авторизация API</span><input type="checkbox" bind:checked={protectedMode} /></label>
      {#if enabled && !protectedMode}<p>Без авторизации управлять через API сможет любой клиент с сетевым доступом к серверу.</p>{/if}
      <button disabled={!changed} onclick={() => request('save')}>Сохранить настройки</button>
    </fieldset>
    <section class="token-panel">
      {#if secret}
        <h2>Токен выпущен</h2><p>Скопируйте токен сейчас. После закрытия он больше не показывается.</p>
        <label for="issued-token">Новый токен</label><input id="issued-token" readonly value={secret} autocomplete="off" spellcheck="false" />
        <div class="actions"><button onclick={copy}>Скопировать</button><button onclick={() => {secret = ''; message = '';}}>Скрыть токен</button></div>
      {:else if state.enabled && !state.protected}
        <h2>Токен не требуется</h2><p>API доступен без авторизации всем клиентам, которые могут подключиться к серверу по сети.</p>
      {:else}
        <h2>Токен API</h2>
        {#if confirmReplace}
          <p>Заменить общий токен? Все клиенты со старым токеном сразу потеряют доступ.</p>
          <div class="actions"><button onclick={() => request('issue')}>Подтвердить замену</button><button onclick={() => confirmReplace = false}>Отмена</button></div>
        {:else}
          <p>{state.hasToken ? 'Токен выпущен. При замене прежнее значение сразу перестанет работать у всех клиентов.' : 'Токен ещё не выпущен. При включённой авторизации внешним клиентам нужен токен.'}</p>
          <button disabled={busy || needsReload || changed} onclick={() => state?.hasToken ? confirmReplace = true : request('issue')}>{state.hasToken ? 'Заменить токен' : 'Выпустить токен'}</button>
        {/if}
      {/if}
    </section>
  {:else if !needsReload}<p role="status">Загружаем настройки.</p>{/if}
</div>
<style>
  .settings { max-width: 56rem; display:grid; gap:1rem; }
  header p, .token-panel p, fieldset p { margin:.5rem 0 1rem; }
  fieldset,.token-panel { color:var(--ink); margin:0; padding:1.25rem; border:1px solid var(--line); background:white; border-radius:var(--radius); }
  legend { font-weight:600; }
  fieldset label { display:flex; justify-content:space-between; align-items:center; gap:1.25rem; padding:.75rem 0; border-bottom:1px solid var(--line); }
  fieldset input { width:1.25rem; height:1.25rem; min-height:0; accent-color:var(--accent); }
  fieldset button { margin-top:1rem; }
  .token-panel input { width:100%; font-family:monospace; }
  .actions { display:flex; gap:.75rem; flex-wrap:wrap; margin-top:1rem; }
  h2 { font-size:1.25rem; margin:0; }
</style>
