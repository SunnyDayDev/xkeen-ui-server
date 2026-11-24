<script lang="ts">
  import { tick, untrack } from 'svelte';
  import { ExactSource, sourceLocation } from './exact-source';

  type Action = 'replace' | 'variables' | 'reset';
  type State = {
    routerId: string; name: string; templateId: string; acceptedVersion: string; activeGeneration: string; draftGeneration: string;
    elementId: string; section: string; mode: 'variables' | 'replacement'; hasOverride: boolean; source?: string;
  };
  type Reply = State & { state?: string; offset?: number; code?: string };
  let { id, elementId, active, sessionEpoch, csrf, signedOut, refreshSession, back }: {
    id: string; elementId: string; active: boolean; sessionEpoch: number; csrf: string; signedOut: () => void;
    refreshSession: () => void; back: (event: MouseEvent, path: string) => void;
  } = $props();

  const selection = $derived(new URLSearchParams({routerId:id,elementId}).toString());
  const backPath = $derived('/ui/routers/variables?' + selection);
  let snapshot = $state<State>();
  let visible = $state('');
  let authored = $state('');
  let message = $state('');
  let success = $state(false);
  let loading = $state(false);
  let saving = $state(false);
  let conflict = $state(false);
  let unavailable = $state(false);
  let invalid = $state(false);
  let confirmation = $state<'variables' | 'reset'>();
  let field = $state<HTMLTextAreaElement>();
  let operation: AbortController | undefined;
  let observedEpoch: number | undefined;
  let buffer = new ExactSource('');
  let previous: Parameters<ExactSource['replaceVisible']>[1];

  function apply(next: State, keep: boolean) {
    snapshot = next;
    if (!keep) {
      buffer = new ExactSource(next.source ?? '');
      syncText();
      previous = undefined;
    }
  }

  function syncText() {
    visible = buffer.display;
    authored = buffer.value;
  }

  function valid(result: Reply): boolean {
    return result.routerId === id && result.elementId === elementId &&
      (result.mode === 'variables' || (result.mode === 'replacement' && typeof result.source === 'string'));
  }

  function refused(result: Reply, status: number, write: boolean) {
    if (status === 401 && result.state === 'signed_out') {
      signedOut();
      return;
    }
    if (status === 403 && result.state === 'invalid_csrf') {
      message = 'Состояние сессии изменилось. Повторите сохранение после проверки.';
      refreshSession();
      return;
    }
    switch (result.state) {
      case 'router_or_element_unavailable':
        unavailable = true;
        message = snapshot ? 'Роутер или элемент недоступен. Ваш JSON остался в редакторе.' : 'Роутер недоступен в текущем обзоре.';
        break;
      case 'different_template':
        unavailable = true;
        message = 'У роутера есть черновик с другим шаблоном. Настройки остаются без изменений.';
        break;
      case 'draft_conflict':
      case 'replacement_mode':
        conflict = true;
        message = 'Настройки изменились. Ваш JSON остался в редакторе.';
        break;
      default:
        message = write ? 'Не удалось сохранить черновик. Ваш JSON остался в редакторе.' : 'Не удалось загрузить параметры. Повторите попытку.';
    }
  }

  async function request(action?: Action, keep = false) {
    if (!active || saving || loading || (action && (!snapshot || conflict || unavailable))) return;
    const controller = new AbortController();
    operation = controller;
    const epoch = sessionEpoch;
    loading = action === undefined;
    saving = action !== undefined;
    message = '';
    invalid = false;
    success = false;
    let caret: number | undefined;
    const current = snapshot;
    const candidate = buffer.value;
    try {
      const response = await fetch('/ui/v1/routers/replacement?' + selection,{
        cache:'no-store',credentials:'same-origin',redirect:'error',
        signal:AbortSignal.any([controller.signal,AbortSignal.timeout(10000)]),
        ...(action && current ? {
          method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},
          body:JSON.stringify({templateId:current.templateId,acceptedVersion:current.acceptedVersion,activeGeneration:current.activeGeneration,
            draftGeneration:current.draftGeneration,action,source:action === 'replace' ? candidate : undefined})
        } : {})
      });
      const result = await response.json() as Reply;
      if (operation !== controller || !active || sessionEpoch !== epoch) return;
      if (response.ok && valid(result)) {
        if (keep && current && (current.templateId !== result.templateId || current.section !== result.section)) {
          message = 'Источник элемента изменился. Ваш JSON остался в редакторе.';
          return;
        }
        apply(result,keep);
        conflict = false;
        unavailable = false;
        confirmation = undefined;
        if (action) {
          success = true;
          message = 'Черновик сохранён. Действующая конфигурация не изменилась.';
        }
      } else if (action === 'replace' && result.state === 'invalid_source') {
        invalid = true;
        const location = typeof result.offset === 'number' ? sourceLocation(candidate,result.offset) : undefined;
        caret = location?.visibleOffset;
        message = location ? `Строка ${location.line}, столбец ${location.column}. Исправьте синтаксис JSON.`
          : result.code === 'duplicate_json_key' ? 'Повторяющийся ключ JSON.' : 'Исправьте синтаксис JSON.';
      } else {
        refused(result,response.status,!!action);
      }
    } catch {
      if (operation !== controller || !active || sessionEpoch !== epoch) return;
      conflict ||= !!action;
      message = action ? 'Результат сохранения неизвестен. Перечитайте настройки.' : 'Не удалось загрузить параметры. Повторите попытку.';
    } finally {
      if (operation === controller) {
        operation = undefined;
        loading = false;
        saving = false;
        if (caret !== undefined && active && sessionEpoch === epoch) {
          await tick();
          field?.focus();
          field?.setSelectionRange(caret,caret);
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
        if (saving) {
          message = 'Результат сохранения неизвестен. Перечитайте настройки.';
          conflict = true;
        }
        saving = false;
        loading = false;
      }
      if (!ready || changed) confirmation = undefined;
      if (ready && !snapshot && !operation) void request();
    });
  });
  $effect(() => () => { operation?.abort(); operation = undefined; });

  function beforeInput(event: InputEvent) {
    const input = event.currentTarget as HTMLTextAreaElement;
    if (event.inputType === 'historyUndo' || event.inputType === 'historyRedo') {
      event.preventDefault();
      if (event.inputType === 'historyUndo') buffer.undo();
      else buffer.redo();
      syncText();
      input.value = visible;
      return;
    }
    previous = {start:input.selectionStart,end:input.selectionEnd,kind:event.inputType};
  }

  function update(event: Event) {
    buffer.replaceVisible((event.currentTarget as HTMLTextAreaElement).value,previous);
    syncText();
    previous = undefined;
  }

  function paste(event: ClipboardEvent) {
    if (!event.clipboardData?.types.includes('text/plain')) return;
    event.preventDefault();
    const input = event.currentTarget as HTMLTextAreaElement;
    const value = event.clipboardData.getData('text/plain');
    const caret = input.selectionStart + value.replace(/\r\n?/g,'\n').length;
    buffer.splice(input.selectionStart,input.selectionEnd,value);
    syncText();
    input.value = visible;
    input.setSelectionRange(caret,caret);
    previous = undefined;
  }

  function goBack(event: MouseEvent) {
    if (authored !== (snapshot?.source ?? '') && !window.confirm('Продолжить без несохранённого JSON?')) {
      event.preventDefault();
      return;
    }
    back(event,backPath);
  }
