<script lang="ts">
  import { tick, untrack } from 'svelte';
  import { ExactSource, sourceLocation } from './exact-source';

  type Rule = { id: string; label: string; origin: 'shared' | 'local'; excluded: boolean; disabled: boolean };
  type State = { routerId: string; name: string; templateId: string; acceptedVersion: string; activeGeneration: string; draftGeneration: string; rules: Rule[] };
  type Reply = State & { state?: string; code?: string; offset?: number };
  let { id, active, sessionEpoch, csrf, signedOut, refreshSession, back }: {
    id: string; active: boolean; sessionEpoch: number; csrf: string; signedOut: () => void;
    refreshSession: () => void; back: (event: MouseEvent, path: string) => void;
  } = $props();

  const query = $derived(new URLSearchParams({routerId:id}).toString());
  const backPath = $derived('/ui/?' + query);
  let snapshot = $state<State>();
  let adding = $state(false);
  let after = $state<string | undefined>('');
  let source = $state('');
  let message = $state('');
  let success = $state(false);
  let loading = $state(false);
  let saving = $state(false);
  let conflict = $state(false);
  let unavailable = $state(false);
  let invalid = $state(false);
  let editor = $state<HTMLTextAreaElement>();
  let placement = $state<HTMLSelectElement>();
  let addButton = $state<HTMLButtonElement>();
  let operation: AbortController | undefined;
  let observedEpoch: number | undefined;
  let buffer = new ExactSource('');
  let previous: Parameters<ExactSource['replaceVisible']>[1];

  function clearDraft() {
    buffer = new ExactSource('');
    source = buffer.display;
    previous = undefined;
    after = '';
    conflict = false;
    invalid = false;
  }

  async function begin() {
    clearDraft();
    message = '';
    success = false;
    adding = true;
    await tick();
    placement?.focus();
  }

  async function cancel() {
    if (saving || loading) return;
    clearDraft();
    message = '';
    adding = false;
    await tick();
    addButton?.focus();
  }

  function refuse(result: Reply, status: number, write: boolean) {
    if (status === 401 && result.state === 'signed_out') {
      signedOut();
      return;
    }
    if (status === 403 && result.state === 'invalid_csrf') {
      message = 'Состояние сессии изменилось. Повторите запись после проверки.';
      refreshSession();
      return;
    }
    switch (result.state) {
      case 'draft_conflict':
        conflict = true;
        message = 'Порядок изменился. Ваш JSON остался в форме.';
        break;
      case 'order_unavailable':
        unavailable = true;
        message = 'Не удалось подтвердить полный порядок. Перечитайте текущие настройки.';
        break;
      case 'router_or_element_unavailable':
        unavailable = true;
        message = 'Роутер недоступен в текущем обзоре.';
        break;
      case 'different_template':
        unavailable = true;
        message = 'У роутера есть черновик с другим шаблоном.';
        break;
      default:
        message = write ? 'Не удалось добавить правило. Ваш JSON остался в форме.' : 'Не удалось загрузить правила.';
    }
  }

  async function request(write = false) {
    const current = snapshot;
    if (!active || saving || loading || (write && (!adding || !current || conflict || unavailable || after === undefined))) return;
    const controller = new AbortController();
    operation = controller;
    const epoch = sessionEpoch;
    loading = !write;
    saving = write;
    message = '';
    invalid = false;
    success = false;
    let caret: number | undefined;
    const candidate = buffer.value;
    try {
      const response = await fetch('/ui/v1/routers/rules?' + query,{
        cache:'no-store',credentials:'same-origin',redirect:'error',
        signal:AbortSignal.any([controller.signal,AbortSignal.timeout(10000)]),
        ...(write && current ? {
          method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},
          body:JSON.stringify({templateId:current.templateId,acceptedVersion:current.acceptedVersion,activeGeneration:current.activeGeneration,
            draftGeneration:current.draftGeneration,afterId:after,source:candidate})
        } : {})
      });
      const result = await response.json() as Reply;
      if (operation !== controller || !active || sessionEpoch !== epoch) return;
      if (response.ok && result.routerId === id && Array.isArray(result.rules)) {
        if (adding && current?.templateId !== result.templateId) {
          conflict = true;
          message = 'Шаблон роутера изменился. Ваш JSON остался в форме.';
          return;
        }
        snapshot = result;
        conflict = false;
        unavailable = false;
        if (write) {
          adding = false;
          clearDraft();
          success = true;
          message = 'Черновик сохранён. Локальное правило добавлено.';
        } else if (adding && after && !result.rules.some(rule => rule.id === after)) {
          after = undefined;
          message = 'Выбранное правило отсутствует. Укажите новое место вставки.';
        }
      } else if (write && result.state === 'invalid_source') {
        invalid = true;
        const location = typeof result.offset === 'number' ? sourceLocation(candidate,result.offset) : undefined;
        caret = location?.visibleOffset;
        message = location ? `Строка ${location.line}, столбец ${location.column}. Исправьте синтаксис JSON.`
          : result.code === 'duplicate_json_key' ? 'Повторяющийся ключ JSON.' : 'Исправьте синтаксис JSON.';
      } else {
        refuse(result,response.status,write);
      }
    } catch {
      if (operation !== controller || !active || sessionEpoch !== epoch) return;
      conflict ||= write;
      message = write ? 'Результат сохранения неизвестен. Перечитайте настройки.' : 'Не удалось загрузить правила.';
    } finally {
      if (operation === controller) {
        operation = undefined;
        loading = false;
        saving = false;
        if (caret !== undefined && active && sessionEpoch === epoch) {
          await tick();
          editor?.focus();
          editor?.setSelectionRange(caret,caret);
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
      input.value = source = buffer.display;
      return;
    }
    previous = {start:input.selectionStart,end:input.selectionEnd,kind:event.inputType};
  }

  function update(event: Event) {
    buffer.replaceVisible((event.currentTarget as HTMLTextAreaElement).value,previous);
    source = buffer.display;
    previous = undefined;
  }

  function paste(event: ClipboardEvent) {
    if (!event.clipboardData?.types.includes('text/plain')) return;
    event.preventDefault();
    const input = event.currentTarget as HTMLTextAreaElement;
    const value = event.clipboardData.getData('text/plain');
    const caret = input.selectionStart + value.replace(/\r\n?/g,'\n').length;
    buffer.splice(input.selectionStart,input.selectionEnd,value);
    input.value = source = buffer.display;
    input.setSelectionRange(caret,caret);
    previous = undefined;
  }
</script>

{#if snapshot}<p class="context"><bdi>{snapshot.name}</bdi> / <bdi>{snapshot.templateId}</bdi></p>{/if}
<h1>Маршрутизация роутера</h1>
{#if loading && !snapshot}<p role="status">Загружаем настройки роутера…</p>{/if}
{#if snapshot && adding}
  <form onsubmit={(event) => { event.preventDefault(); void request(true); }}>
    <h2>Новое локальное правило</h2>
    <label>Место в полном порядке
      <select aria-label="Место в полном порядке" bind:this={placement} bind:value={after} disabled={loading || saving}>
        {#if after === undefined}<option value={undefined}>Выберите место</option>{/if}
        <option value="">В начало</option>
        {#each snapshot.rules as rule (rule.id)}
          <option value={rule.id}>После {rule.label}{rule.excluded ? ' · исключено' : ''}{rule.disabled ? ' · отключено' : ''}</option>
        {/each}
      </select>
    </label>
    <label>JSON правила
      <textarea aria-label="JSON правила" bind:this={editor} value={source} rows={8} spellcheck={false} aria-invalid={invalid} disabled={loading || saving}
        oninput={update} onbeforeinput={beforeInput} onpaste={paste}></textarea>
    </label>
    {#if message}<p role="alert" class="notice error">{message}</p>{/if}
    <div class="actions">
      {#if conflict || unavailable}
        <button type="button" disabled={loading || saving} onclick={() => request()}>Перечитать настройки</button>
      {:else}
        <button type="submit" disabled={!active || loading || saving || after === undefined || !source}>Добавить правило</button>
      {/if}
      <button type="button" class="secondary" disabled={loading || saving} onclick={cancel}>Отмена</button>
    </div>
  </form>
{:else}
  {#if snapshot && !unavailable}
    {#if snapshot.rules.length === 0}<p>Правил пока нет.</p>
    {:else}
      <ol aria-label="Полный порядок правил">
        {#each snapshot.rules as rule (rule.id)}
          <li><bdi>{rule.label}</bdi>
            <p>{rule.origin === 'shared' ? 'Общее' : 'Локальное'}{rule.excluded ? ' · исключено' : ''}{rule.disabled ? ' · отключено' : ''}</p>
          </li>
        {/each}
      </ol>
    {/if}
  {/if}
  {#if message}<p role={success ? 'status' : 'alert'} class:notice={!success} class:error={!success}>{message}</p>{/if}
  <div class="actions">
    {#if snapshot && !unavailable}
      <button bind:this={addButton} disabled={!active || loading} onclick={begin}>Добавить локальное правило</button>
    {:else if message}
      <button disabled={loading} onclick={() => request()}>Перечитать настройки</button>
    {/if}
    <a href={backPath} onclick={(event) => back(event,backPath)}>К роутеру</a>
  </div>
{/if}

<style>
  form { display:grid; gap:1rem; }
  form h2 { margin:0; font-size:1.15rem; }
  .context { color:var(--muted); font-size:.86rem; overflow-wrap:anywhere; }
  label { display:grid; gap:.4rem; }
  select,textarea { width:100%; min-width:0; background:var(--surface); color:var(--ink); padding:.75rem; border:1px solid var(--line); border-radius:var(--radius); }
  select { max-width:32rem; }
  textarea { font-family:ui-monospace,monospace; resize:vertical; }
  textarea[aria-invalid=true] { border-color:#b42318; }
  ol { padding:1rem 1rem 1rem 2.5rem; border:1px solid var(--line); border-radius:var(--radius); background:var(--surface); }
  li { padding:.5rem 0; overflow-wrap:anywhere; }
  li p { margin:0; color:var(--muted); }
  .actions { display:flex; flex-wrap:wrap; align-items:center; gap:.75rem; }
  .secondary { background:var(--surface); color:var(--ink); border:1px solid var(--line); }
</style>
