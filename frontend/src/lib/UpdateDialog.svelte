<script>
  // Settings + updates: release source, channel, check now, download & install.
  import { createEventDispatcher, onMount, onDestroy } from 'svelte'
  import * as api from './api.js'
  import { EventsOn, EventsOff, BrowserOpenURL } from '../wailsjs/runtime/runtime.js'
  export let version = ''
  export let status = null          // last check result from the app (may be null)
  const dispatch = createEventDispatcher()
  let st = null, busy = '', err = '', msg = '', progress = null, path = ''
  onMount(async () => {
    st = await api.getSettings()
    EventsOn('update:progress', p => progress = p)
    if (!status) check(true)
  })
  onDestroy(() => EventsOff('update:progress'))
  async function save() { try { await api.saveSettings(st); msg = 'Settings saved' } catch (e) { err = String(e) } }
  async function check(force = false) {
    busy = 'check'; err = ''; msg = ''
    try { await api.saveSettings(st); status = await api.checkUpdate(force); if (status.error) err = status.error; else msg = status.available ? `Version ${status.latest} is available` : `You are on the latest version (${status.latest || version})` ; dispatch('status', status) }
    catch (e) { err = String(e) } finally { busy = '' }
  }
  async function download() {
    busy = 'download'; err = ''; msg = 'Downloading…'; progress = null
    try { path = await api.downloadUpdate(status.kind); msg = 'Downloaded and verified' } catch (e) { err = String(e); msg = '' } finally { busy = '' }
  }
  async function install() {
    busy = 'install'; err = ''
    try {
      const r = await api.installUpdate(status.kind, path)
      msg = r === 'restarting' ? 'Installed, restarting…' : 'The installer / disk image was opened, finish the installation there'
    } catch (e) { err = String(e) } finally { busy = '' }
  }
  async function skip() { st.skipVersion = status.latest; await save(); status = { ...status, available: false }; dispatch('status', status); msg = `Version ${status.latest} will be skipped` }
  const pct = p => p && p.total > 0 ? Math.round(p.done * 100 / p.total) : null
  const mb = n => (n / 1048576).toFixed(1) + ' MB'
  const kindLabel = { 'linux-deb': 'Debian package (.deb, asks for your password)', 'linux-tar': 'portable binary (replaced in place)', 'windows-zip': 'Windows binary (replaced in place)', 'windows-installer': 'Windows installer', 'macos-dmg': 'macOS disk image', 'macos-zip': 'macOS app (zip)' }
</script>

<div class="backdrop" on:mousedown|self={() => !busy && dispatch('close')} role="presentation">
  <div class="modal" role="dialog" aria-label="Updates and settings">
    <h2>Updates <span class="muted small">DuruSQL {version}</span></h2>
    {#if st}
      <div class="grid">
        <label for="src">Release source</label>
        <input id="src" class="mono" bind:value={st.updateURL} placeholder="https://github.com/<owner>/durusql   or   https://example.com/durusql/" />
        <span></span><span class="hint">A GitHub repository (releases are the channel) or a URL hosting <code>stable.json</code> / <code>beta.json</code> written by <code>package.sh</code>.</span>
        <label for="ch">Channel</label>
        <select id="ch" bind:value={st.updateChannel}><option value="stable">stable — releases only</option><option value="beta">beta — includes pre-releases</option></select>
        <label></label><label class="check"><input type="checkbox" bind:checked={st.autoCheck} /> Check for updates at start and once a day</label>
      </div>
      <div class="row">
        <button on:click={() => check(true)} disabled={!!busy}>{busy === 'check' ? 'Checking…' : 'Check now'}</button>
        <button class="ghost" on:click={save} disabled={!!busy}>Save settings</button>
        {#if st.lastCheck}<span class="muted small">last check {new Date(st.lastCheck).toLocaleString()}</span>{/if}
      </div>

      {#if status && !status.error}
        <div class="rel" class:avail={status.available}>
          <div class="relh"><b>{status.available ? `Update available: ${status.latest}` : `Latest: ${status.latest || version}`}</b>{#if status.date}<span class="muted small">{status.date}</span>{/if}</div>
          {#if status.notes}<pre class="notes">{status.notes}</pre>{/if}
          {#if status.available}
            <div class="muted small">This installation: {kindLabel[status.kind] || status.kind}{status.installPath ? ` · ${status.installPath}` : ''}{status.files?.length && !status.files.includes(status.kind) ? ` · no ${status.kind} package in this release (has: ${status.files.join(', ')})` : ''}</div>
            {#if progress}<div class="bar"><div class="fill" style="width:{pct(progress) ?? 0}%"></div><span>{pct(progress) !== null ? pct(progress) + '%' : mb(progress.done)}{progress.total > 0 ? ` of ${mb(progress.total)}` : ''}</span></div>{/if}
            <div class="row">
              {#if !path}
                <button class="primary" on:click={download} disabled={!!busy || (status.files?.length && !status.files.includes(status.kind))}>{busy === 'download' ? 'Downloading…' : 'Download'}</button>
              {:else}
                <button class="primary" on:click={install} disabled={!!busy}>{busy === 'install' ? 'Installing…' : status.canSelf ? 'Install and restart' : 'Install'}</button>
              {/if}
              <button class="ghost" on:click={skip} disabled={!!busy}>Skip this version</button>
              {#if status.error === undefined && st.updateURL?.includes('github.com')}<button class="ghost" on:click={() => BrowserOpenURL(st.updateURL.replace(/\/$/, '') + '/releases')}>Open releases page</button>{/if}
            </div>
          {/if}
        </div>
      {/if}
    {/if}
    <div class="actions"><span class="msg" class:err={!!err}>{err || msg}</span><button on:click={() => dispatch('close')} disabled={!!busy}>Close</button></div>
  </div>
</div>

<style>
  .backdrop { position: fixed; inset: 0; background: rgba(0,0,0,.55); display: grid; place-items: center; z-index: 950; }
  .modal { background: var(--bg2); border: 1px solid var(--line); padding: 16px 20px; width: 680px; max-width: 96vw; max-height: 90vh; overflow: auto; }
  h2 { margin: 0 0 12px; font-size: 15px; }
  .small { font-size: 12px; }
  .grid { display: grid; grid-template-columns: 130px 1fr; gap: 8px 12px; align-items: center; }
  .grid > label { margin: 0; text-align: right; color: var(--fg); font-size: 12.5px; }
  .hint { color: var(--fg2); font-size: 11.5px; } code { font-family: var(--mono); font-size: 11px; }
  .check { display: flex; align-items: center; gap: 8px; text-align: left; } .check input { width: auto; }
  .row { display: flex; align-items: center; gap: 8px; margin-top: 10px; }
  .rel { margin-top: 14px; border: 1px solid var(--line); padding: 10px 12px; background: var(--bg); }
  .rel.avail { border-color: var(--acc); }
  .relh { display: flex; gap: 10px; align-items: baseline; }
  .notes { white-space: pre-wrap; font-size: 12px; max-height: 220px; overflow: auto; margin: 8px 0; color: var(--fg); font-family: var(--font); }
  .bar { position: relative; height: 18px; background: var(--bg3); border: 1px solid var(--line); margin-top: 8px; font-size: 11px; }
  .bar .fill { height: 100%; background: var(--acc); }
  .bar span { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; }
  .actions { display: flex; gap: 8px; align-items: center; margin-top: 14px; }
  .msg { flex: 1; font-size: 12px; color: var(--fg2); } .msg.err { color: var(--err); }
</style>
