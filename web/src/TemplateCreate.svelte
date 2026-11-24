<script lang="ts">
  import { onDestroy, tick, untrack } from 'svelte';

  type Props = {
    canSubmit: boolean;
    sessionEpoch: number;
    csrf: string;
    changed: () => void;
    signedOut: () => void;
    refreshSession: () => void;
  };

  let { canSubmit, sessionEpoch, csrf, changed, signedOut, refreshSession }: Props = $props();
  let open = $state(false);
  let id = $state('');
  let busy = $state(false);
  let message = $state('');
  let created = $state<string>();
  let field = $state<HTMLInputElement>();
  let opener = $state<HTMLButtonElement>();
  let pending: { controller: AbortController; epoch: number } | undefined;

  const uncertain = 'Не удалось подтвердить создание. Проверьте список или повторите.';

  $effect(() => {
    const currentEpoch = sessionEpoch;
    const available = canSubmit;
    if (pending && (!available || pending.epoch !== currentEpoch)) {
      pending.controller.abort();
      pending = undefined;
      busy = false;
      message = uncertain;
      if (available) untrack(changed);
    }
  });

  onDestroy(() => {
    pending?.controller.abort();
    pending = undefined;
  });

  async function showForm() {
    open = true;
    created = undefined;
    await tick();
    field?.focus();
  }

  async function closeForm() {
    open = false;
    id = '';
    message = '';
    await tick();
    opener?.focus();
  }

  async function create(event: SubmitEvent) {
    event.preventDefault();
    if (!canSubmit || pending || id === '') return;
    const submitted = id;
    const request = { controller: new AbortController(), epoch: sessionEpoch };
    pending = request;
    busy = true;
    message = '';

    let status: number | undefined;
    let reply: { id?: string; version?: string; state?: string } | undefined;
    try {
      const response = await fetch('/ui/v1/templates', {
        method: 'POST',
        headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
        body: JSON.stringify({ id: submitted }),
        credentials: 'same-origin', cache: 'no-store', redirect: 'error',
        signal: AbortSignal.any([request.controller.signal, AbortSignal.timeout(10000)])
      });
      status = response.status;
      reply = await response.json();
    } catch {
      // The write may already have reached the server.
    }
    if (!canSubmit || request.epoch !== sessionEpoch || pending !== request) return;
    pending = undefined;
    busy = false;

    if (status === 201 && reply?.id === submitted && reply.version === '1') {
      created = submitted;
      changed();
      await closeForm();
    } else if (status === 401 && reply?.state === 'signed_out') {
      field?.focus();
      signedOut();
    } else if (status === 409 && reply?.state === 'template_exists') {
      message = 'Шаблон с таким идентификатором уже существует.';
    } else if (status === 403 && reply?.state === 'invalid_csrf') {
      message = 'Состояние сессии изменилось. Проверьте ввод и повторите.';
      refreshSession();
    } else if (status === 413 && reply?.state === 'request_too_large') {
      message = 'Слишком длинный идентификатор.';
    } else if (status === 400 && reply?.state === 'invalid_request') {
      message = 'Проверьте идентификатор шаблона.';
    } else {
      message = uncertain;
      changed();
    }
  }
</script>

<div class="template-toolbar">
  <h1>Шаблоны</h1>
  {#if !open}
    <button bind:this={opener} onclick={showForm} disabled={!canSubmit}>Создать шаблон</button>
  {/if}
</div>

{#if open}
  <form class="template-form" aria-label="Создание шаблона" aria-busy={busy} onsubmit={create}>
    <div class="template-field">
      <label for="new-template-id">Идентификатор шаблона</label>
      <input id="new-template-id" type="text" required autocomplete="off" spellcheck="false"
        aria-describedby="new-template-hint" bind:this={field} bind:value={id} readonly={busy} />
      <p id="new-template-hint">ID сохраняется как введён.</p>
    </div>
    {#if message}
      <p class="form-error" role="alert">{message}</p>
    {/if}
    <div class="template-actions">
      <button type="submit" disabled={busy || !canSubmit}>{busy ? 'Создаём…' : 'Создать'}</button>
      <button type="button" class="secondary" onclick={closeForm} disabled={busy}>Отмена</button>
    </div>
  </form>
{/if}

{#if created !== undefined}
  <p class="template-confirmation" role="status">Шаблон создан: <bdi>{created}</bdi></p>
{/if}

<style>
  .template-toolbar, .template-actions { display: flex; flex-wrap: wrap; align-items: center; gap: 12px; }
  .template-toolbar { justify-content: space-between; }
  .template-form {
    margin-top: 16px; padding: 18px; background: var(--surface);
    border: 1px solid var(--line); border-radius: var(--radius); gap: 12px;
  }
  .template-field { display: grid; gap: 8px; max-width: 420px; }
  .template-field p { font-size: .8rem; }
  .template-actions button { width: auto; margin: 0; }
  .secondary { background: var(--surface); color: var(--ink); }
  .form-error { color: var(--error); }
  .template-confirmation { margin-top: 12px; color: var(--ink); }
  .template-confirmation bdi { white-space: pre-wrap; overflow-wrap: anywhere; }
  @media (max-width: 520px) {
    .template-form { padding: 14px; }
  }
</style>
