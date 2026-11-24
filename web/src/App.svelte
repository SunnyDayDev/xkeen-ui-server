<script lang="ts">
  import { onMount, tick } from 'svelte';
  import ExternalSettings from './ExternalSettings.svelte';
  let settings = $state(false);
  import { sessionRequest, type Session } from './session';

  let view = $state<'initial' | 'setup_required' | 'signed_out' | 'active' | 'unavailable'>('initial');
  let message = $state('');
  let opened = $state(false);
  let expired = $state(false);
  let busy = $state(false);
  let password = $state('');
  let csrf = $state('');
  let passwordField = $state<HTMLInputElement>();
  let workspace = $state<HTMLElement>();
  let reading = false;
  let mutation = 0;
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

  async function refresh() {
    if (busy || reading || document.hidden) return;
    reading = true;
    const generation = mutation;
    const reply = await sessionRequest('session');
    reading = false;
    if (generation !== mutation || busy) return;
    if (reply.state === 'active' && !expired) {
      csrf = reply.csrf ?? '';
      if (view !== 'active') {
        view = 'active';
        opened = true;
        await focusWorkspace();
      }
    } else if (reply.state === 'signed_out' || reply.state === 'setup_required') {
      if (view === 'active') {
        const focused = document.activeElement;
        retainedFocus = focused instanceof HTMLElement && workspace?.contains(focused) ? focused : undefined;
        expired = true;
        message = '';
      }
      csrf = '';
      const changed = view !== reply.state;
      view = reply.state;
      if (changed && view === 'signed_out') await focusPassword();
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
      settings = false;
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
    check();
    const timer = setInterval(check, 15_000);
    window.addEventListener('focus', check);
    document.addEventListener('visibilitychange', check);
    return () => {
      clearInterval(timer);
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
        <a href="/ui/" aria-current={settings ? undefined : "page"} onclick={(event) => {
          if (!event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey) {
            event.preventDefault();
            settings = false;
            void focusWorkspace();
          }
        }}>Обзор</a>
        <a href="#settings" aria-current={settings ? "page" : undefined} onclick={(event) => {event.preventDefault(); settings = true; void focusWorkspace();}}>Настройки</a>
      </nav>
      <button onclick={logout} disabled={busy}>Выйти</button>
    </header>
    <main bind:this={workspace} tabindex="-1">
      {#if settings}
        {#if view === 'active'}{#key csrf}<ExternalSettings {csrf} refreshSession={refresh} />{/key}{/if}
      {:else}<h1>Обзор</h1>{/if}
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
