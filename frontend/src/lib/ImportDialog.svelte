<script>
  // Import connections from JetBrains IDEs: DataGrip's own projects, any folder with
  // PhpStorm / IntelliJ / GoLand projects (.idea/dataSources.xml), a single file, or pasted XML.
  import { createEventDispatcher } from 'svelte'
  import * as api from './api.js'
  const dispatch = createEventDispatcher()
  let paths = [], xml = '', busy = false, msg = '', err = '', result = null
  async function addFolder() { const p = await api.pickDirectory('Folder to scan for .idea projects'); if (p && !paths.includes(p)) paths = [...paths, p] }
  async function addFile() { const p = await api.pickOpenFile('Exported connections (.xml or settings .zip)', '*.xml;*.zip'); if (p && !paths.includes(p)) paths = [...paths, p] }
  async function run(datagrip = false) {
    busy = true; err = ''; msg = datagrip ? 'Importing DataGrip data sources…' : 'Importing…'; result = null
    try {
      result = datagrip ? await api.importDataGrip() : await api.importJetBrains(paths, xml)
      msg = `Imported ${result.imported} connection(s)`
      dispatch('imported', result)
    } catch (e) { err = String(e); msg = '' } finally { busy = false }
  }
</script>

<div class="backdrop" on:mousedown|self={() => !busy && dispatch('close')} role="presentation">
  <div class="modal" role="dialog" aria-label="Import connections">
    <h2>Import connections from JetBrains IDEs</h2>
    <p class="muted small">DataGrip, PhpStorm, IntelliJ, GoLand and WebStorm all keep data sources in <code>.idea/dataSources.xml</code>. SSH configs are read from every IDE installed here, and saved passwords from the system keyring.</p>

    <div class="block primary">
      <div class="row"><b>Exported file</b><span class="spacer"></span>
        <button class="primary" on:click={addFile} disabled={busy}>Choose exported file…</button>
      </div>
      <p class="muted small">
        Accepts a settings export (<b>File → Manage IDE Settings → Export Settings</b>, a <code>.zip</code>),
        a project's <code>.idea/dataSources.xml</code>, or XML saved from <b>Copy Settings</b>. SSH configs inside the
        export are used too. Passwords are not in exported files: they come from this machine's keyring when the
        data source was used here, otherwise fill them in after importing.
      </p>
      {#if paths.length}
        <ul class="paths mono">
          {#each paths as p}<li>{p}<button class="ghost" title="Remove" on:click={() => paths = paths.filter(x => x !== p)}>✕</button></li>{/each}
        </ul>
      {/if}
    </div>

    <div class="block">
      <div class="row"><b>Or scan this machine</b><span class="spacer"></span>
        <button on:click={() => run(true)} disabled={busy}>DataGrip projects</button>
        <button on:click={addFolder} disabled={busy}>Scan a folder for IDE projects…</button>
      </div>
      <p class="muted small">DataGrip: everything under ~/DataGripProjects. Folder scan: finds <code>.idea/dataSources.xml</code> of PhpStorm / IntelliJ / GoLand projects (6 levels deep, node_modules/vendor skipped), grouped by project name.</p>
    </div>

    <div class="block">
      <div class="row"><b>Pasted XML</b><span class="muted small">right-click a data source in the IDE → Copy Settings, then paste here</span></div>
      <textarea class="mono" rows="5" bind:value={xml} placeholder="<data-source source=&quot;LOCAL&quot; name=&quot;…&quot; uuid=&quot;…&quot;> … </data-source>"></textarea>
    </div>

    {#if result?.warnings?.length}
      <div class="warn"><b>Notes</b><ul>{#each result.warnings as w}<li>{w}</li>{/each}</ul></div>
    {/if}

    <div class="actions">
      <span class="msg" class:err={!!err}>{err || msg}</span>
      <button on:click={() => dispatch('close')} disabled={busy}>{result ? 'Close' : 'Cancel'}</button>
      <button class="primary" on:click={() => run(false)} disabled={busy || (!paths.length && !xml.trim())}>Import</button>
    </div>
  </div>
</div>

<style>
  .backdrop { position: fixed; inset: 0; background: rgba(0,0,0,.55); display: grid; place-items: center; z-index: 950; }
  .modal { background: var(--bg2); border: 1px solid var(--line); padding: 16px 20px; width: 760px; max-width: 96vw; max-height: 92vh; overflow: auto; }
  h2 { margin: 0 0 6px; font-size: 15px; }
  .small { font-size: 12px; }
  code { font-family: var(--mono); font-size: 11.5px; }
  .block { border: 1px solid var(--line); padding: 10px 12px; margin-top: 10px; background: var(--bg); }
  .block.primary { border-color: var(--acc); }
  .row { display: flex; align-items: center; gap: 8px; }
  .spacer { flex: 1; }
  .paths { list-style: none; margin: 8px 0 0; padding: 0; font-size: 12px; }
  .paths li { display: flex; align-items: center; gap: 6px; padding: 2px 0; }
  textarea { width: 100%; margin-top: 8px; font-size: 12px; background: var(--bg2); color: var(--fg); border: 1px solid var(--line); padding: 6px 8px; resize: vertical; }
  .warn { margin-top: 10px; font-size: 12px; max-height: 160px; overflow: auto; border: 1px solid var(--line); padding: 8px 12px; }
  .warn ul { margin: 4px 0 0; padding-left: 18px; }
  .actions { display: flex; gap: 8px; align-items: center; margin-top: 14px; }
  .msg { flex: 1; font-size: 12px; color: var(--fg2); }
  .msg.err { color: var(--err); }
</style>
