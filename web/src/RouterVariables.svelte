<script lang="ts">
  import { tick, untrack } from 'svelte';
  import { ExactSource, sourceLocation } from './exact-source';

  type Member = { id: string; label: string };
  type Section = { name: string; members: Member[] };
  type Value = { name: string; type: string; required: boolean; defaultSource?: string; personalSource?: string; issue?: string };
  type Selection = { id: string; section: string; mode: 'variables' | 'replacement'; definitions: Value[] };
  type Snapshot = {
    routerId: string; name: string; templateId: string; acceptedVersion: string; activeGeneration: string;
    draftGeneration: string; sections: Section[]; selection?: Selection;
  };
  type Reply = Snapshot & { state?: string; name?: string; code?: string; offset?: number };
  type Row = Value & { inherited: boolean; display: string; source: string };
  let { id, initialElementId, active, sessionEpoch, csrf, signedOut, refreshSession, back, openReplacement }: {
    id: string; initialElementId: string; active: boolean; sessionEpoch: number; csrf: string; signedOut: () => void;
    refreshSession: () => void; back: (event: MouseEvent, path: string) => void;
    openReplacement: (event: MouseEvent, path: string) => void;
  } = $props();

  const labels: Record<string, string> = {
    log:'Журналы', api:'API Xray', inbounds:'Входящие подключения', outbounds:'Исходящие подключения',
    routing:'Маршрутизация', rules:'Правила маршрутизации', dns:'DNS', policy:'Политика',
    stats:'Статистика', observatory:'Наблюдение', fakedns:'FakeDNS',
  };
  const diagnostics: Record<string, string> = {
    missing_value:'При сборке потребуется значение.',
    type_mismatch:'Значение не соответствует типу.',
    null_not_allowed:'JSON null недопустим при сборке.',
    invalid_variable_name:'Имя определения недопустимо.',
    invalid_variable_type:'Тип определения недопустим.',
    duplicate_variable:'Имя определения повторяется.',
  };
  const backPath = $derived('/ui/?' + new URLSearchParams({routerId:id}));
  let snapshot = $state<Snapshot>();
  const ownPath = $derived(snapshot?.selection && '/ui/routers/replacement?' + new URLSearchParams({routerId:id,elementId:snapshot.selection.id}));
  let rows = $state<Row[]>([]);
  let section = $state('');
  let message = $state('');
  let saved = $state(false);
  let loading = $state(false);
  let busy = $state(false);
  let conflict = $state(false);
  let unavailable = $state(false);
  let problem = $state('');
  let controller: AbortController | undefined;
  let operation: 'read' | 'save' | undefined;
  let observedEpoch: number | undefined;
  let sources = new Map<number, ExactSource>();
  let previous: Parameters<ExactSource['replaceVisible']>[1];

  function endpoint(element?: string): string {
    const query = new URLSearchParams({routerId:id});
    if (element) query.set('elementId',element);
    return '/ui/v1/routers/variables?' + query.toString();
  }

  function apply(next: Snapshot, keep: boolean) {
    snapshot = next;
    section = next.selection?.section ?? section;
    if (keep) {
      rows = next.selection?.definitions.map((row,i) => ({...row,inherited:rows[i].inherited,display:rows[i].display,source:rows[i].source})) ?? [];
      return;
    }
    sources = new Map();
    rows = (next.selection?.definitions ?? []).map((row, i) => {
      const editable = new ExactSource(row.personalSource ?? row.defaultSource ?? '');
      sources.set(i,editable);
      return {...row,inherited:row.personalSource === undefined,display:editable.display,source:editable.value};
    });
  }

  function compatible(next: Snapshot): boolean {
    const defs = next.selection?.definitions;
    return snapshot?.templateId === next.templateId && snapshot.selection?.id === next.selection?.id &&
      next.selection?.mode === 'variables' && !!defs && defs.length === rows.length &&
      defs.every((row,i) => row.name === rows[i].name && row.type === rows[i].type);
  }

  function dirty(): boolean {
    return rows.some(row => (row.inherited ? undefined : row.source) !== row.personalSource);
  }

  function valid(result: Reply, element?: string): boolean {
    return result.routerId === id && Array.isArray(result.sections) && (element ? result.selection?.id === element : !result.selection);
  }

  function explain(reply: Reply, status: number, writing = false) {
    if (status === 401 && reply.state === 'signed_out') {
      signedOut();
      return;
    }
    if (status === 403 && reply.state === 'invalid_csrf') {
      message = 'Состояние сессии изменилось. Повторите сохранение после проверки.';
      refreshSession();
      return;
    }
    if (reply.state === 'different_template') {
      message = 'У роутера есть черновик с другим шаблоном. Настройки остаются без изменений.';
    } else if (reply.state === 'router_or_element_unavailable') {
      message = snapshot ? 'Роутер или элемент недоступен. Ваши значения остались в форме.' : 'Роутер недоступен в текущем обзоре.';
      unavailable = true;
    } else if (reply.state === 'draft_conflict' || reply.state === 'replacement_mode') {
      message = 'Настройки изменились. Ваши значения остались в форме.';
      conflict = true;
    } else {
      message = writing ? 'Не удалось сохранить черновик. Ваши значения остались в форме.'
        : 'Не удалось загрузить параметры. Повторите попытку.';
    }
  }

  async function load(element?: string, keep = false) {
    if (!active || busy) return;
    controller?.abort();
    const request = new AbortController();
    const epoch = sessionEpoch;
    controller = request;
    operation = 'read';
    loading = true;
    message = '';
    saved = false;
    problem = '';
    unavailable = false;
    try {
      const reply = await fetch(endpoint(element),{cache:'no-store',credentials:'same-origin',redirect:'error',
        signal:AbortSignal.any([request.signal,AbortSignal.timeout(10000)])});
      const result = await reply.json() as Reply;
      if (controller !== request || !active || sessionEpoch !== epoch) return;
      if (!reply.ok) {
        explain(result,reply.status);
        if (!keep && snapshot?.selection) section = snapshot.selection.section;
      } else if (!valid(result,element)) {
        message = 'Не удалось загрузить параметры. Повторите попытку.';
      } else if (keep && !compatible(result)) {
        message = 'Состав или режим переменных изменился. Ваши значения остались в форме.';
      } else {
        apply(result,keep);
        conflict = false;
      }
    } catch {
      if (controller === request && active && epoch === sessionEpoch) message = 'Не удалось загрузить параметры. Повторите попытку.';
    } finally {
      if (controller === request) {
        controller = undefined;
        operation = undefined;
        loading = false;
      }
    }
  }

  $effect(() => {
    const ready = active;
    const changed = observedEpoch !== sessionEpoch;
    observedEpoch = sessionEpoch;
    untrack(() => {
      if (controller && (changed || !ready)) {
        controller.abort();
        if (operation === 'save') {
          message = 'Результат сохранения неизвестен. Перечитайте настройки.';
          conflict = true;
        }
        controller = undefined;
        operation = undefined;
        busy = false;
        loading = false;
      }
      if (ready && !snapshot && !controller) void load(initialElementId || undefined);
    });
  });

  function selectSection(event: Event) {
    const current = event.currentTarget as HTMLSelectElement;
    if (dirty() && !window.confirm('Продолжить без несохранённых значений?')) {
      current.value = section;
      return;
    }
    section = current.value;
    if (snapshot?.selection) void load();
  }

  function selectElement(event: Event) {
    const current = event.currentTarget as HTMLSelectElement;
    if (dirty() && !window.confirm('Продолжить без несохранённых значений?')) {
      current.value = snapshot?.selection?.id ?? '';
      return;
    }
    void load(current.value || undefined);
  }

  function beforeInput(event: InputEvent, i: number) {
    const input = event.currentTarget as HTMLTextAreaElement;
    const source = sources.get(i);
    if (!source) return;
    if (event.inputType === 'historyUndo' || event.inputType === 'historyRedo') {
      event.preventDefault();
      if (event.inputType === 'historyUndo') source.undo();
      else source.redo();
      input.value = rows[i].display = source.display;
      rows[i].source = source.value;
      return;
    }
    previous = {start:input.selectionStart,end:input.selectionEnd,kind:event.inputType};
  }

  function updateSource(event: Event, i: number) {
    const input = event.currentTarget as HTMLTextAreaElement;
    const source = sources.get(i);
    if (!source) return;
    source.replaceVisible(input.value,previous);
    previous = undefined;
    rows[i].display = source.display;
    rows[i].source = source.value;
  }

  function paste(event: ClipboardEvent, i: number) {
    if (!event.clipboardData?.types.includes('text/plain')) return;
    const input = event.currentTarget as HTMLTextAreaElement;
    const source = sources.get(i);
    if (!source) return;
    event.preventDefault();
    const value = event.clipboardData.getData('text/plain');
    const caret = input.selectionStart + value.replace(/\r\n?/g,'\n').length;
    source.splice(input.selectionStart,input.selectionEnd,value);
    input.value = rows[i].display = source.display;
    rows[i].source = source.value;
    input.setSelectionRange(caret,caret);
    previous = undefined;
  }

  async function save(event: SubmitEvent) {
    event.preventDefault();
    if (!active || busy || loading || conflict || unavailable || !snapshot?.selection || !dirty()) return;
    const {templateId,acceptedVersion,activeGeneration,draftGeneration,selection} = snapshot;
    const values = rows.map(row => ({name:row.name,source:row.inherited ? undefined : row.source}))
      .filter((row,i) => row.source !== rows[i].personalSource);
    const request = new AbortController();
    const epoch = sessionEpoch;
    controller = request;
    operation = 'save';
    busy = true;
    message = '';
    problem = '';
    saved = false;
    let caret: number | undefined;
    try {
      const response = await fetch(endpoint(selection.id),{
        method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},cache:'no-store',credentials:'same-origin',redirect:'error',
        signal:AbortSignal.any([request.signal,AbortSignal.timeout(10000)]),
        body:JSON.stringify({templateId,acceptedVersion,activeGeneration,draftGeneration,values})
      });
      const reply = await response.json() as Reply;
      if (controller !== request || !active || epoch !== sessionEpoch) return;
      if (response.ok && valid(reply,selection.id)) {
        apply(reply,false);
        saved = true;
        message = 'Черновик сохранён. Действующая конфигурация не изменилась.';
      } else if (reply.state === 'invalid_source' && reply.name) {
        problem = reply.name;
        const row = values.find(row => row.name === problem);
        if (typeof reply.offset === 'number' && row?.source !== undefined) {
          const location = sourceLocation(row.source,reply.offset);
          caret = location.visibleOffset;
          message = `Строка ${location.line}, столбец ${location.column}. Исправьте синтаксис JSON.`;
        } else message = reply.code === 'duplicate_json_key' ? 'Повторяющийся ключ JSON.' : 'Исправьте синтаксис JSON.';
      } else {
        explain(reply,response.status,true);
      }
    } catch {
      if (controller === request && active && epoch === sessionEpoch) {
        conflict = true;
        message = 'Результат сохранения неизвестен. Перечитайте настройки.';
      }
    } finally {
      if (controller === request) {
        controller = undefined;
        operation = undefined;
        busy = false;
        if (caret !== undefined && active && epoch === sessionEpoch) {
          await tick();
          const field = document.getElementById('personal-value-'+rows.findIndex(row => row.name === problem));
          if (field instanceof HTMLTextAreaElement) {
            field.focus();
            field.setSelectionRange(caret,caret);
          }
        }
      }
    }
  }

  $effect(() => () => { controller?.abort(); controller = undefined; });
