<script lang="ts">
  import { untrack } from 'svelte';

  type Summary = { id: string; version: string };

  let { active, refreshKey, signedOut }: { active: boolean; refreshKey: number; signedOut: () => void } = $props();
  let view = $state<'loading' | 'ready' | 'unavailable'>('loading');
  let templates = $state<Summary[]>([]);
  let pending: AbortController | undefined;

  function isSummary(item: unknown): item is Summary {
    if (item === null || typeof item !== 'object') return false;
    const value = item as Partial<Summary>;
    return typeof value.id === 'string' && value.id !== '' &&
      typeof value.version === 'string' && /^[1-9][0-9]*$/.test(value.version);
  }

  async function load() {
    pending?.abort();
    const request = new AbortController();
    pending = request;
    view = 'loading';
    templates = [];

    try {
      const response = await fetch('/ui/v1/templates', {
        headers: {Accept: 'application/json'}, credentials: 'same-origin',
        cache: 'no-store', redirect: 'error',
        signal: AbortSignal.any([request.signal, AbortSignal.timeout(10000)])
      });
      const result = await response.json();
      if (request.signal.aborted || pending !== request || !active) return;
      if (response.status === 401 && result?.state === 'signed_out') {
        signedOut();
      } else if (response.ok && Array.isArray(result?.templates) && result.templates.every(isSummary)) {
        templates = result.templates;
        view = 'ready';
      } else {
        view = 'unavailable';
      }
    } catch {
      if (!request.signal.aborted && pending === request && active) view = 'unavailable';
    }
  }

  $effect(() => {
    void refreshKey;
    if (!active) {
      templates = [];
      view = 'loading';
      return;
    }
    untrack(() => { void load(); });
    return () => { pending?.abort(); };
  });
</script>

<div class="template-list" aria-busy={view === 'loading'}>
  {#if view === 'ready' && templates.length > 0}
    <ul aria-label="Сохранённые шаблоны">
      {#each templates as template (template.id)}
        <li>
          <bdi>{template.id}</bdi>
          <span>Версия {template.version}</span>
        </li>
      {/each}
    </ul>
  {:else if view === 'ready'}
    <p role="status">Шаблонов пока нет.</p>
  {:else if view === 'loading'}
    <p role="status">Загрузка шаблонов…</p>
  {:else}
    <p role="alert">Не удалось загрузить шаблоны.</p>
    <button onclick={load}>Повторить</button>
  {/if}
</div>

<style>
  .template-list {
    margin-top: 16px; padding: 18px; background: var(--surface);
    border: 1px solid var(--line); border-radius: var(--radius);
  }
  ul { list-style: none; padding: 0; margin: 0; display: grid; gap: 12px; }
  li { display: flex; flex-wrap: wrap; justify-content: space-between; gap: 4px 12px; padding: 8px 0; font-size: .92rem; }
  bdi { min-width: 0; max-width: 100%; font-weight: 600; white-space: pre-wrap; overflow-wrap: anywhere; }
  li span { color: var(--muted); }
  button { margin-top: 12px; }
  @media (max-width: 520px) {
    .template-list { padding: 14px; }
  }
</style>
