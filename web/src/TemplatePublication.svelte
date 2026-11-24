<script lang="ts">
  import { tick, untrack } from 'svelte';

  type Member = {routerId:string; name?:string; contact?:string; desiredRevision:string;
    changes?:{name:string; kind:string}[]; estimate?:string; changed?:boolean};
  type Reply = {state:string; templateId:string; version?:string; acceptedVersion:string;
    members:Member[]; receipt?:string};
  type Step = 'idle' | 'preview' | 'confirm' | 'published' | 'current' | 'stale' | 'error' | 'unknown';
  type Request = 'preview' | 'confirm' | 'current' | 'refresh';
  let {id, active, sessionEpoch, csrf, signedOut, refreshSession, back}: {
    id:string; active:boolean; sessionEpoch:number; csrf:string;
    signedOut:()=>void; refreshSession:()=>void; back:(event:MouseEvent,path:string)=>void;
  } = $props();

  const editorPath = $derived('/ui/templates/item?' + new URLSearchParams({templateId:id}));
  const endpoint = $derived('/ui/v1/templates/publication?' + new URLSearchParams({templateId:id}));
  let step = $state<Step>('idle');
  let work = $state<Request>();
  let plan = $state<Reply>();
  let result = $state<Reply>();
  let names = $state(new Map<string,string>());
  let notice = $state('');
  let continueButton = $state<HTMLButtonElement>();
  let confirmButton = $state<HTMLButtonElement>();
  let nextButton = $state<HTMLButtonElement>();
  let editorLink = $state<HTMLAnchorElement>();
  let operation: AbortController | undefined;
  let initialized = false;
  let previousEpoch: number | undefined;

  const canonical = (value:string | undefined) => /^[1-9][0-9]*$/.test(value ?? '');
  const matching = (body:Reply, state:string) => body.state === state && body.templateId === id && Array.isArray(body.members);

  function discard() {
    const unresolved = work === 'confirm' || step === 'unknown';
    operation?.abort();
    operation = undefined;
    work = undefined;
    plan = undefined;
    result = undefined;
    names = new Map();
    if (unresolved) unknown();
    else {
      step = 'idle';
      notice = '';
    }
  }

  $effect(() => {
    const permitted = active;
    const epoch = sessionEpoch;
    untrack(() => {
      if (!permitted || epoch !== previousEpoch) discard();
      previousEpoch = epoch;
      if (permitted && !initialized) {
        initialized = true;
        const saved = new URLSearchParams(location.search).get('version') ?? undefined;
        if (canonical(saved)) void run('preview',saved);
      }
    });
  });
  $effect(() => () => { discard(); });

  function refuse(status:number, code:string, kind:Request) {
    if (status === 401 && code === 'signed_out') {
      signedOut();
      return;
    }
    if (status === 403 && code === 'invalid_csrf') {
      refreshSession();
      notice = 'Состояние сессии изменилось. Подготовьте новый предпросмотр.';
      step = 'stale';
      return;
    }
    if (kind === 'current') {
      step = 'unknown';
      notice = 'Не удалось прочитать текущее состояние. Повторите проверку.';
      return;
    }
    if (status === 409 && code === 'preview_changed') {
      step = 'stale';
      notice = 'Настройки или состав изменились. Подготовьте новый предпросмотр.';
      return;
    }
    step = 'error';
    switch (code) {
      case 'catalog_rejected':
        notice = 'Xray отклонил каталог одного из роутеров. Публикация недоступна.';
        break;
      case 'validator_unavailable':
        notice = 'Проверка Xray сейчас недоступна.';
        break;
      case 'bypass_unavailable':
        notice = 'Обход для указанных адресов обеспечить нельзя.';
        break;
      case 'catalog_unready':
        notice = 'Не удалось собрать полный каталог для публикации.';
        break;
      case 'template_unavailable':
        notice = 'Шаблон недоступен.';
        break;
      default:
        if (kind === 'confirm') unknown();
        else notice = 'Не удалось прочитать состояние публикации.';
    }
  }

  function unknown() {
    step = 'unknown';
    notice = 'Не удалось подтвердить результат.';
  }

  async function run(kind: Request, version?: string) {
    if (!active || !id || work || (kind === 'confirm' && (step !== 'confirm' || !plan?.receipt))) return;
    const controller = new AbortController();
    const epoch = sessionEpoch;
    const receipt = plan?.receipt;
    const memberNames = plan?.members.map(member => [member.routerId, member.name || member.routerId] as const) ?? [];
    operation = controller;
    work = kind;
    notice = '';
    plan = undefined;
    result = undefined;
    names = new Map();
    const current = () => operation === controller && active && epoch === sessionEpoch;
    const send = async (data?: object) => {
      const response = await fetch(endpoint,{
        cache:'no-store', credentials:'same-origin', redirect:'error',
        signal:AbortSignal.any([controller.signal,AbortSignal.timeout(data ? 95000 : 10000)]),
        ...(data ? {method:'POST', headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:JSON.stringify(data)} : {}),
      });
      return {status:response.status, ok:response.ok, body:await response.json() as Reply};
    };
    try {
      if (kind === 'refresh') {
        const latest = await send();
        if (!current()) return;
        if (!latest.ok || !matching(latest.body,'current') || !canonical(latest.body.version)) {
          refuse(latest.status,latest.body.state,kind);
          return;
        }
        version = latest.body.version;
      }
      const reply = await send(kind === 'confirm' ? {action:'confirm',receipt}
        : kind === 'current' ? undefined : {action:'preview',version});
      if (!current()) return;
      if (!reply.ok) {
        refuse(reply.status,reply.body.state,kind);
      } else if (kind === 'confirm' && matching(reply.body,'published')) {
        names = new Map(memberNames);
        result = reply.body;
        step = 'published';
      } else if (kind === 'current' && matching(reply.body,'current')) {
        result = reply.body;
        step = 'current';
      } else if ((kind === 'preview' || kind === 'refresh') && matching(reply.body,'preview') && reply.body.receipt &&
          reply.body.version === version) {
        plan = reply.body;
        step = 'preview';
      } else if (kind === 'confirm') {
        unknown();
      } else {
        notice = 'Не удалось прочитать состояние публикации.';
        step = 'error';
      }
    } catch {
      if (!current()) return;
      if (kind === 'confirm' || kind === 'current' && step === 'unknown') unknown();
      else {
        notice = 'Не удалось прочитать состояние публикации.';
        step = 'error';
      }
    } finally {
      if (operation === controller) {
        operation = undefined;
        work = undefined;
        await tick();
        if (active && epoch === sessionEpoch && document.activeElement === document.body) {
          (continueButton ?? nextButton ?? editorLink)?.focus();
        }
      }
    }
  }

  async function goToConfirmation() {
    if (step !== 'preview' || !plan?.receipt) return;
    step = 'confirm';
    await tick();
    confirmButton?.focus();
  }

  async function cancelConfirmation() {
    step = 'preview';
    await tick();
    continueButton?.focus();
  }

  const presence = (value?:string) => value === 'online' ? 'На связи' : value === 'offline' ? 'Нет связи' : 'Контакт не подтверждён';
  const estimate = (value?:string) => value === 'hot_rules_possible' ? 'Горячая замена возможна при проверках агента.'
    : value === 'restart_expected' ? 'Ожидается перезапуск. Способ проверит агент.' : 'Файлы действующего каталога совпадают.';
  const changeWord = (value:string) => value === 'added' ? 'Добавлен' : value === 'removed' ? 'Удалён' : 'Изменён';
