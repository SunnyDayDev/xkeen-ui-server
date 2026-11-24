<script lang="ts">
  import { onMount } from 'svelte';

  type Phase = 'missing' | 'pending' | 'expired' | 'registered' | 'revoked';
  type Contact = 'unobserved' | 'online' | 'offline';
  type Snapshot = {
    routerId: string; name: string; origin: string; phase: Phase; epoch: string;
    desiredRevision: string; expires?: string; contact: Contact; appliedRevision?: string;
    runtime?: 'unknown' | 'verified';
  };
  type Receipt = { epoch: string; expires: string; invitation: string };
  type Props = { id: string; csrf: string; signedOut: () => void; refreshSession: () => void };
  let {id, csrf, signedOut, refreshSession}: Props = $props();

  let snapshot = $state<Snapshot>();
  let secret = $state<Receipt>();
  let message = $state('');
  let awaitingRead = $state(false);
  let confirming = $state(false);
  let working = $state(false);
  let copied = $state(false);
  let pending: AbortController | undefined;
  let expiration: ReturnType<typeof setTimeout> | undefined;
  let presentation = 0;
  let deadline = 0;

  const date = new Intl.DateTimeFormat('ru', {
    dateStyle: 'medium', timeStyle: 'short', timeZone: 'UTC',
  });
  const endpoint = () => '/ui/v1/routers/enroll?' + new URLSearchParams({routerId:id});
  const contactLabel: Record<Contact,string> = {
    unobserved:'ожидается первый контакт', online:'на связи', offline:'нет связи',
  };

  function dismiss() {
    presentation++;
    clearTimeout(expiration);
    secret = undefined;
    copied = false;
  }

  function expire() {
    dismiss();
    void read();
  }

  async function request(method: 'GET' | 'POST', payload?: object) {
    const controller = new AbortController();
    pending = controller;
    let reply: {status:number; body:unknown; responseTime:number} | undefined;
    try {
      const response = await fetch(endpoint(), {
        method, signal: AbortSignal.any([controller.signal,AbortSignal.timeout(8000)]), cache: 'no-store',
        ...(payload ? {headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:JSON.stringify(payload)} : {}),
      });
      reply = {status:response.status,body:await response.json().catch(()=>undefined),
        responseTime: Date.parse(response.headers.get('Date') ?? '')};
    } catch {
      // A transport failure can follow a committed issuance.
    }
    if (pending !== controller || controller.signal.aborted) return;
    if (reply?.status === 401) {
      dismiss();
      signedOut();
      return;
    }
    if (reply?.status === 403 && (reply.body as {state?:string} | undefined)?.state === 'invalid_csrf') {
      dismiss();
      refreshSession();
    }
    return reply;
  }

  async function read(explicit = false) {
    if (working || pending || document.hidden || (awaitingRead && !explicit)) return;
    const promise = request('GET');
    const active = pending;
    const reply = await promise;
    if (pending !== active) return;
    pending = undefined;
    if (reply?.status !== 200 || !reply.body) {
      dismiss();
      message = reply?.status === 404 ? 'Роутер ещё не подготовлен.' : 'Не удалось прочитать состояние.';
      awaitingRead = true;
      return;
    }
    const next = reply.body as Snapshot;
    if (next.routerId !== id) {
      dismiss();
      snapshot = undefined;
      message = 'Не удалось прочитать состояние.';
      awaitingRead = true;
      return;
    }
    if (secret && (next.phase !== 'pending' || next.epoch !== secret.epoch || performance.now() >= deadline)) dismiss();
    if (confirming && next.epoch !== snapshot?.epoch) {
      confirming = false;
      message = 'Состояние изменилось. Перечитайте параметры.';
      awaitingRead = true;
    } else {
      message = '';
      awaitingRead = false;
    }
    snapshot = next;
  }

  async function issue(action: 'issue' | 'replace') {
    if (!snapshot?.origin || working || awaitingRead || document.hidden) return;
    pending?.abort();
    dismiss();
    const shownIn = presentation;
    confirming = false;
    working = true;
    const requestStarted = performance.now();
    const promise = request('POST',{epoch:snapshot.epoch,desiredRevision:snapshot.desiredRevision,action});
    const active = pending;
    const reply = await promise;
    if (pending !== active) return;
    pending = undefined;
    working = false;
    if (reply?.status === 201 && reply.body) {
      const receipt = reply.body as Receipt;
      // HTTP Date is rounded to seconds. Count the entire request against the lifetime.
      deadline = requestStarted + Date.parse(receipt.expires) - reply.responseTime - 1000;
      const remaining = deadline - performance.now();
      if (shownIn === presentation && !document.hidden && Number.isFinite(remaining) && remaining > 0) {
        secret = receipt;
        expiration = setTimeout(expire,remaining);
      }
      snapshot = {...snapshot, epoch:receipt.epoch, expires:receipt.expires, phase:'pending'};
      if (!secret) void read();
      return;
    }
    awaitingRead = true;
    message = reply?.status === 409
      ? 'Состояние изменилось. Перечитайте параметры.'
      : 'Не удалось подтвердить выдачу.';
  }

  async function copyInvitation() {
    if (!secret || performance.now() >= deadline || document.hidden) {
      expire();
      return;
    }
    const current = secret.invitation;
    try {
      await navigator.clipboard.writeText(current);
      if (secret?.invitation === current) copied = true;
    } catch {
      if (secret?.invitation === current) message = 'Не удалось скопировать. Выделите текст приглашения.';
    }
  }

  onMount(() => {
    void read();
    const timer = setInterval(() => { void read(); }, 2_500);
    const visibility = () => {
      if (document.hidden) dismiss();
      else void read();
    };
    const resume = (event: PageTransitionEvent) => {
      if (event.persisted) {
        dismiss();
        void read();
      }
    };
    window.addEventListener('pagehide', dismiss);
    window.addEventListener('pageshow', resume);
    document.addEventListener('visibilitychange',visibility);
    return () => {
      clearInterval(timer);
      clearTimeout(expiration);
      pending?.abort();
      pending = undefined;
      window.removeEventListener('pagehide',dismiss);
      window.removeEventListener('pageshow',resume);
      document.removeEventListener('visibilitychange',visibility);
    };
  });
