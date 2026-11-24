<script lang="ts">
  import { onDestroy, untrack } from 'svelte';

  type Variable = {name: string; type: string; defaultSource?: string; required: boolean};
  type Element = {section: string; elementId: string; inactive?: boolean; variables: Variable[]};
  type Source = {routerId: string; templateId: string; source: 'accepted' | 'draft'; version: string; elements: Element[]};
  type Prepared = {routerId: string; name: string; templateId: string; revision: string};
  type ErrorState = {state?: string; code?: string; elementId?: string; variable?: string};
  type Work = 'templates' | 'source' | 'confirm' | 'lookup';
  type Request = {controller: AbortController; epoch: number; work: Work};
  type Props = {
    canSubmit: boolean; sessionEpoch: number; csrf: string;
    signedOut: () => void; refreshSession: () => void;
  };

  let {canSubmit, sessionEpoch, csrf, signedOut, refreshSession}: Props = $props();
  let templates = $state<{id: string}[]>();
  let selected = $state('');
  let source = $state<Source>();
  let routerId = $state('');
  let name = $state('');
  let values = $state(new Map<string,string>());
  let work = $state<Work>();
  let message = $state('');
  let fieldError = $state<ErrorState>();
  let failedRead = $state(false);
  let conflict = $state(false);
  let unknown = $state(false);
  let prepared = $state<Prepared>();
  let pending: Request | undefined;

  const endpoint = '/ui/v1/routers/prepare';
  const unknownResult = 'Не удалось подтвердить результат.';
  const variableType: Record<string,string> = {string:'Строка',number:'Число',boolean:'Логическое значение',object:'Объект',array:'Массив'};

  $effect(() => {
    const epoch = sessionEpoch;
    const available = canSubmit;
    if (pending && (!available || pending.epoch !== epoch)) {
      pending.controller.abort();
      if (pending.work === 'confirm' || pending.work === 'lookup') {
        unknown = true;
        message = unknownResult;
      }
      pending = undefined;
      work = undefined;
    }
    if (available) untrack(() => {
      if (pending || failedRead) return;
      if (!templates) void loadTemplates();
      else if (selected && !source) void loadSource(selected);
    });
  });
  onDestroy(() => {
    pending?.controller.abort();
    pending = undefined;
  });

  function begin(action: Work): Request {
    pending?.controller.abort();
    const request = {controller: new AbortController(), epoch: sessionEpoch, work: action};
    pending = request;
    work = action;
    message = '';
    fieldError = undefined;
    return request;
  }

  async function exchange(request: Request, path: string, payload?: object) {
    try {
      const headers: Record<string,string> = {Accept:'application/json'};
      if (payload) {
        headers['Content-Type'] = 'application/json';
        headers['X-CSRF-Token'] = csrf;
      }
      const res = await fetch(path, {
        method: payload ? 'POST' : 'GET', headers, body: payload ? JSON.stringify(payload) : undefined,
        credentials:'same-origin', cache:'no-store', redirect:'error',
        signal: AbortSignal.any([request.controller.signal, AbortSignal.timeout(payload ? 35000 : 10000)])
      });
      return {status:res.status, body:await res.json()};
    } catch {
      return undefined;
    }
  }

  function finish(request: Request, reply: {status:number; body:ErrorState} | undefined) {
    if (!canSubmit || pending !== request || request.epoch !== sessionEpoch) return false;
    pending = undefined;
    work = undefined;
    if (reply?.status === 401 && reply.body?.state === 'signed_out') {
      signedOut();
      return false;
    }
    return true;
  }

  async function loadTemplates() {
    failedRead = false;
    const request = begin('templates');
    const reply = await exchange(request, '/ui/v1/templates');
    if (!finish(request, reply)) return;
    if (reply?.status === 200 && Array.isArray(reply.body?.templates)) {
      templates = reply.body.templates;
    } else {
      failedRead = true;
      message = 'Не удалось загрузить шаблоны.';
    }
  }

  async function loadSource(id: string) {
    if (id !== selected) values = new Map();
    selected = id;
    source = undefined;
    conflict = false;
    failedRead = false;
    if (!id) {
      pending?.controller.abort();
      pending = undefined;
      work = undefined;
      message = '';
      fieldError = undefined;
      return;
    }
    const request = begin('source');
    const reply = await exchange(request, endpoint + '?' + new URLSearchParams({templateId:id}));
    if (!finish(request, reply)) return;
    if (reply?.status === 200 && reply.body?.templateId === id && Array.isArray(reply.body?.elements)) {
      source = reply.body;
      if (!routerId) routerId = reply.body.routerId;
    } else {
      failedRead = true;
      message = 'Не удалось загрузить шаблон.';
    }
  }

  function key(element: Element, variable: Variable) {
    return element.elementId + '/' + variable.name;
  }

  function assign(element: Element, variable: Variable, value: string) {
    values = new Map(values).set(key(element,variable), value);
  }

  function placeholder(variable: Variable) {
    if (variable.defaultSource === undefined || variable.required) return '';
    if (variable.type !== 'string') return variable.defaultSource;
    try {
      return JSON.parse(variable.defaultSource) as string;
    } catch {
      return '';
    }
  }

  function matchesPrepared(body: Prepared | undefined): body is Prepared {
    return body?.routerId === routerId && body.revision === '1' && typeof body.name === 'string';
  }

  async function confirm(event: SubmitEvent) {
    event.preventDefault();
    if (!source || !canSubmit || pending || conflict || unknown) return;
    const payload = {
      routerId, name, templateId:source.templateId, source:source.source, version:source.version,
      values:source.elements.filter(element => !element.inactive).flatMap(element => element.variables.flatMap(variable => {
        const value = values.get(key(element,variable));
        return value === undefined || value === '' ? [] : [{
          elementId:element.elementId, name:variable.name, source:variable.type === 'string' ? JSON.stringify(value) : value
        }];
      }))
    };
    const request = begin('confirm');
    const reply = await exchange(request, endpoint, payload);
    if (!finish(request,reply)) return;
    const state = reply?.body?.state;
    if (reply?.status === 201 && matchesPrepared(reply.body)) {
      prepared = reply.body;
    } else if (reply?.status === 409 && state === 'conflict') {
      conflict = true;
      message = 'Снимок изменился. Перечитайте шаблон и подтвердите ещё раз.';
    } else if (reply?.status === 422 && state === 'invalid_input') {
      fieldError = reply.body;
      message = reply.body.code === 'missing_value' ? 'Заполните обязательное значение.' : 'Проверьте значение и его тип.';
    } else if (reply?.status === 422 && state === 'catalog_rejected') {
      message = 'Xray отклонил конфигурацию. Проверьте значения шаблона.';
    } else if (reply?.status === 422 && state === 'configuration_invalid') {
      message = 'Конфигурация шаблона не готова. Проверьте выбранный шаблон и значения.';
    } else if (reply?.status === 503 && state === 'validator_unavailable') {
      message = 'Проверка Xray сейчас недоступна.';
    } else if (reply?.status === 403 && state === 'invalid_csrf') {
      message = 'Состояние сессии изменилось. Проверьте ввод и повторите.';
      refreshSession();
    } else {
      unknown = true;
      message = unknownResult;
    }
  }

  async function lookup() {
    if (!canSubmit || pending || !routerId) return;
    const request = begin('lookup');
    const reply = await exchange(request, endpoint + '?' + new URLSearchParams({routerId}));
    if (!finish(request,reply)) return;
    if (reply?.status === 200 && matchesPrepared(reply.body)) {
      prepared = reply.body;
      unknown = false;
    } else if (reply?.status === 404 && reply.body?.state === 'not_prepared') {
      unknown = false;
      message = 'Подготовка не зафиксирована. Подтвердите ещё раз.';
    } else {
      message = unknownResult;
    }
  }