</script>

<svelte:window onpagehide={discard} />

<h1>Последствия публикации</h1>
<section class="publication" aria-label="Общая публикация шаблона" aria-busy={!!work}>
  {#if work}
    <p role="status">{work === 'confirm' ? 'Проверяем и сохраняем публикацию…' : work === 'current' ? 'Читаем текущее состояние…' : 'Готовим предпросмотр для всех назначений…'}</p>
  {:else}
    {#if notice}<p role="alert">{notice}</p>{/if}
    {#if (step === 'preview' || step === 'confirm') && plan}
      <p>Шаблон <bdi>{id}</bdi> · сохранённая версия {plan.version}</p>
      {#if step === 'preview'}
        {#if plan.members.length === 0}
          <p>Активных назначений пока нет. Можно принять общую версию.</p>
        {:else}
          <ul class="members">
            {#each plan.members as member (member.routerId)}
              <li>
                <strong><bdi>{member.name || member.routerId}</bdi></strong>
                <p>{presence(member.contact)}</p>
                {#if member.changes?.length}
                  <ul>
                    {#each member.changes as file (file.name)}
                      <li>{changeWord(file.kind)} <bdi>{file.name}</bdi></li>
                    {/each}
                  </ul>
                {/if}
                <p>{estimate(member.estimate)}</p>
              </li>
            {/each}
          </ul>
        {/if}
        <button bind:this={continueButton} onclick={goToConfirmation}>Перейти к подтверждению</button>
      {:else}
        <p>Принять сохранённую версию {plan.version} для {plan.members.length} действующих назначений?</p>
        <div class="actions">
          <button bind:this={confirmButton} onclick={() => run('confirm')}>Подтвердить публикацию</button>
          <button class="secondary" onclick={cancelConfirmation}>Отмена</button>
        </div>
      {/if}
    {:else if (step === 'published' || step === 'current') && result}
      {#if result.acceptedVersion === '0'}
        <p role="status">Принятого снимка пока нет.</p>
      {:else}
        <p role="status">{step === 'published' ? 'Снимок выпуска принят' : 'Текущее принятое состояние'} · версия {result.acceptedVersion}</p>
      {/if}
      {#if result.members.length}
        <ul class="members">
          {#each result.members as member (member.routerId)}
            <li><bdi>{member.name || names.get(member.routerId) || member.routerId}</bdi> · желаемая ревизия {member.desiredRevision}</li>
          {/each}
        </ul>
        <p>Подтверждение применения появится после отчёта агента.</p>
      {/if}
    {:else if step === 'unknown'}
      <button bind:this={nextButton} onclick={() => run('current')}>Проверить состояние</button>
    {:else}
      <button bind:this={nextButton} onclick={() => run('refresh')}>{step === 'idle' ? 'Подготовить предпросмотр' : 'Подготовить снова'}</button>
    {/if}
  {/if}
  <a bind:this={editorLink} href={editorPath} onclick={(event) => back(event,editorPath)}>К шаблону</a>
</section>

<style>
  .publication { display:grid; gap:.75rem; min-width:0; max-width:58rem; border:1px solid var(--line); background:var(--surface); padding:1rem; border-radius:var(--radius); }
  .publication > button { justify-self:start; }
  .actions { display:flex; flex-wrap:wrap; gap:.5rem; }
  .secondary { background:var(--surface); color:var(--ink); border:1px solid var(--line); }
  .members { display:grid; gap:.65rem; list-style:none; margin:0; padding:0; }
  .members > li { border:1px solid var(--line); border-radius:var(--radius); padding:.75rem; }
  .members ul { margin:.3rem 0; padding-inline-start:1.2rem; }
  p,li { overflow-wrap:anywhere; }
</style>
