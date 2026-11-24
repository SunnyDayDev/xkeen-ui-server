<script lang="ts">
  import { tick, untrack } from 'svelte';
  import { ExactSource, sourceLocation } from './exact-source';
  import TemplateVariables from './TemplateVariables.svelte';

  type Member = { id: string; label: string };
  type Section = { name: string; members: Member[] };
  type Selection = { id: string; section: string; source: string };
  type Snapshot = {id: string; version: string; sections: Section[]; selected?: Selection; firstSections?: string[]; appendSections?: string[]};
  type Reply = Snapshot & { state?: string; code?: string; offset?: number; path?: string };
  type ReadState = 'loading' | 'ready' | 'missing' | 'unavailable';

  let { id, active, sessionEpoch, csrf, signedOut, refreshSession, changed }: {
    id: string; active: boolean; sessionEpoch: number; csrf: string;
    signedOut: () => void; refreshSession: () => void; changed: () => void;
  } = $props();

  const sectionLabels: Record<string, string> = {
    log:'Журналы', api:'API Xray', inbounds:'Входящие подключения',
    outbounds:'Исходящие подключения', routing:'Маршрутизация', rules:'Правила маршрутизации',
    dns:'DNS', policy:'Политика', stats:'Статистика', observatory:'Наблюдение', fakedns:'FakeDNS',
  };

  let current = $state<Snapshot>();
  let comparison = $state<Snapshot>();
  let loadState = $state<ReadState>('loading');
  let section = $state('');
  let source = $state('');
  let message = $state('');
  let warning = $state(false);
  let busy = $state(false);
  let appending = $state(false);
  let sectionField = $state<HTMLSelectElement>();
  let addButton = $state<HTMLButtonElement>();
  let editorField = $state<HTMLTextAreaElement>();
  let saving: { epoch: number } | undefined;
  let comparing = $state(false);
  let conflict = $state(false);
  let editingVariables = $state(false);
  let variableButton = $state<HTMLButtonElement>();
  let buffer = new ExactSource('');
  let previous: { start: number; end: number; kind: string } | undefined;
  let reading: AbortController | undefined;

  function endpoint(elementId?: string): string {
    const query = new URLSearchParams({templateId:id});
    if (elementId) query.set('elementId',elementId);
    return '/ui/v1/templates/item?' + query.toString();
  }

  function sectionName(name: string): string {
    return Object.hasOwn(sectionLabels, name) ? sectionLabels[name] : name;
  }

  function applySnapshot(snapshot: Snapshot, clearSource: boolean) {
    current = snapshot;
    section = snapshot.selected?.section ?? '';
    if (clearSource) {
      buffer = new ExactSource(snapshot.selected?.source ?? '');
      source = buffer.display;
    }
    loadState = 'ready';
  }

  function validSnapshot(result: Reply): boolean {
    return result?.id === id && typeof result.version === 'string' &&
      /^[1-9][0-9]*$/.test(result.version) && Array.isArray(result.sections);
  }

  function readFailed(missing: boolean, forComparison: boolean) {
    if (forComparison) {
      warning = true;
      message = 'Не удалось прочитать актуальную версию. Ваш JSON остался в редакторе.';
    } else if (current) {
      loadState = 'ready';
      warning = true;
      message = 'Не удалось открыть выбранный элемент. Ваш JSON остался в редакторе.';
    } else {
      loadState = missing ? 'missing' : 'unavailable';
    }
  }

  async function load(selected?: string, forComparison = false) {
    reading?.abort();
    const request = new AbortController();
    const epoch = sessionEpoch;
    reading = request;
    if (forComparison) comparing = true;
    else loadState = 'loading';
    try {
      const response = await fetch(endpoint(selected),{
        headers:{Accept:'application/json'},cache:'no-store',credentials:'same-origin',redirect:'error',
        signal:AbortSignal.any([request.signal,AbortSignal.timeout(10000)])
      });
      const result = await response.json() as Reply;
      if (reading !== request || request.signal.aborted || !active || epoch !== sessionEpoch) return;
      if (response.status === 401 && result.state === 'signed_out') {
        signedOut();
      } else if (response.ok && validSnapshot(result)) {
        if (forComparison) comparison = result;
        else applySnapshot(result, true);
      } else {
        readFailed(response.status === 404, forComparison);
      }
    } catch {
      if (reading !== request || request.signal.aborted || !active || epoch !== sessionEpoch) return;
      readFailed(false, forComparison);
    } finally {
      if (reading === request) comparing = false;
    }
  }

  $effect(() => {
    const epoch = sessionEpoch;
    const available = active;
    untrack(() => {
      if (current && loadState === 'loading') readFailed(false, false);
      if (saving && (!available || saving.epoch !== epoch)) {
        saving = undefined;
        busy = false;
        warning = true;
        message = 'Результат сохранения неизвестен. Проверьте актуальную версию.';
        conflict = true;
      }
    });
    if (!available) return;
    untrack(() => { if (!current) void load(); });
    return () => { reading?.abort(); };
  });

  function selectSection(event: Event) {
    const selected = (event.currentTarget as HTMLSelectElement).value;
    if (current?.selected && !appending) {
      const next = current.sections.find(entry => entry.name === selected)?.members[0];
      if (next) void load(next.id);
    } else {
      section = selected;
    }
  }

  async function beginAppend() {
    if (!active || busy || conflict || !current?.selected || !current.appendSections?.length) return;
    if (buffer.value !== current.selected.source && !window.confirm('Продолжить без несохранённых изменений текущего элемента?')) return;
    appending = true;
    section = '';
    buffer = new ExactSource('');
    source = buffer.display;
    previous = undefined;
    warning = false;
    message = '';
    await tick();
    sectionField?.focus();
  }

  async function cancelAppend() {
    if (!current || busy) return;
    reading?.abort();
    comparing = false;
    appending = false;
    comparison = undefined;
    conflict = false;
    warning = false;
    message = '';
    previous = undefined;
    applySnapshot(current, true);
    await tick();
    addButton?.focus();
  }

  function beforeInput(event: InputEvent) {
    const input = event.currentTarget as HTMLTextAreaElement;
    if (event.inputType === 'historyUndo' || event.inputType === 'historyRedo') {
      event.preventDefault();
      if (event.inputType === 'historyUndo') buffer.undo();
      else buffer.redo();
      input.value = source = buffer.display;
      return;
    }
    previous = {start:input.selectionStart, end:input.selectionEnd, kind:event.inputType};
  }

  function updateSource(event: Event) {
    const input = event.currentTarget as HTMLTextAreaElement;
    buffer.replaceVisible(input.value, previous);
    previous = undefined;
    source = buffer.display;
  }

  function paste(event: ClipboardEvent) {
    if (!event.clipboardData?.types.includes('text/plain')) return;
    event.preventDefault();
    const input = event.currentTarget as HTMLTextAreaElement;
    const value = event.clipboardData.getData('text/plain');
    const caret = input.selectionStart + value.replace(/\r\n?/g,'\n').length;
    buffer.splice(input.selectionStart, input.selectionEnd, value);
    input.value = source = buffer.display;
    input.setSelectionRange(caret,caret);
    previous = undefined;
  }

  function editorKey(event: KeyboardEvent) {
    if ((!event.metaKey && !event.ctrlKey) || event.altKey || !['z','y'].includes(event.key.toLowerCase())) return;
    event.preventDefault();
    const input = event.currentTarget as HTMLTextAreaElement;
    if (event.shiftKey || event.key.toLowerCase() === 'y') buffer.redo();
    else buffer.undo();
    input.value = source = buffer.display;
    previous = undefined;
  }

  function compare() {
    void load(current?.selected?.id, true);
  }

  function useLatest() {
    if (!comparison || comparison.selected?.id !== current?.selected?.id) return;
    current = comparison;
    if (appending && !current.appendSections?.includes(section)) section = '';
    comparison = undefined;
    conflict = false;
    warning = false;
    message = '';
  }

  async function save(event: SubmitEvent) {
    event.preventDefault();
    if (!active || busy || conflict || !current || !section) return;
    const request = { epoch: sessionEpoch };
    saving = request;
    busy = true;
    let caret: number | undefined;
    message = '';
    warning = false;
    const draft = buffer.value;
    const input = appending ? {version:current.version,appendSection:section,source:draft}
      : current.selected ? {version:current.version,elementId:current.selected.id,source:draft}
      : {version:current.version,section,source:draft};
    try {
      const response = await fetch(endpoint(),{
        method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf,Accept:'application/json'},
        body:JSON.stringify(input),cache:'no-store',credentials:'same-origin',redirect:'error',
        signal:AbortSignal.timeout(10000)
      });
      const result = await response.json() as Reply;
      if (!active || saving !== request || sessionEpoch !== request.epoch) return;
      if (response.status === 401 && result.state === 'signed_out') {
        busy = false;
        signedOut();
      } else if (response.status === 403 && result.state === 'invalid_csrf') {
        warning = true;
        message = 'Состояние сессии изменилось. Проверьте вход и повторите сохранение.';
        refreshSession();
      } else if (response.ok && validSnapshot(result) && result.selected) {
        appending = false;
        applySnapshot(result, false);
        conflict = false;
        comparison = undefined;
        message = 'Черновик сохранён.';
        changed();
      } else if (result.state === 'invalid_source') {
        if (typeof result.offset === 'number') {
          const {line,column,visibleOffset} = sourceLocation(draft,result.offset);
          caret = visibleOffset;
          message = `Строка ${line}, столбец ${column}. Исправьте синтаксис JSON.`;
        } else {
          message = result.code === 'duplicate_json_key' ? 'Повторяющийся ключ JSON.' : 'Исправьте синтаксис JSON.';
          if (result.path) message += ' Путь: ' + result.path;
        }
      } else if (result.state === 'section_occupied') {
        warning = true;
        message = 'В этой секции уже есть элемент. Выберите другую секцию.';
      } else if (result.state === 'version_conflict') {
        comparison = undefined;
        conflict = true;
        warning = true;
        message = 'Шаблон изменился в другой вкладке. Ваш JSON остался в редакторе.';
      } else {
        warning = true;
        message = 'Не удалось сохранить черновик. Ваш JSON остался в редакторе.';
      }
    } catch {
      if (active && saving === request && sessionEpoch === request.epoch) {
        warning = true;
        conflict = true;
        message = 'Результат сохранения неизвестен. Проверьте актуальную версию.';
      }
    } finally {
      if (saving === request) {
        saving = undefined;
        busy = false;
        if (caret !== undefined && active && sessionEpoch === request.epoch) {
          await tick();
          editorField?.focus();
          editorField?.setSelectionRange(caret,caret);
        }
      }
    }
  }