</script>

{#if prepared}
  <h1>Роутер подготовлен</h1>
  <div class="router-card">
    <strong>{prepared.name}</strong>
    <p>ID: <bdi>{prepared.routerId}</bdi></p>
    <p>Желаемая ревизия {prepared.revision}</p>
  </div>
{:else}
  <h1>Подключить роутер</h1>
  {#if !templates}
    <div class="router-card">
      {#if failedRead}
        <p role="alert">{message}</p>
        <button onclick={loadTemplates}>Повторить</button>
      {:else}
        <p role="status">Загружаем шаблоны…</p>
      {/if}
    </div>
  {:else if templates.length === 0}
    <div class="router-card">
      <p>Шаблонов пока нет.</p>
      <a href="/ui/templates">Открыть шаблоны</a>
    </div>
  {:else}
    <form class="router-card" aria-label="Подготовка роутера" onsubmit={confirm}>
      <div class="router-field">
        <label for="router-preparation-name">Имя роутера</label>
        <input id="router-preparation-name" bind:value={name} required readonly={work === 'confirm' || unknown}
          placeholder="Например, Филиал Восток" />
      </div>
      <div class="router-field">
        <label for="router-preparation-template">Шаблон</label>
        <select id="router-preparation-template" value={selected} required disabled={work === 'confirm' || unknown}
          onchange={(event)=> { void loadSource(event.currentTarget.value); }}>
          <option value="">Выберите шаблон</option>
          {#each templates as template (template.id)}
            <option value={template.id}>{template.id}</option>
          {/each}
        </select>
        {#if source}
          <p>{source.source === 'accepted' ? 'Принятый снимок' : 'Черновик'} · версия {source.version}</p>
          {#if source.source === 'draft'}<p>При подтверждении черновик станет принятым снимком.</p>{/if}
        {/if}
      </div>
      {#if work === 'source'}<p role="status">Загружаем определения шаблона…</p>{/if}
      {#if source?.elements.length}
        <h2>Персональные значения</h2>
        {#each source.elements as element, elementIndex (element.elementId)}
          <div class="router-field" role="group" aria-labelledby={'router-element-' + elementIndex}>
            <p class="router-element-label" id={'router-element-' + elementIndex}>{element.section} · {element.elementId}</p>
            {#each element.variables as variable, variableIndex}
              {@const field = 'router-value-' + elementIndex + '-' + variableIndex}
              {@const invalid = fieldError?.elementId === element.elementId && fieldError?.variable === variable.name}
              <label for={field}>{variable.name}</label>
              {#if variable.type === 'object' || variable.type === 'array'}
                <textarea id={field} required={variable.required} value={values.get(key(element,variable)) ?? ''}
                  placeholder={placeholder(variable)} readonly={work === 'confirm' || unknown || element.inactive}
                  aria-describedby={field + '-hint'} aria-invalid={invalid}
                  oninput={event => assign(element,variable,event.currentTarget.value)}></textarea>
              {:else}
                <input id={field} required={variable.required} value={values.get(key(element,variable)) ?? ''}
                  placeholder={placeholder(variable)} readonly={work === 'confirm' || unknown || element.inactive}
                  aria-describedby={field + '-hint'} aria-invalid={invalid}
                  oninput={event => assign(element,variable,event.currentTarget.value)} />
              {/if}
              <p id={field + '-hint'}>{variableType[variable.type] ?? variable.type} · {element.inactive ? 'элемент отключён' : variable.required ? 'обязательное значение' : 'при пустом поле используется дефолт'}</p>
            {/each}
          </div>
        {/each}
      {/if}
      {#if message}<p class="router-error" role="alert">{message}</p>{/if}
      <div class="router-actions">
        {#if unknown}
          <button type="button" onclick={lookup} disabled={!!work || !canSubmit}>Проверить результат</button>
        {:else if conflict || failedRead}
          <button type="button" onclick={()=> { void loadSource(selected); }} disabled={!canSubmit}>{conflict ? 'Перечитать' : 'Повторить'}</button>
        {:else}
          <button type="submit" disabled={!source || !!work || !canSubmit}>
            {work === 'confirm' ? 'Проверяем конфигурацию…' : 'Проверить и подготовить'}
          </button>
        {/if}
      </div>
    </form>
  {/if}
{/if}

<style>
  .router-card {
    display: grid; gap: 12px; max-width: 700px; margin-top: 16px; padding: 20px;
    background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius);
    overflow-wrap: anywhere;
  }
  .router-card h2 { font-size: 1rem; margin: 0; }
  .router-card button { justify-self: start; width: auto; margin: 0; }
  .router-field { display: grid; gap: 8px; }
  .router-field p { font-size: .82rem; }
  .router-field select, .router-field textarea {
    font: inherit; color: var(--ink); background: var(--surface); border: 1px solid var(--line);
    border-radius: var(--radius); min-height: 42px; min-width: 0; width: 100%; padding: 10px 12px;
  }
  .router-field textarea { resize: vertical; }
  .router-field :where(input,textarea)[readonly] { background: #f3f5f7; }
  .router-field :where(select, textarea):focus-visible { outline: 2px solid #497de5; outline-offset: 3px; }
  .router-error { color: var(--error); }
  @media (max-width: 520px) { .router-card { padding: 14px; } }
</style>
