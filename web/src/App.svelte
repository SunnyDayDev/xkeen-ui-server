<script lang="ts">
  import { onMount, tick } from 'svelte';
  import ExternalSettings from './ExternalSettings.svelte';
  import { sessionRequest, type Session } from './session';

  import TemplateList from './TemplateList.svelte';
  import TemplateCreate from './TemplateCreate.svelte';
  import ExclusionEditor from './ExclusionEditor.svelte';
  import TemplateEditor from './TemplateEditor.svelte';
  import TemplateCopy from './TemplateCopy.svelte';
  import RouterPreparation from './RouterPreparation.svelte';
  import RouterEnrollment from './RouterEnrollment.svelte';
  import RouterOverview from './RouterOverview.svelte';
  import RouterVariables from './RouterVariables.svelte';
  import RouterExclusions from './RouterExclusions.svelte';
  import RouterReplacement from './RouterReplacement.svelte';
  import RouterRules from './RouterRules.svelte';
  import RouterInbounds from './RouterInbounds.svelte';
  import RouterCatalog from './RouterCatalog.svelte';
  import RouterProfileSwitch, {type ProfileSelection} from './RouterProfileSwitch.svelte';
  import TemplatePublication from './TemplatePublication.svelte';

  type Section = 'router-exclusions' | 'template-exclusions' | 'overview' | 'templates' | 'settings' | 'template-editor' | 'template-copy' | 'router-connect' | 'router-enroll' | 'router-variables' | 'router-replacement' | 'router-rules' | 'router-inbounds' | 'router-catalog' | 'template-publication' | 'router-profile';

  function sectionForPath(): Section {
    if (location.pathname === '/ui/templates/copy') return 'template-copy';
    if (location.pathname === '/ui/templates/publication') return 'template-publication';
    if (location.pathname === '/ui/templates/exclusions') return 'template-exclusions';
    if (location.pathname === '/ui/templates/item') return 'template-editor';
    if (location.pathname === '/ui/routers/inbounds') return 'router-inbounds';
    if (location.pathname === '/ui/routers/profile') return 'router-profile';
    if (location.pathname === '/ui/routers/catalog') return 'router-catalog';
    if (location.pathname === '/ui/routers/rules') return 'router-rules';
    if (location.pathname === '/ui/routers/exclusions') return 'router-exclusions';
    if (location.pathname === '/ui/routers/replacement') return 'router-replacement';
    if (location.pathname === '/ui/routers/variables') return 'router-variables';
    if (location.pathname === '/ui/routers/enroll') return 'router-enroll';
    if (location.pathname === '/ui/routers/connect') return 'router-connect';
    if (location.pathname === '/ui/settings') return 'settings';
    return location.pathname === '/ui/templates' ? 'templates' : 'overview';
  }

  let section = $state<Section>(sectionForPath());
  let editorID = $state(new URLSearchParams(location.search).get('templateId') ?? '');
  let routerID = $state(new URLSearchParams(location.search).get('routerId') ?? '');
  let routerElementID = $state(new URLSearchParams(location.search).get('elementId') ?? '');

  let view = $state<'initial' | 'setup_required' | 'signed_out' | 'active' | 'unavailable'>('initial');
  let message = $state('');
  let opened = $state(false);
  let expired = $state(false);
  let busy = $state(false);
  let password = $state('');
  let csrf = $state('');
  let unconfirmedHide = $state(new Set<string>());
  let profileSelections = $state(new Map<string,ProfileSelection>());
  let profileReturnID = $state('');
  let nameDrafts = $state(new Map<string,{text: string; unknown: boolean}>());
  let templateRevision = $state(0);
  let passwordField = $state<HTMLInputElement>();
  let workspace = $state<HTMLElement>();
  let reading = false;
  let mutation = $state(0);
  let retainedFocus: HTMLElement | undefined;

  async function focusPassword() {
    await tick();
    passwordField?.focus();
  }

  async function focusWorkspace(restore = false) {
    await tick();
    if (restore && retainedFocus?.isConnected && workspace?.contains(retainedFocus)) {
      retainedFocus.focus();
      if (document.activeElement === retainedFocus) return;
    }
    workspace?.focus();
  }

  function navigate(event: MouseEvent, path: string, destination: Section) {
    if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    event.preventDefault();
    goTo(path,destination);
  }

  function goTo(path: string, destination: Section) {
    if (location.pathname + location.search !== path) history.pushState({}, '', path);
    editorID = new URLSearchParams(location.search).get('templateId') ?? '';
    routerID = new URLSearchParams(location.search).get('routerId') ?? '';
    routerElementID = new URLSearchParams(location.search).get('elementId') ?? '';
    section = destination;
    void focusWorkspace();
  }

  function closeRouterDetail() {
    if (!routerID) return;
    history.pushState({}, '', '/ui/');
    routerID = '';
  }

  async function endAccess(state: 'signed_out' | 'setup_required') {
    if (view === 'active') {
      mutation++;
      const focused = document.activeElement;
      retainedFocus = focused instanceof HTMLElement && workspace?.contains(focused) ? focused : undefined;
      expired = true;
      message = '';
    }
    csrf = '';
    const changed = view !== state;
    view = state;
    if (changed && state === 'signed_out') await focusPassword();
  }

  async function refresh() {
    if (busy || reading || document.hidden) return;
    reading = true;
    const generation = mutation;
    const reply = await sessionRequest('session');
    reading = false;
    if (generation !== mutation || busy) return;
    if (reply.state === 'active' && !expired) {
      const currentProof = reply.csrf ?? '';
      if (view === 'active' && csrf !== currentProof) mutation++;
      csrf = currentProof;
      if (view !== 'active') {
        view = 'active';
        opened = true;
        await focusWorkspace();
      }
    } else if (reply.state === 'signed_out' || reply.state === 'setup_required') {
      await endAccess(reply.state);
    } else if (reply.state === 'unavailable' && (view === 'initial' || view === 'unavailable')) {
      view = 'unavailable';
    }
  }

  function showFailure(result: Session) {
    switch (result.state) {
      case 'invalid_credentials':
        message = 'Не удалось войти. Проверьте пароль и попробуйте ещё раз.';
        break;
      case 'rate_limited':
        message = 'Слишком много попыток входа. Повторите позже.';
        break;
      default:
        message = 'Сервер сейчас недоступен. Попробуйте ещё раз.';
    }
  }

  async function login(event: SubmitEvent) {
    event.preventDefault();
    if (busy) return;
    busy = true;
    mutation++;
    message = '';
    const pending = sessionRequest('login', password);
    password = '';
    const result = await pending;
    busy = false;
    if (result.state === 'active') {
      csrf = result.csrf ?? '';
      opened = true;
      const restore = expired;
      expired = false;
      view = 'active';
      await focusWorkspace(restore);
      retainedFocus = undefined;
    } else if (result.state === 'setup_required') {
      view = 'setup_required';
    } else {
      showFailure(result);
      await focusPassword();
    }
  }

  async function logout() {
    if (busy) return;
    busy = true;
    mutation++;
    message = '';
    const result = await sessionRequest('logout', csrf);
    busy = false;
    if (result.state === 'signed_out') {
      csrf = '';
      opened = false;
      expired = false;
      retainedFocus = undefined;
      view = 'signed_out';
      message = 'Вы вышли из консоли.';
      await focusPassword();
    } else if (result.state === 'invalid_csrf') {
      message = 'Состояние сессии изменилось. Повторите выход после проверки.';
      await refresh();
    } else {
      showFailure(result);
    }
  }

  onMount(() => {
    const check = () => { void refresh(); };
    const locationChanged = () => {
      section = sectionForPath();
      editorID = new URLSearchParams(location.search).get('templateId') ?? '';
      routerID = new URLSearchParams(location.search).get('routerId') ?? '';
      routerElementID = new URLSearchParams(location.search).get('elementId') ?? '';
      if (view === 'active') void focusWorkspace();
    };
    window.addEventListener('popstate', locationChanged);
    check();
    const timer = setInterval(check, 15_000);
    window.addEventListener('focus', check);
    document.addEventListener('visibilitychange', check);
    return () => {
      clearInterval(timer);
      window.removeEventListener('popstate', locationChanged);
      window.removeEventListener('focus', check);
      document.removeEventListener('visibilitychange', check);
    };
  });
