<script>
  import { createEventDispatcher } from 'svelte'
  import * as api from './api.js'

  export let conn = {}
  const dispatch = createEventDispatcher()

  let c = {
    id: '', name: '', group: '', driver: 'mysql', host: '127.0.0.1', port: 0,
    user: '', password: '', database: '', favorite: false, color: '#5aa8ff',
    ...structuredClone(conn),
  }
  let useSSH = !!c.ssh?.host
  let ssh = { host: '', port: 22, user: '', keyPath: '~/.ssh/id_ed25519', password: '', useAgent: true, ...(c.ssh || {}) }
  let msg = '', busy = false

  $: if (c.port === 0) c.port = c.driver === 'postgres' ? 5432 : c.driver === 'opensearch' ? 9200 : 3306
  $: isDoc = c.driver === 'opensearch'

  function payload() {
    return { ...c, port: Number(c.port), ssh: useSSH ? { ...ssh, port: Number(ssh.port) } : null }
  }
  async function test() {
    busy = true; msg = 'Testing…'
    try { await api.testConnection(payload()); msg = 'Connected OK' }
    catch (e) { msg = 'Failed: ' + e }
    finally { busy = false }
  }
  async function save() {
    busy = true; msg = ''
    try {
      const saved = await api.saveConnection(payload())
      dispatch('saved', saved)
    } catch (e) { msg = 'Failed: ' + e; busy = false }
  }
</script>

<div class="backdrop" on:click|self={() => dispatch('close')} role="presentation">
  <form class="modal" on:submit|preventDefault={save}>
    <h2>{c.id ? 'Edit connection' : 'New connection'}</h2>

    <div class="grid">
      <div><label for="name">Name</label><input id="name" bind:value={c.name} required /></div>
      <div><label for="group">Group</label><input id="group" bind:value={c.group} placeholder="prod, staging…" /></div>
      <div><label for="driver">Driver</label>
        <select id="driver" bind:value={c.driver} on:change={() => c.port = 0}>
          <option value="mysql">MySQL / MariaDB</option>
          <option value="postgres">PostgreSQL</option>
          <option value="opensearch">OpenSearch / Elasticsearch</option>
        </select></div>
      <div><label for="color">Color</label><input id="color" type="color" bind:value={c.color} /></div>
      <div class="span2"><label for="host">Host</label><input id="host" bind:value={c.host} required /></div>
      <div><label for="port">Port</label><input id="port" type="number" bind:value={c.port} /></div>
      {#if isDoc}
        <div class="doc"><label class="check inline"><input type="checkbox" bind:checked={c.tls} /> HTTPS</label><label class="check inline"><input type="checkbox" bind:checked={c.insecure} disabled={!c.tls} /> Skip certificate check</label></div>
      {:else}
        <div><label for="db">Database</label><input id="db" bind:value={c.database} /></div>
      {/if}
      <div><label for="user">User</label><input id="user" bind:value={c.user} /></div>
      <div><label for="pw">Password</label><input id="pw" type="password" bind:value={c.password} /></div>
    </div>
    {#if isDoc}<p class="muted small">Indices are listed as tables and aliases as views. The console runs SQL (via the SQL plugin) and raw REST requests such as <code>GET /index/_search</code> followed by a JSON body. Leave user empty when security is disabled.</p>{/if}

    <label class="check"><input type="checkbox" bind:checked={useSSH} /> Connect through SSH tunnel</label>
    {#if useSSH}
      <div class="grid ssh">
        <div class="span2"><label for="sh">SSH host</label><input id="sh" bind:value={ssh.host} required /></div>
        <div><label for="sp">Port</label><input id="sp" type="number" bind:value={ssh.port} /></div>
        <div><label for="su">SSH user</label><input id="su" bind:value={ssh.user} required /></div>
        <div class="span2"><label for="sk">Private key path</label><input id="sk" bind:value={ssh.keyPath} placeholder="leave empty to use agent/password" /></div>
        <div><label for="spw">Passphrase / password</label><input id="spw" type="password" bind:value={ssh.password} /></div>
        <label class="check span3"><input type="checkbox" bind:checked={ssh.useAgent} /> Use ssh-agent if available</label>
      </div>
      <p class="muted small">DB host above is resolved from the SSH server side (usually 127.0.0.1).</p>
    {/if}

    <div class="actions">
      <span class:err={msg.startsWith('Failed')} class="msg">{msg}</span>
      <button type="button" on:click={() => dispatch('close')}>Cancel</button>
      <button type="button" disabled={busy} on:click={test}>Test</button>
      <button type="submit" class="primary" disabled={busy}>Save</button>
    </div>
  </form>
</div>

<style>
  .backdrop { position: fixed; inset: 0; background: rgba(0,0,0,.55); display: grid; place-items: center; }
  .modal { background: var(--bg2); border: 1px solid var(--line); border-radius: 0; padding: 18px 20px; width: 620px; max-width: 95vw; }
  h2 { margin: 0 0 14px; font-size: 16px; }
  .grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 10px; }
  .span2 { grid-column: span 2; } .span3 { grid-column: span 3; }
  .ssh { margin-top: 8px; padding: 10px; background: var(--bg); border-radius: 0; }
  .check { display: flex; align-items: center; gap: 8px; margin: 14px 0 4px; color: var(--fg); }
  .check input { width: auto; }
  .doc { display: flex; flex-direction: column; justify-content: flex-end; gap: 2px; }
  .check.inline { margin: 0; font-size: 12px; }
  .small { font-size: 12px; margin: 6px 0 0; }
  .actions { display: flex; gap: 8px; align-items: center; margin-top: 16px; }
  .msg { flex: 1; font-size: 12px; }
</style>
