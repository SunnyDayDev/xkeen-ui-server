<script lang="ts">
  import { tick, untrack } from 'svelte';

  type Source = 'desired' | 'personal';
  type CatalogFile = { name: string; bytes: Uint8Array<ArrayBuffer>; text: string };
  type Catalog = { source: Source; acceptedVersion: string; desiredRevision: string; files: CatalogFile[] };
  type WireFile = { name: string; content: string };
  type Issue = { code: string; position: string };
  type Reply = Omit<Catalog, 'files'> & { routerId: string; files: WireFile[]; state?: string; issues?: Issue[] };

  let { id, active, sessionEpoch, signedOut, back }: {
    id: string; active: boolean; sessionEpoch: number; signedOut: () => void;
    back: (event: MouseEvent, path: string) => void;
  } = $props();

  const backPath = $derived('/ui/?' + new URLSearchParams({routerId:id}));
  let catalog = $state<Catalog>();
  let fileName = $state('');
  const chosen = $derived(catalog?.files.find(file => file.name === fileName));
  let work = $state<Source>();
  let message = $state('');
  let issues = $state<Issue[]>([]);
  let changed = $state<Source>();
  let picker = $state<HTMLSelectElement>();
  let retryButton = $state<HTMLButtonElement>();
  let desiredButton = $state<HTMLButtonElement>();
  let personalButton = $state<HTMLButtonElement>();
  let request: AbortController | undefined;
  let previousEpoch: number | undefined;
  const downloadURLs = new Set<string>();

  function clearCatalog() {
    catalog = undefined;
    fileName = '';
    for (const url of downloadURLs) URL.revokeObjectURL(url);
    downloadURLs.clear();
  }

  function discard() {
    request?.abort();
    request = undefined;
    work = undefined;
    message = '';
    issues = [];
    changed = undefined;
    clearCatalog();
  }

  $effect(() => {
    const epoch = sessionEpoch;
    const available = active;
    untrack(() => {
      if (!available || previousEpoch !== epoch) discard();
      previousEpoch = epoch;
    });
  });
  $effect(() => () => { discard(); });

  function decodeFiles(files: WireFile[]) {
    const names = new Set<string>();
    const decoder = new TextDecoder('utf-8',{fatal:true,ignoreBOM:true});
    return files.map(({name, content}) => {
      if (typeof name !== 'string' || name === '' || name === '.' || name === '..' ||
          name.includes('/') || name.includes('\\') || name.includes('\0') || names.has(name) ||
          typeof content !== 'string') {
        throw new Error('Invalid catalog file');
      }
      names.add(name);
      const bytes = Uint8Array.from(atob(content), character => character.charCodeAt(0));
      return {name, bytes, text:decoder.decode(bytes)};
    });
  }

  async function load(source: Source) {
    if (!active || !id || work === source) return;
    discard();
    const epoch = sessionEpoch;
    const controller = new AbortController();
    request = controller;
    work = source;
    try {
      const query = new URLSearchParams({routerId:id,source});
      const response = await fetch('/ui/v1/routers/catalog?' + query,{
        cache:'no-store', credentials:'same-origin', redirect:'error',
        signal:AbortSignal.any([controller.signal,AbortSignal.timeout(10000)]),
      });
      const reply = await response.json() as Reply;
      if (request !== controller || !active || epoch !== sessionEpoch) return;
      if (response.status === 401 && reply.state === 'signed_out') {
        signedOut();
      } else if (response.status === 409 && reply.state === 'catalog_changed') {
        message = 'Настройки изменились. Запросите файлы повторно.';
        changed = source;
      } else if (response.status === 409 && reply.state === 'different_template') {
        message = 'Сохранённые настройки относятся к другому шаблону.';
      } else if (response.status === 422 && reply.state === 'catalog_unready') {
        message = 'Не удалось собрать каталог.';
        issues = Array.isArray(reply.issues) ? reply.issues : [];
      } else if (response.status === 404) {
        message = 'Роутер недоступен в текущем обзоре.';
      } else if (response.ok && reply.routerId === id && reply.source === source && Array.isArray(reply.files) && reply.files.length > 0) {
        const files = decodeFiles(reply.files);
        catalog = {source, files, acceptedVersion:reply.acceptedVersion, desiredRevision:reply.desiredRevision};
        fileName = files[0].name;
      } else {
        message = 'Не удалось получить файлы. Повторите запрос.';
      }
    } catch {
      if (request === controller && active && epoch === sessionEpoch) message = 'Не удалось получить файлы. Повторите запрос.';
    } finally {
      if (request === controller) {
        request = undefined;
        work = undefined;
        await tick();
        if (active && epoch === sessionEpoch && document.activeElement === document.body) {
          (picker ?? retryButton ?? (source === 'desired' ? desiredButton : personalButton))?.focus();
        }
      }
    }
  }

  function download() {
    if (!active || !chosen) return;
    const url = URL.createObjectURL(new Blob([chosen.bytes],{type:'application/json'}));
    downloadURLs.add(url);
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = chosen.name;
    document.body.append(anchor);
    anchor.click();
    anchor.remove();
    setTimeout(() => {
      URL.revokeObjectURL(url);
      downloadURLs.delete(url);
    }, 1000);
  }
