<script lang="ts">
  import { onMount, untrack } from 'svelte';

  type Definition = {name:string; type:string; defaultSource?:string; required:boolean; referenced:boolean; issue?:string};
  type Snapshot = {id:string; elementId:string; version:string; definitions:Definition[]};
  type Reply = Snapshot & {state?:string; definitionIndex?:number; code?:string};
  type Draft = {name:string; type:string; source:string};
  let {id,elementId,active,csrf,sessionEpoch,close,signedOut,refreshSession,changed}:{
    id:string; elementId:string; active:boolean; csrf:string; sessionEpoch:number;
    close:()=>void; signedOut:()=>void; refreshSession:()=>void; changed:(before:string,after:string)=>void;
  } = $props();

  const types = ['string','number','boolean','object','array'];
  let view = $state<'loading'|'ready'|'unavailable'|'missing'>('loading');
  let verified = $state<Snapshot>();
  let comparison = $state<Snapshot>();
  let draft = $state<Draft[]>([]);
  let busy = $state(false);
  let conflict = $state(false);
  let comparing = $state(false);
  let feedback = $state('');
  let received = $state(false);
  let field = $state<number>();
  let heading: HTMLHeadingElement;
  let loading: AbortController | undefined;
  let saving: {epoch:number} | undefined;

  onMount(() => heading.focus());

  function endpoint() {
    return '/ui/v1/templates/variables?' + new URLSearchParams({templateId:id,elementId});
  }

  function valid(result: Reply) {
    return result?.id === id && result?.elementId === elementId &&
      /^[1-9][0-9]*$/.test(result.version) && Array.isArray(result.definitions);
  }

  function readSnapshot(result: Snapshot) {
    verified = result;
    draft = result.definitions.map(row=>({name:row.name,type:row.type,source:row.defaultSource ?? ''}));
    view = 'ready';
  }

  async function load(compare = false) {
    loading?.abort();
    const request = new AbortController();
    const epoch = sessionEpoch;
    loading = request;
    if (compare) comparing = true;
    else view = 'loading';
    try {
      const response = await fetch(endpoint(),{headers:{Accept:'application/json'},cache:'no-store',credentials:'same-origin',
        redirect:'error',signal:AbortSignal.any([request.signal,AbortSignal.timeout(10000)])});
      const result = await response.json() as Reply;
      if (loading !== request || request.signal.aborted || !active || sessionEpoch !== epoch) return;
      if (response.status === 401 && result.state === 'signed_out') signedOut();
      else if (response.ok && valid(result)) {
        if (compare) comparison = result;
        else readSnapshot(result);
      } else if (compare) feedback = 'Не удалось прочитать актуальные определения.';
      else view = response.status === 404 ? 'missing' : 'unavailable';
    } catch {
      if (loading === request && !request.signal.aborted && active && sessionEpoch === epoch) {
        if (compare) feedback = 'Не удалось прочитать актуальные определения.';
        else view = 'unavailable';
      }
    } finally {
      if (loading === request) comparing = false;
    }
  }

  $effect(() => {
    const epoch = sessionEpoch;
    const available = active;
    untrack(() => {
      if (saving && (!available || saving.epoch !== epoch)) {
        saving = undefined;
        busy = false;
        conflict = true;
        feedback = 'Результат сохранения неизвестен. Проверьте актуальные определения.';
      }
      if (available && !verified) void load();
    });
    return () => loading?.abort();
  });

  function issueMessage(code?:string):string {
    switch (code) {
      case 'type_mismatch': return 'Дефолт должен соответствовать выбранному типу.';
      case 'null_not_allowed': return 'Дефолт содержит null и не готов к сборке.';
      case 'invalid_variable_name': return 'Имя определения недопустимо при сборке.';
      case 'duplicate_variable': return 'Имя определения повторяется.';
      case 'invalid_variable_type': return 'Выбранный тип не поддерживается при сборке.';
      default: return 'Исправьте определение перед сборкой.';
    }
  }

  function sameNames(other: Snapshot) {
    return other.definitions.length === draft.length &&
      other.definitions.every((row,index)=>row.name === draft[index].name);
  }

  function useLatest() {
    if (!comparison || !sameNames(comparison)) return;
    verified = comparison;
    comparison = undefined;
    conflict = false;
    feedback = '';
  }

  async function save(event: SubmitEvent) {
    event.preventDefault();
    if (!active || busy || conflict || !verified || !draft.length) return;
    const version = verified.version;
    const request = {epoch:sessionEpoch};
    saving = request;
    busy = true;
    feedback = '';
    received = false;
    field = undefined;
    const definitions = draft.map(row=>({name:row.name,type:row.type,...(row.source === '' ? {} : {defaultSource:row.source})}));
    try {
      const response = await fetch(endpoint(),{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf,Accept:'application/json'},
        body:JSON.stringify({version,definitions}),cache:'no-store',credentials:'same-origin',redirect:'error',
        signal:AbortSignal.timeout(10000)});
      const result = await response.json() as Reply;
      if (!active || saving !== request || sessionEpoch !== request.epoch) return;
      if (response.status === 401 && result.state === 'signed_out') {
        saving = undefined;
        busy = false;
        signedOut();
      } else if (response.status === 403 && result.state === 'invalid_csrf') {
        feedback = 'Состояние сессии изменилось. Проверьте вход и повторите сохранение.';
        refreshSession();
      } else if (response.ok && valid(result)) {
        readSnapshot(result);
        conflict = false;
        comparison = undefined;
        received = true;
        feedback = 'Определения сохранены.';
        changed(version,result.version);
      } else if (result.state === 'invalid_source') {
        field = result.definitionIndex;
        feedback = result.code === 'duplicate_json_key' ? 'Ключ в JSON-дефолте повторяется.' : 'Исправьте синтаксис JSON-дефолта.';
      } else if (result.state === 'version_conflict') {
        conflict = true;
        comparison = undefined;
        feedback = 'Шаблон изменился. Ваши определения остались в редакторе.';
      } else {
        feedback = 'Не удалось сохранить определения. Введённые данные остались в редакторе.';
      }
    } catch {
      if (active && saving === request && sessionEpoch === request.epoch) {
        conflict = true;
        feedback = 'Результат сохранения неизвестен. Проверьте актуальные определения.';
      }
    } finally {
      if (saving === request) {
        saving = undefined;
        busy = false;
      }
    }
  }
