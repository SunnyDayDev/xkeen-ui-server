<script lang="ts">
  import { onDestroy, tick } from 'svelte';

  type Basis = {routerId: string; name: string; activeGeneration: string; version: string};
  type Retained = {text: string; unknown: boolean};
  type Phase = 'idle' | 'reading' | 'editing' | 'saving' | 'review';
  let {id, csrf, retained, retain, onSaved, signedOut, refreshSession}: {
    id: string; csrf: string; retained?: Retained; retain: (value?: Retained) => void;
    onSaved: (name: string) => void; signedOut: () => void; refreshSession: () => void;
  } = $props();

  let phase = $state<Phase>('idle');
  let basis = $state<Basis>();
  let message = $state('');
  let current = $state('');
  let editButton = $state<HTMLButtonElement>();
  let input = $state<HTMLInputElement>();
  let reviewButton = $state<HTMLButtonElement>();
  const inputId = $props.id();
  let activeRequest: AbortController | undefined;
  let alive = true;

  function valid(value: Basis) {
    return value?.routerId === id && typeof value.name === 'string' &&
      typeof value.activeGeneration === 'string' && /^[1-9][0-9]{0,18}$/.test(value.activeGeneration) &&
      typeof value.version === 'string' && /^(0|[1-9][0-9]{0,18})$/.test(value.version);
  }

  async function restoreFocus(started: HTMLElement, target: () => HTMLElement | undefined) {
    await tick();
    if (alive && (document.activeElement === started || document.activeElement === document.body)) target()?.focus();
  }

  function cancel(event: MouseEvent & {currentTarget: HTMLButtonElement}) {
    retain();
    basis = undefined;
    current = '';
    message = '';
    phase = 'idle';
    void restoreFocus(event.currentTarget,()=>editButton);
  }

  async function run(action: 'open' | 'recheck' | 'save', started: HTMLElement) {
    if (activeRequest || !alive || (action === 'save' && (!basis || !retained))) return;
    const controller = new AbortController();
    activeRequest = controller;
    const saving = action === 'save';
    const keep = retained?.text;
    let target: (() => HTMLElement | undefined) | undefined;
    if (saving && keep !== undefined) retain({text:keep,unknown:true});
    phase = saving ? 'saving' : 'reading';
    message = '';
    current = '';
    try {
      const response = await fetch('/ui/v1/routers/name?' + new URLSearchParams({routerId:id}), {
        method: saving ? 'POST' : 'GET', cache:'no-store', credentials:'same-origin', redirect:'error',
        signal: AbortSignal.any([controller.signal,AbortSignal.timeout(8000)]),
        headers: saving ? {'Content-Type':'application/json','X-CSRF-Token':csrf} : {},
        body: saving ? JSON.stringify({activeGeneration:basis?.activeGeneration,version:basis?.version,name:keep}) : undefined,
      });
      if (!alive) return;
      if (response.status === 401 || response.status === 403) {
        if (saving && keep !== undefined) retain({text:keep,unknown:false});
        phase = keep !== undefined ? 'review' : 'idle';
        if (response.status === 401) signedOut();
        else refreshSession();
        return;
      }
      if (saving && (response.status === 400 || response.status === 413)) {
        retain({text:keep ?? '',unknown:false});
        phase = 'editing';
        message = 'Введите допустимое отображаемое имя.';
        target = () => input;
        return;
      }
      if (saving && response.status === 409) {
        retain({text:keep ?? '',unknown:false});
        phase = 'review';
        basis = undefined;
        message = 'Имя изменилось. Введённый текст сохранён.';
        target = () => reviewButton;
        return;
      }
      if (response.status === 404) {
        if (saving && keep !== undefined) retain({text:keep,unknown:false});
        basis = undefined;
        phase = keep !== undefined ? 'review' : 'idle';
        message = 'Роутер недоступен в текущем обзоре.';
        target = keep !== undefined ? () => reviewButton : () => editButton;
        return;
      }
      if (!response.ok) throw new Error('router name unavailable');
      const value = await response.json() as Basis;
      if (!alive) return;
      if (!valid(value)) throw new Error('invalid router name response');
      if (saving) {
        if (value.name !== keep) throw new Error('different router name');
        retain();
        basis = undefined;
        phase = 'idle';
        message = 'Имя сохранено.';
        onSaved(value.name);
        target = () => editButton;
      } else {
        basis = value;
        retain({text:keep ?? value.name,unknown:false});
        phase = 'editing';
        if (action === 'recheck') current = value.name;
        target = () => input;
      }
    } catch {
      if (alive) {
        phase = saving || keep !== undefined ? 'review' : 'idle';
        message = saving
          ? 'Не удалось подтвердить сохранение. Проверьте текущее имя.'
          : 'Не удалось проверить текущее имя.';
        target = phase === 'review' ? () => reviewButton : () => editButton;
      }
    } finally {
      if (activeRequest === controller) activeRequest = undefined;
      if (target) await restoreFocus(started,target);
    }
  }

  onDestroy(() => {
    alive = false;
    activeRequest?.abort();
  });
</script>

<div class="name-editor">
  {#if message}<p role="status">{message}</p>{/if}
  {#if phase === 'reading' || phase === 'saving'}
    <p role="status">{phase === 'saving' ? 'Сохраняем имя…' : 'Проверяем текущее имя…'}</p>
  {:else if phase === 'editing' || phase === 'review' || retained}
    {#if retained?.unknown && !message}<p role="status">Не удалось подтвердить сохранение. Проверьте текущее имя.</p>{/if}
    {#if current}<p>Текущее имя: <bdi>{current}</bdi></p>{/if}
    <label for={inputId}>Отображаемое имя</label>
    <input id={inputId} bind:this={input} value={retained?.text ?? ''}
      oninput={event => retain({text:event.currentTarget.value,unknown:retained?.unknown ?? false})} />
    {#if phase === 'editing'}
      <button onclick={event => run('save',event.currentTarget)}>Сохранить имя</button>
    {:else}
      <button class="secondary" bind:this={reviewButton} onclick={event => run('recheck',event.currentTarget)}>Проверить текущее имя</button>
    {/if}
    <button class="secondary" onclick={cancel}>Отмена</button>
  {:else}
    <button class="secondary" bind:this={editButton} onclick={event => run('open',event.currentTarget)}>Изменить имя</button>
  {/if}
</div>

<style>
  .name-editor { display: grid; gap: .55rem; justify-items: start; }
  .name-editor p { margin: 0; overflow-wrap: anywhere; }
  .name-editor input { box-sizing: border-box; width: 100%; min-width: 0; }
  button.secondary { background: white; color: var(--ink); border: 1px solid var(--line); }
</style>