</script>

<svelte:window onpagehide={discard} />

<h1>Итоговые файлы</h1>
<section class="catalog" aria-label="Каталог Xray" aria-busy={!!work}>
  <div class="actions">
    <button bind:this={desiredButton} disabled={!active || work === 'desired'} onclick={() => load('desired')}>Действующие файлы</button>
    <button bind:this={personalButton} disabled={!active || work === 'personal'} onclick={() => load('personal')}>Собрать предпросмотр</button>
  </div>
  {#if work}<p role="status">Готовим файлы…</p>{/if}
  {#if message}<p role="alert">{message}</p>{/if}
  {#if changed}<button class="retry" bind:this={retryButton} onclick={() => changed && load(changed)}>Перечитать</button>{/if}
  {#if issues.length}
    <ul>
      {#each issues as issue}
        <li><bdi>{issue.code}</bdi>{issue.position ? ' · ' + issue.position : ''}</li>
      {/each}
    </ul>
  {/if}
  {#if catalog && chosen}
    {#if catalog.source === 'desired'}
      <p>Желаемая ревизия {catalog.desiredRevision}. Просмотр не подтверждает применения.</p>
    {:else}
      <p>Сохранённые настройки роутера · принятый снимок {catalog.acceptedVersion}</p>
      <p>Предпросмотр не проверялся Xray и не меняет желаемую ревизию.</p>
    {/if}
    <label>Файл каталога
      <select bind:this={picker} bind:value={fileName}>
        {#each catalog.files as file (file.name)}
          <option value={file.name}>{file.name}</option>
        {/each}
      </select>
    </label>
    <!-- svelte-ignore a11y_no_noninteractive_tabindex (keyboard access to the scroll area) -->
    <div class="file-content" role="region" aria-label="Содержимое файла" tabindex="0"><pre>{chosen.text}</pre></div>
    <button class="retry" onclick={download}>Скачать файл</button>
  {/if}
  <a href={backPath} onclick={(event) => back(event,backPath)}>К роутеру</a>
</section>

<style>
  .catalog { display:grid; gap:.75rem; min-width:0; max-width:64rem; border:1px solid var(--line); background:var(--surface); padding:1rem; border-radius:var(--radius); }
  .actions { display:flex; flex-wrap:wrap; gap:.5rem; }
  .retry { justify-self:start; }
  label { display:grid; gap:.4rem; }
  select { width:100%; min-width:0; max-width:28rem; padding:.65rem; background:var(--surface); color:var(--ink); border:1px solid var(--line); border-radius:var(--radius); }
  pre { margin:0; }
  .file-content { max-height:28rem; overflow:auto; background:#f3f5f7; border-radius:var(--radius); padding:.75rem; }
  p,li { overflow-wrap:anywhere; }
  select:focus-visible, .file-content:focus-visible { outline:2px solid #497de5; outline-offset:3px; }
</style>
