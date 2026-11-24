<script lang="ts">
  import { onDestroy, tick } from 'svelte';

  type Basis = {routerId: string; activeGeneration: string; epoch: string; visible: boolean};
  type Stage = 'idle' | 'reading' | 'confirm' | 'posting' | 'stale' | 'unknown' | 'visible' | 'error';
  let {id, name, csrf, unconfirmed, setUnconfirmed, onHidden, refreshList, signedOut, refreshSession}: {
    id: string; name: string; csrf: string; unconfirmed: boolean;
    setUnconfirmed: (pending: boolean) => void; onHidden: (confirmed: boolean) => void;
    refreshList: () => void; signedOut: () => void; refreshSession: () => void;
  } = $props();

  const confirmationId = $props.id();
  let stage = $state<Stage>('idle');
  let basis = $state<Basis>();
  let offer = $state<HTMLButtonElement>();
  let cancel = $state<HTMLButtonElement>();
  let inspect = $state<HTMLButtonElement>();
  let request: AbortController | undefined;
  let alive = true;

  const zeroOrPositive = /^(0|[1-9][0-9]{0,18})$/;
  function valid(value: Basis) {
    return value?.routerId === id && typeof value.visible === 'boolean' &&
      typeof value.activeGeneration === 'string' && /^[1-9][0-9]{0,18}$/.test(value.activeGeneration) &&
      typeof value.epoch === 'string' && zeroOrPositive.test(value.epoch);
  }

  async function restoreFocus(started: HTMLButtonElement, target: () => HTMLButtonElement | undefined) {
    await tick();
    if (alive && (document.activeElement === started || document.activeElement === document.body)) target()?.focus();
  }

  async function run(action: 'prepare' | 'inspect' | 'hide', started: HTMLButtonElement) {
    if (request || !alive || (action === 'hide' && !basis)) return;
    const controller = new AbortController();
    request = controller;
    const posting = action === 'hide';
    const recoveryRead = action === 'inspect';
    let target: (() => HTMLButtonElement | undefined) | undefined;
    if (posting) setUnconfirmed(true);
    stage = posting ? 'posting' : 'reading';
    try {
      const response = await fetch('/ui/v1/routers/visibility?' + new URLSearchParams({routerId:id}), {
        method: posting ? 'POST' : 'GET', cache:'no-store', credentials:'same-origin', redirect:'error',
        signal: AbortSignal.any([controller.signal,AbortSignal.timeout(8000)]),
        headers: posting ? {'Content-Type':'application/json','X-CSRF-Token':csrf} : {},
        body: posting ? JSON.stringify({activeGeneration:basis?.activeGeneration,epoch:basis?.epoch,confirm:true}) : undefined,
      });
      if (!alive) return;
      if (response.status === 401 || response.status === 403) {
        if (posting) setUnconfirmed(false);
        stage = recoveryRead && unconfirmed ? 'unknown' : 'idle';
        if (response.status === 401) signedOut();
        else refreshSession();
        return;
      }
      if (response.status === 409 && posting) {
        setUnconfirmed(false);
        basis = undefined;
        stage = 'stale';
        target = () => inspect;
        return;
      }
      if (!response.ok) throw new Error('visibility response unavailable');
      const value = await response.json() as Basis;
      if (!alive) return;
      if (!valid(value) || (posting && value.visible)) throw new Error('invalid visibility response');
      setUnconfirmed(false);
      if (!value.visible) {
        onHidden(posting);
        return;
      }
      if (recoveryRead) {
        basis = undefined;
        stage = 'visible';
        refreshList();
        target = () => offer;
      } else {
        basis = value;
        stage = 'confirm';
        target = () => cancel;
      }
    } catch {
      if (alive) {
        stage = posting || unconfirmed ? 'unknown' : 'error';
        target = stage === 'unknown' ? () => inspect : () => offer;
      }
    } finally {
      if (request === controller) request = undefined;
      if (target) await restoreFocus(started,target);
    }
  }

  function dismiss(event: MouseEvent & {currentTarget: HTMLButtonElement}) {
    basis = undefined;
    stage = 'idle';
    void restoreFocus(event.currentTarget, () => offer);
  }

  onDestroy(() => {
    alive = false;
    request?.abort();
  });
</script>

{#if stage === 'confirm'}
  <div class="visibility-actions">
    <strong>Скрыть {name || id} из списка?</strong>
    <p id={confirmationId}>Роутер появится снова после следующего допустимого контакта. Ключ, настройки и история сохранятся.</p>
    <button aria-describedby={confirmationId} onclick={event => run('hide',event.currentTarget)}>Скрыть роутер</button>
    <button class="secondary" aria-describedby={confirmationId} bind:this={cancel} onclick={dismiss}>Отмена</button>
  </div>
{:else if stage === 'posting' || stage === 'reading'}
  <p role="status">{stage === 'posting' ? 'Скрываем роутер…' : 'Проверяем видимость…'}</p>
{:else if unconfirmed || stage === 'unknown' || stage === 'stale'}
  <div class="visibility-actions">
    <p role="status">{stage === 'stale'
      ? 'Состояние изменилось. Проверьте текущую видимость перед подтверждением.'
      : 'Не удалось подтвердить результат. Проверьте текущее состояние.'}</p>
    <button class="secondary" bind:this={inspect} onclick={event => run('inspect',event.currentTarget)}>Проверить состояние</button>
  </div>
{:else if name || stage === 'visible'}
  <div class="visibility-actions">
    {#if stage === 'visible'}<p role="status">Роутер сейчас видим.</p>{/if}
    {#if stage === 'error'}<p role="status">Не удалось проверить видимость роутера.</p>{/if}
    <button class="secondary" bind:this={offer} onclick={event => run('prepare',event.currentTarget)}>Скрыть из списка</button>
  </div>
{/if}

<style>
  .visibility-actions { display: grid; gap: .55rem; justify-items: start; }
  .visibility-actions p { margin: 0; overflow-wrap: anywhere; }
</style>