</script>

{#if snapshot}<p class="context"><bdi>{snapshot.name}</bdi> / <bdi>{snapshot.templateId}</bdi></p>{/if}
<h1>Свой JSON элемента</h1>
{#if loading && !snapshot}<p role="status">Загружаем настройки роутера…</p>{/if}
{#if snapshot}
  <p class="context">UUID <bdi>{snapshot.elementId}</bdi></p>
  <p>Режим черновика: {snapshot.mode === 'replacement' ? 'свой JSON' : 'только переменные'}</p>
  <form onsubmit={(event) => { event.preventDefault(); void request('replace'); }}>
    <label>JSON
      <textarea bind:this={field} value={visible} rows={8} spellcheck={false} aria-invalid={invalid} disabled={loading || saving || !!confirmation}
        onbeforeinput={beforeInput} oninput={update} onpaste={paste}></textarea>
    </label>
    {#if message}<p role={success ? 'status' : 'alert'} class:notice={!success} class:error={!success}>{message}</p>{/if}
    {#if confirmation === 'variables'}
      <p>Вернуть режим переменных? Собственный JSON будет удалён из черновика. Персональные значения сохранятся.</p>
    {:else if confirmation === 'reset'}
      <p>Сбросить переопределение? Собственный JSON и персональные значения будут удалены.</p>
    {:else if snapshot.mode === 'variables' && !message}
      <p>Введите содержимое блока целиком.</p>
    {/if}
    <div class="actions">
      {#if conflict}
        <button type="button" disabled={loading || saving} onclick={() => request(undefined,true)}>Перечитать настройки</button>
      {:else if confirmation}
        <button type="button" disabled={saving} onclick={() => request(confirmation)}>{confirmation === 'variables' ? 'Вернуть переменные' : 'Сбросить'}</button>
        <button type="button" class="secondary" disabled={saving} onclick={() => { confirmation = undefined; }}>Отмена</button>
      {:else if !unavailable}
        <button type="submit" disabled={!active || loading || saving || authored === (snapshot.source ?? '')}>Сохранить черновик</button>
        {#if snapshot.mode === 'replacement'}
          <button type="button" class="secondary" disabled={!active || loading || saving} onclick={() => { confirmation = 'variables'; }}>Вернуть переменные</button>
        {/if}
        {#if snapshot.hasOverride}
          <button type="button" class="secondary" disabled={!active || loading || saving} onclick={() => { confirmation = 'reset'; }}>Сбросить переопределение</button>
        {/if}
      {/if}
      {#if !confirmation}<a href={backPath} onclick={goBack}>К переменным</a>{/if}
    </div>
  </form>
{:else}
  {#if message}<p role="alert" class="notice error">{message}</p>{/if}
  <div class="actions">
    {#if message && !unavailable}<button disabled={loading} onclick={() => request()}>Повторить</button>{/if}
    <a href={backPath} onclick={goBack}>К переменным</a>
  </div>
{/if}

<style>
  form { display:grid; gap:1rem; }
  .context { color:var(--muted); font-size:.86rem; overflow-wrap:anywhere; }
  label { display:grid; gap:.4rem; }
  textarea { width:100%; min-width:0; background:var(--surface); color:var(--ink); padding:.75rem; border:1px solid var(--line); border-radius:var(--radius); font-family:ui-monospace,monospace; resize:vertical; }
  textarea[aria-invalid=true] { border-color:#b42318; }
  .actions { display:flex; flex-wrap:wrap; align-items:center; gap:.75rem; }
  .secondary { background:var(--surface); color:var(--ink); border:1px solid var(--line); }
</style>