</script>

<h1>Подключение агента</h1>
{#if snapshot}
  <section class="enrollment-card">
    <strong>{snapshot.name || snapshot.routerId}</strong>
    <p>Адрес сервера</p>
    {#if snapshot.origin}<p><bdi>{snapshot.origin}</bdi></p>
    {:else}<p class="notice error">Адрес агента не настроен.</p>{/if}
  </section>
  <section class="enrollment-card">
    {#if secret}
      <label for="enrollment-token">Одноразовое приглашение</label>
      <textarea id="enrollment-token" readonly value={secret.invitation} rows="2"></textarea>
      <p>Действует до {date.format(new Date(secret.expires))} UTC.</p>
      <p>Сохраните приглашение в приватный файл <code>invitation</code> (0600).</p>
      <div class="enrollment-actions">
        <button onclick={copyInvitation}>Скопировать</button>
        <button class="secondary" onclick={dismiss}>Скрыть</button>
      </div>
      {#if copied}<p role="status">Приглашение скопировано.</p>{/if}
    {:else if working}
      <button disabled>Выдаём приглашение…</button>
    {:else if confirming}
      <strong>Заменить приглашение?</strong>
      <p>Прежнее приглашение перестанет действовать.</p>
      <div class="enrollment-actions">
        <button onclick={()=>{ void issue('replace'); }}>Заменить</button>
        <button class="secondary" onclick={()=>{ confirming=false; }}>Отмена</button>
      </div>
    {:else if awaitingRead}
      <button onclick={()=>{ void read(true); }}>Проверить состояние</button>
    {:else if snapshot.phase === 'registered'}
      <p>Агент зарегистрирован.</p>
    {:else if snapshot.phase === 'revoked'}
      <p>Рабочий ключ отозван.</p>
    {:else if snapshot.origin}
      {#if snapshot.phase === 'missing'}
        <button onclick={()=>{ void issue('issue'); }}>Выдать приглашение</button>
      {:else}
        {#if snapshot.phase === 'expired'}<p>Срок приглашения истёк.</p>
        {:else}
          <p>Приглашение действует.</p>
          {#if snapshot.expires}<p>Действует до {date.format(new Date(snapshot.expires))} UTC.</p>{/if}
        {/if}
        <button class="secondary" onclick={()=>{ confirming=true; }}>Заменить приглашение</button>
      {/if}
    {/if}
    {#if message}<p role="alert">{message}</p>{/if}
  </section>
  <section class="enrollment-card">
    <p>Связь: {contactLabel[snapshot.contact]}</p>
    <p>Желаемая ревизия {snapshot.desiredRevision}</p>
    {#if snapshot.appliedRevision}
      <p>Последняя подтверждённая ревизия {snapshot.appliedRevision}</p>
    {:else}
      <p>Применение: нет подтверждения</p>
    {/if}
    {#if snapshot.runtime === 'unknown'}<p>Текущее состояние Xray неизвестно.</p>{/if}
  </section>
{:else}
  <section class="enrollment-card">
    {#if message}
      <p role="alert">{message}</p>
      <button onclick={()=>{ void read(true); }}>Проверить состояние</button>
    {:else}
      <p role="status">Загружаем параметры подключения…</p>
    {/if}
  </section>
{/if}

<style>
  .enrollment-card {
    display:grid; gap:.65rem; max-width:42rem; margin-bottom:1rem; padding:1rem;
    border:1px solid var(--line); border-radius:.5rem; background:var(--surface);
    overflow-wrap:anywhere;
  }
  .enrollment-card p { margin:0; }
  .enrollment-card button { justify-self:start; width:auto; margin:0; }
  .enrollment-actions { display:flex; flex-wrap:wrap; gap:.5rem; }
  textarea {
    width:100%; min-width:0; font:inherit; color:var(--ink); background:var(--surface);
    border:1px solid var(--line); border-radius:.4rem; padding:.5rem;
  }
</style>