</script>

{#if snapshot}<p class="context"><bdi>{snapshot.name}</bdi> / <bdi>{snapshot.templateId}</bdi></p>{/if}
<h1>Персональные переменные</h1>
{#if loading && !snapshot}<p role="status">Загружаем настройки роутера…</p>{/if}
{#if message}<p role={saved ? 'status' : 'alert'} class:notice={!saved} class:error={!saved}>{message}</p>{/if}
{#if snapshot}
  <form onsubmit={save}>
    <div class="coordinates">
      <label>Секция Xray
        <select value={section} onchange={selectSection} disabled={loading || busy}>
          <option value="">Выберите секцию</option>
          {#each snapshot.sections as group (group.name)}
            <option value={group.name}>{Object.hasOwn(labels,group.name) ? labels[group.name] : group.name}</option>
          {/each}
        </select>
      </label>
      <label>Элемент
        <select aria-label="Элемент" value={snapshot.selection?.id ?? ''} onchange={selectElement} disabled={!section || loading || busy}>
          <option value="">Выберите элемент</option>
          {#each snapshot.sections.find(group => group.name === section)?.members ?? [] as member (member.id)}
            <option value={member.id}>{member.label}</option>
          {/each}
        </select>
      </label>
    </div>
    {#if snapshot.selection}
      <p class="context">UUID <bdi>{snapshot.selection.id}</bdi></p>
      {#if snapshot.selection.mode === 'replacement'}
        <p>Этот элемент использует собственный JSON. Переменные сейчас не действуют.</p>
      {:else if rows.length === 0}
        <p>У выбранного элемента нет определений переменных.</p>
      {/if}
    {:else}
      <p>Выберите общий элемент, чтобы открыть его переменные.</p>
    {/if}
    {#each rows as row, i (i)}
      <fieldset disabled={busy || loading}>
        <legend><bdi>{row.name}</bdi> · {row.type}</legend>
        <label class="mode"><input type="checkbox" bind:checked={row.inherited}> Использовать шаблон</label>
        {#if row.inherited && row.defaultSource !== undefined}
          <p>Дефолт шаблона: <code>{row.defaultSource}</code></p>
        {:else if !row.inherited}
          <label>JSON
            <textarea id={'personal-value-'+i} value={row.display} rows={3} spellcheck={false} aria-invalid={problem === row.name}
              onbeforeinput={(event) => beforeInput(event,i)} oninput={(event) => updateSource(event,i)} onpaste={(event) => paste(event,i)}></textarea>
          </label>
        {/if}
        {#if row.issue}<p>{Object.hasOwn(diagnostics,row.issue) ? diagnostics[row.issue] : 'Проверьте значение и определение шаблона.'}</p>{/if}
      </fieldset>
    {/each}
    <div class="actions">
      {#if conflict}
        <button type="button" disabled={loading} onclick={() => load(snapshot?.selection?.id,true)}>Перечитать настройки</button>
      {:else if rows.length > 0}
        <button type="submit" disabled={!active || loading || busy || unavailable || !dirty()}>Сохранить черновик</button>
      {/if}
      <a href={backPath} onclick={(event) => back(event,backPath)}>К роутеру</a>
      {#if ownPath && !unavailable && !conflict && !busy && !loading}
        <a href={ownPath} onclick={(event) => {
          if (dirty() && !window.confirm('Продолжить без несохранённых значений?')) {
            event.preventDefault();
            return;
          }
          openReplacement(event,ownPath);
        }}>Свой JSON</a>
      {/if}
    </div>
  </form>
{:else}
  <div class="actions">
    {#if message && !unavailable}<button disabled={loading} onclick={() => { void load(); }}>Повторить</button>{/if}
    <a href={backPath} onclick={(event) => back(event,backPath)}>К роутеру</a>
  </div>
{/if}

<style>
  form { display:grid; gap:1rem; }
  .context { color:var(--muted); font-size:.86rem; overflow-wrap:anywhere; }
  .coordinates,.actions { display:flex; flex-wrap:wrap; gap:.75rem; }
  .coordinates label { width:min(300px,100%); }
  label { display:grid; gap:.4rem; }
  fieldset { display:grid; min-width:0; gap:.55rem; margin:0; padding:1rem; border:1px solid var(--line); border-radius:var(--radius); background:var(--surface); }
  fieldset p { margin:0; overflow-wrap:anywhere; }
  legend { font-weight:600; overflow-wrap:anywhere; }
  .mode { display:flex; align-items:center; gap:.55rem; }
  .mode input { width:auto; }
  select,textarea { width:100%; min-width:0; background:var(--surface); color:var(--ink); padding:.65rem; border:1px solid var(--line); border-radius:var(--radius); }
  textarea,code { font-family:ui-monospace,monospace; }
  textarea { resize:vertical; }
  textarea[aria-invalid=true] { border-color:#b42318; }
  .actions { align-items:center; }
</style>