</script>

<p class="subtitle"><bdi>{id}</bdi></p>
<h1 bind:this={heading} tabindex="-1">Переменные элемента</h1>
<p class="uuid">UUID <bdi>{elementId}</bdi></p>

{#if view === 'loading'}
  <p role="status">Загружаем определения…</p>
{:else if view === 'unavailable'}
  <p role="alert">Не удалось загрузить определения.</p>
  <button onclick={() => load()}>Повторить</button>
{:else if view === 'missing'}
  <p role="alert">Элемент не найден.</p>
{:else if verified && draft.length === 0}
  <p role="status">У этого элемента пока нет переменных.</p>
{:else if verified}
  <form onsubmit={save} aria-busy={busy}>
    {#each draft as row,index (index)}
      {@const definition=verified.definitions[index]}
      {@const edited=row.type !== definition.type || row.source !== (definition.defaultSource ?? '')}
      <fieldset>
        <legend><bdi>{row.name}</bdi></legend>
        {#if definition.referenced}
          <p class="subtitle">Ссылка в JSON: <code>{'$'+'{'+row.name+'}'}</code></p>
        {:else}
          <p class="subtitle">В исходном JSON больше нет ссылки.</p>
        {/if}
        <div class="fields">
          <label>Тип
            <select aria-label={'Тип '+row.name} bind:value={row.type} disabled={busy}>
              {#if !types.includes(row.type)}<option value={row.type}>{row.type}</option>{/if}
              {#each types as type}<option value={type}>{type}</option>{/each}
            </select>
          </label>
          <label>Значение по умолчанию (JSON)
            <textarea rows={2} spellcheck={false} aria-label={'Значение по умолчанию '+row.name}
              aria-invalid={field === index} aria-describedby={field === index ? 'variable-feedback' : undefined}
              bind:value={row.source} disabled={busy}></textarea>
          </label>
        </div>
        {#if field === index && feedback}
          <p id="variable-feedback" class="field-error" role="alert">{feedback}</p>
        {:else if edited}
          <p class="subtitle">Обязательность проверится после сохранения.</p>
        {:else if definition.issue}
          <p class="warning">{issueMessage(definition.issue)}</p>
          <p class="subtitle">Черновик можно сохранить. Перед сборкой исправьте определение.</p>
        {:else if definition.required}
          <p class="required">При сборке требуется значение роутера.</p>
        {:else}
          <p class="inherited">Роутер может использовать этот дефолт.</p>
        {/if}
      </fieldset>
    {/each}
    {#if feedback && field === undefined}
      <p class:warning={!received} role={received ? 'status':'alert'}>{feedback}</p>
    {/if}
    <div class="actions">
      {#if conflict}
        <button type="button" disabled={comparing} onclick={() => load(true)}>Показать актуальные определения</button>
      {:else}
        <button type="submit" disabled={!active || busy}>Сохранить определения</button>
      {/if}
      <button type="button" class="secondary" disabled={busy} onclick={close}>К JSON</button>
    </div>
  </form>
  {#if comparison}
    <h2>Актуальная версия {comparison.version}</h2>
    <ul>
      {#each comparison.definitions as item}
        <li><bdi>{item.name}</bdi> · {item.type} {item.defaultSource ?? 'Без дефолта'}</li>
      {/each}
    </ul>
    {#if sameNames(comparison)}
      <button onclick={useLatest}>Продолжить от актуальной версии</button>
    {/if}
  {/if}
{/if}
{#if !verified || draft.length === 0}
  <button type="button" class="secondary" onclick={close}>К JSON</button>
{/if}

<style>
  form { display:grid; gap:14px; }
  .subtitle,.uuid { color:var(--muted); font-size:.86rem; overflow-wrap:anywhere; }
  .uuid,textarea { font-family:ui-monospace,monospace; }
  fieldset { display:grid; gap:10px; min-width:0; max-width:640px; padding:12px 16px 16px;
    background:var(--surface); border:1px solid var(--line); border-radius:var(--radius); }
  legend { color:var(--ink); font-weight:650; padding:0 6px; overflow-wrap:anywhere; max-width:100%; }
  .fields { display:grid; gap:12px; grid-template-columns:150px minmax(0,1fr); align-items:start; }
  label { display:grid; gap:6px; font-size:.88rem; }
  textarea,select { width:100%; min-width:0; padding:9px 11px; border:1px solid var(--line);
    background:var(--surface); color:var(--ink); border-radius:var(--radius); }
  textarea[aria-invalid=true] { border-color:#b42318; }
  .field-error,.warning,.required,.inherited { padding:8px 10px; border-radius:var(--radius); overflow-wrap:anywhere; }
  .field-error { background:#fff0ed; color:#b42318; }
  .warning { background:#fff3d9; color:#935d10; }
  .required { background:#eaf1ff; }
  .inherited { background:#e8f5ed; color:#197449; }
  .actions { display:flex; flex-wrap:wrap; gap:10px; }
  button.secondary { background:var(--surface); color:var(--ink); border:1px solid var(--line); }
  li { overflow-wrap:anywhere; }
  @media(max-width:520px) { .fields { grid-template-columns:minmax(0,1fr); } }
</style>
