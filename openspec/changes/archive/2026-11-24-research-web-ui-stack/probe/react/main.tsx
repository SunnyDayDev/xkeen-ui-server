import React, { useState } from 'react'
import { createRoot } from 'react-dom/client'
import './style.css'

function Probe() {
  const [rows, setRows] = useState(['Rule A', 'Rule B'])
  const [status, setStatus] = useState('Waiting')
  async function check() {
    const response = await fetch('/status-text')
    setStatus(await response.text())
  }
  return <main>
    <h1>UI stack probe</h1>
    <label htmlFor="draft">Draft JSON</label>
    <textarea id="draft" defaultValue="{}" />
    <ol id="rows">{rows.map(row => <li key={row}>{row}</li>)}</ol>
    <button id="swap" onClick={() => setRows(([a, b]) => [b, a])}>Swap rows</button>
    <button id="check" onClick={check}>Check status</button>
    <p id="status" role="status">{status}</p>
  </main>
}
createRoot(document.querySelector('#app')!).render(<Probe />)