</script>

{#if opened}
  <section class="console" hidden={view !== 'active'} inert={view !== 'active'}>
    <header class="console-header">
      <strong>XKeen Console</strong>
      <nav aria-label="Основная навигация">
        <a href="/ui/" aria-current={section === 'overview' || section === 'router-variables' || section === 'router-replacement' || section === 'router-rules' || section === 'router-inbounds' || section === 'router-catalog' ? 'page' : undefined}
          onclick={(event) => navigate(event, '/ui/', 'overview')}>Обзор</a>
        <a href="/ui/templates" aria-current={section === 'templates' || section === 'template-editor' || section === 'template-copy' || section === 'template-exclusions' || section === 'template-publication' ? 'page' : undefined}
          onclick={(event) => navigate(event, '/ui/templates', 'templates')}>Шаблоны</a>
        <a href="/ui/settings" aria-current={section === 'settings' ? 'page' : undefined}
          onclick={(event) => navigate(event, '/ui/settings', 'settings')}>Настройки</a>
      </nav>
      <button onclick={logout} disabled={busy}>Выйти</button>
    </header>
    <main bind:this={workspace} tabindex="-1">
      {#if section === 'templates'}
        <TemplateCreate canSubmit={view === 'active' && !busy} sessionEpoch={mutation} {csrf}
          changed={() => { templateRevision++; }} signedOut={() => { void endAccess('signed_out'); }}
          refreshSession={() => { void refresh(); }} />
        <TemplateList active={view === 'active'} refreshKey={templateRevision}
          signedOut={() => { void endAccess('signed_out'); }}
          openTemplate={(event, path) => navigate(event, path, 'template-editor')} />
      {:else if section === 'template-exclusions'}
        {#key editorID}<ExclusionEditor id={editorID} active={view === 'active'} sessionEpoch={mutation} {csrf} signedOut={() => {void endAccess('signed_out');}} refreshSession={() => {void refresh();}} />{/key}
      {:else if section === 'template-editor'}
        {#key editorID}<TemplateEditor id={editorID} active={view === 'active'} sessionEpoch={mutation} {csrf}
          changed={() => { templateRevision++; }} signedOut={() => { void endAccess('signed_out'); }}
          refreshSession={() => { void refresh(); }} openCopy={(path) => goTo(path,'template-copy')}
          openPublication={(path) => goTo(path,'template-publication')} />{/key}
      {:else if section === 'template-publication'}
        {#key editorID}
          <TemplatePublication id={editorID} active={view === 'active' && !busy} sessionEpoch={mutation} {csrf}
            signedOut={() => { void endAccess('signed_out'); }} refreshSession={() => { void refresh(); }}
            back={(event,path) => navigate(event,path,'template-editor')} />
        {/key}
      {:else if section === 'template-copy'}
        {#key editorID}<TemplateCopy id={editorID} active={view === 'active'} sessionEpoch={mutation} {csrf}
          signedOut={() => { void endAccess('signed_out'); }} refreshSession={() => { void refresh(); }}
          saved={() => { templateRevision++; }}
          openTemplate={(event,path) => navigate(event,path,'template-editor')} />{/key}
      {:else if section === 'settings'}
        {#if view === 'active'}{#key csrf}<ExternalSettings {csrf} refreshSession={refresh} />{/key}{/if}
      {:else if section === 'router-connect'}
        <RouterPreparation canSubmit={view === 'active' && !busy} sessionEpoch={mutation} {csrf}
          signedOut={() => { void endAccess('signed_out'); }} refreshSession={() => { void refresh(); }}
          openEnrollment={(event,path) => navigate(event,path,'router-enroll')} />
      {:else if section === 'router-profile'}
        {#if view === 'active' && !busy}
          {#key routerID + ':' + mutation}
            {@const profileID = routerID}
            <RouterProfileSwitch id={profileID} {csrf} retained={profileSelections.get(profileID)}
              retain={selection => {
                const next = new Map(profileSelections);
                if (selection) next.set(profileID,selection); else next.delete(profileID);
                profileSelections = next;
              }}
              signedOut={() => {void endAccess('signed_out');}} refreshSession={() => {void refresh();}}
              back={(event,path) => { profileReturnID=profileID; navigate(event,path,'overview'); }} />
          {/key}
        {/if}
      {:else if section === 'router-catalog'}
        {#key routerID}
          <RouterCatalog id={routerID} active={view === 'active' && !busy} sessionEpoch={mutation}
            signedOut={() => { void endAccess('signed_out'); }}
            back={(event,path) => navigate(event,path,'overview')} />
        {/key}
      {:else if section === 'router-inbounds'}
        {#key routerID}
          <RouterInbounds id={routerID} active={view === 'active' && !busy} sessionEpoch={mutation} {csrf}
            signedOut={() => { void endAccess('signed_out'); }} refreshSession={() => { void refresh(); }}
            back={(event,path) => navigate(event,path,'overview')} />
        {/key}
      {:else if section === 'router-rules'}
        {#key routerID}
          <RouterRules id={routerID} active={view === 'active' && !busy} sessionEpoch={mutation} {csrf}
            signedOut={() => { void endAccess('signed_out'); }} refreshSession={() => { void refresh(); }}
            back={(event,path) => navigate(event,path,'overview')} />
        {/key}
      {:else if section === 'router-exclusions'}
        {#if view === 'active' && !busy}{#key routerID + ':' + mutation}<RouterExclusions id={routerID} {csrf} signedOut={()=>{void endAccess('signed_out')}} refreshSession={()=>{void refresh()}} />{/key}{/if}
      {:else if section === 'router-replacement'}
        {#key JSON.stringify([routerID,routerElementID])}
          <RouterReplacement id={routerID} elementId={routerElementID} active={view === 'active' && !busy} sessionEpoch={mutation} {csrf}
            signedOut={() => { void endAccess('signed_out'); }} refreshSession={() => { void refresh(); }}
            back={(event,path) => navigate(event,path,'router-variables')} />
        {/key}
      {:else if section === 'router-variables'}
        {#key JSON.stringify([routerID,routerElementID])}
          <RouterVariables id={routerID} initialElementId={routerElementID} active={view === 'active' && !busy} sessionEpoch={mutation} {csrf}
            signedOut={() => { void endAccess('signed_out'); }} refreshSession={() => { void refresh(); }}
            back={(event,path) => navigate(event,path,'overview')}
            openReplacement={(event,path) => navigate(event,path,'router-replacement')} />
        {/key}
      {:else if section === 'router-enroll'}
        {#if view === 'active' && !busy}
          {#key routerID + ':' + mutation}
            <RouterEnrollment id={routerID} {csrf}
              signedOut={() => { void endAccess('signed_out'); }} refreshSession={() => { void refresh(); }} />
          {/key}
        {/if}
      {:else}
        <h1>Обзор</h1>
        <a class="overview-action" href="/ui/routers/connect"
          onclick={(event)=>navigate(event,'/ui/routers/connect','router-connect')}>Подключить роутер</a>
        {#if view === 'active' && !busy}
          {#key mutation}
            <RouterOverview selectedId={routerID} {csrf} {unconfirmedHide} {nameDrafts} {profileReturnID}
              clearProfileReturn={() => {profileReturnID='';}} openProfile={(event,path) => navigate(event,path,'router-profile')} refreshSession={() => {void refresh();}}
              setUnconfirmedHide={(id,pending) => {
                const next = new Set(unconfirmedHide);
                if (pending) next.add(id); else next.delete(id);
                unconfirmedHide = next;
              }}
              retainName={(id,value) => {
                const next = new Map(nameDrafts);
                if (value) next.set(id,value); else next.delete(id);
                nameDrafts = next;
              }}
              openRouter={(event,path) => navigate(event,path,'overview')} closeRouter={closeRouterDetail}
              openVariables={(event,path) => navigate(event,path,'router-variables')}
              openEnrollment={(event,path) => navigate(event,path,'router-enroll')}
              openRules={(event,path) => navigate(event,path,'router-rules')}
              openInbounds={(event,path) => navigate(event,path,'router-inbounds')}
              openCatalog={(event,path) => navigate(event,path,'router-catalog')}
              signedOut={() => { void endAccess('signed_out'); }} />
          {/key}
        {/if}
      {/if}
      {#if message && view === 'active'}
        <p class="notice error" role="status">{message}</p>
      {/if}
    </main>
  </section>
{/if}

{#if view !== 'active'}
  <div class="access">
    <section class="access-card" aria-labelledby="access-heading">
      <span class="brand">XKeen Console</span>
      {#if view === 'setup_required'}
        <h1 id="access-heading">Доступ ещё не настроен</h1>
        <p>Создайте пароль администратора локально на сервере через терминал или приватный файл.</p>
        <button onclick={refresh}>Проверить настройку</button>
      {:else if view === 'initial'}
        <h1 id="access-heading">Вход в консоль</h1>
        <p role="status">Проверяем состояние сессии.</p>
      {:else if view === 'unavailable'}
        <h1 id="access-heading">Вход в консоль</h1>
        <p class="notice error" role="status">Сервер сейчас недоступен. Попробуйте ещё раз.</p>
        <button onclick={refresh}>Повторить</button>
      {:else}
        <h1 id="access-heading">{expired ? 'Сессия истекла' : 'Вход в консоль'}</h1>
        <p>{expired ? 'Войдите снова, чтобы вернуться к работе.' : 'Используйте пароль администратора этой установки.'}</p>
        <form onsubmit={login} aria-busy={busy}>
          <div class="feedback" aria-live="polite">
            {#if busy}
              <p class="notice">Проверяем пароль.</p>
            {:else if message}
              <p class="notice" class:error={message !== 'Вы вышли из консоли.'}>{message}</p>
            {/if}
          </div>
          <label for="administrator-password">Пароль администратора</label>
          <input id="administrator-password" type="password" autocomplete="current-password"
            placeholder="Введите пароль" required bind:this={passwordField} bind:value={password} disabled={busy} />
          <button type="submit" disabled={busy}>{expired ? 'Войти снова' : 'Войти'}</button>
        </form>
      {/if}
    </section>
  </div>
{/if}