</script>

{#if editingVariables && current?.selected}
  <TemplateVariables {id} elementId={current.selected.id} {active} {csrf} {sessionEpoch} {signedOut}
    {refreshSession} close={async () => { editingVariables = false; await tick(); variableButton?.focus(); }}
    changed={(before,after) => {
      if (current?.version === before) current = {...current,version:after};
      changed();
    }} />
{:else}
<p class="editor-context"><bdi>{id}</bdi></p>
<h1>{appending ? 'Новый элемент' : loadState === 'ready' && !current?.selected ? 'Первый элемент' : 'Сырой JSON элемента'}</h1>
{#if loadState === 'loading'}
  <p class="editor-info" role="status">Загружаем шаблон…</p>
{:else if loadState === 'missing'}
  <p class="editor-warning" role="alert">Шаблон или элемент не найден. Проверьте ссылку и вернитесь к списку.</p>
  <a href="/ui/templates">К шаблонам</a>
{:else if loadState === 'unavailable'}
  <p class="editor-warning" role="alert">Не удалось загрузить шаблон. Повторите попытку.</p>
  <button onclick={() => load()}>Повторить</button>
{:else if current}
  <form onsubmit={save} aria-busy={busy}>
    <div class="editor-coordinates">
      <label>Секция Xray
        <select bind:this={sectionField} value={section} onchange={selectSection} disabled={busy}>
          {#if appending || !current.selected}<option value="">Выберите секцию</option>{/if}
          {#each appending ? current.appendSections ?? [] : current.selected ? current.sections.filter(group => group.members.length).map(group => group.name)
            : current.firstSections ?? [] as name (name)}
            <option value={name}>{sectionName(name)}</option>
          {/each}
        </select>
      </label>
      {#if current.selected && !appending}
        <label>Элемент
          <select value={current.selected.id} onchange={(e) => load(e.currentTarget.value)} disabled={busy}>
            {#each current.sections.find(group => group.name === section)?.members ?? [] as item (item.id)}
              <option value={item.id}>{item.label}</option>
            {/each}
          </select>
        </label>
      {/if}
    </div>
    {#if current.selected && !appending}
      <p class="editor-id">UUID <bdi>{current.selected.id}</bdi></p>
    {:else if !appending}
      <p class="editor-context">Элемент появится в шаблоне после сохранения.</p>
    {/if}
    <label>JSON
      <textarea bind:this={editorField} value={source} rows={8} spellcheck={false} autocapitalize="off" disabled={busy}
        aria-invalid={message !== '' && !warning && message !== 'Черновик сохранён.'}
        aria-describedby={message ? 'template-editor-feedback' : undefined}
        onbeforeinput={beforeInput} oninput={updateSource} onpaste={paste} onkeydown={editorKey}></textarea>
    </label>
    {#if message}
      <p id="template-editor-feedback" class:editor-warning={warning} class:editor-error={!warning && message !== 'Черновик сохранён.'}
        role={message === 'Черновик сохранён.' ? 'status' : 'alert'}>{message}</p>
    {/if}
    <div class="editor-actions">
      {#if conflict}
        <button type="button" disabled={comparing} onclick={compare}>Показать актуальную версию</button>
      {:else}
        <button type="submit" disabled={!active || busy || !section}>{appending ? 'Добавить элемент' : current.selected ? 'Сохранить черновик' : 'Создать элемент'}</button>
        {#if current.selected && !appending}
          <button bind:this={variableButton} type="button" class="secondary" disabled={busy} onclick={() => { editingVariables = true; }}>Переменные</button>
          {#if current.appendSections?.length}
            <button bind:this={addButton} type="button" class="secondary" disabled={!active || busy} onclick={beginAppend}>Добавить элемент</button>
          {/if}
        {/if}
      {/if}
      {#if appending}
        <button type="button" class="secondary" disabled={busy} onclick={cancelAppend}>Отмена</button>
      {/if}
    </div>
  </form>
  {#if comparison}
    <h2>Актуальная версия {comparison.version}</h2>
    {#if comparison.selected}
      <pre>{comparison.selected.source}</pre>
    {/if}
    {#if comparison.selected?.id === current.selected?.id}
      <button onclick={useLatest}>Продолжить от актуальной версии</button>
    {/if}
  {/if}
{/if}
{/if}

<style>
  form { display:grid; gap:14px; }
  .editor-context,.editor-id { color:var(--muted); font-size:.86rem; overflow-wrap:anywhere; }
  .editor-id,textarea,pre { font-family:ui-monospace,monospace; }
  .editor-coordinates,.editor-actions { display:flex; flex-wrap:wrap; gap:12px; }
  .editor-coordinates label { width:min(300px,100%); }
  label { display:grid; gap:6px; font-size:.88rem; }
  textarea,select,pre {
    width:100%; min-width:0; border:1px solid var(--line); border-radius:var(--radius);
    background:var(--surface); color:var(--ink); padding:12px;
  }
  textarea { resize:vertical; line-height:1.55; }
  textarea[aria-invalid=true] { border-color:#b42318; }
  button.secondary { background:var(--surface); color:var(--ink); border:1px solid var(--line); }
  pre { white-space:pre-wrap; overflow-wrap:anywhere; }
  .editor-info,.editor-warning,.editor-error { padding:10px 12px; border-radius:var(--radius); overflow-wrap:anywhere; }
  .editor-info { background:#eaf1ff; }
  .editor-warning { background:#fff3d9; color:#935d10; }
  .editor-error { background:#fff0ed; color:#b42318; }
</style>
