<script lang="ts">
  let rows = $state(['Rule A', 'Rule B'])
  let status = $state('Waiting')
  function swap() {
    rows = [rows[1], rows[0]]
  }
  async function check() {
    const response = await fetch('/status-text')
    status = await response.text()
  }
</script>

<main>
  <h1>UI stack probe</h1>
  <label for="draft">Draft JSON</label>
  <textarea id="draft">{'{}'}</textarea>
  <ol id="rows">{#each rows as row (row)}<li>{row}</li>{/each}</ol>
  <button id="swap" onclick={swap}>Swap rows</button>
  <button id="check" onclick={check}>Check status</button>
  <p id="status" role="status">{status}</p>
</main>
