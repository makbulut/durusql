<script>
  // In-app confirmation dialog (replaces window.confirm) with an optional "Don't ask again".
  import { onMount } from 'svelte'
  export let dlg   // { title, message, ok, cancel, danger, dontAskKey, resolve(bool) }
  let dontAsk = false, okBtn
  onMount(() => okBtn?.focus())
  function done(v) {
    if (v && dontAsk && dlg.dontAskKey) { try { localStorage.setItem('durusql.noConfirm.' + dlg.dontAskKey, '1') } catch {} }
    dlg.resolve(v)
  }
</script>

<svelte:window on:keydown={e => { if (e.key === 'Escape') { e.preventDefault(); done(false) } else if (e.key === 'Enter') { e.preventDefault(); done(true) } }} />
<div class="backdrop" role="presentation">
  <div class="modal" role="alertdialog" aria-modal="true">
    {#if dlg.title}<h3>{dlg.title}</h3>{/if}
    <p class="msg">{dlg.message}</p>
    {#if dlg.dontAskKey}
      <label class="check"><input type="checkbox" bind:checked={dontAsk} /> Don't ask again</label>
    {/if}
    <div class="actions">
      <button on:click={() => done(false)}>{dlg.cancel || 'Cancel'}</button>
      <button class="primary" class:danger={dlg.danger} bind:this={okBtn} on:click={() => done(true)}>{dlg.ok || 'OK'}</button>
    </div>
  </div>
</div>

<style>
  .backdrop { position: fixed; inset: 0; background: rgba(0,0,0,.5); display: grid; place-items: center; z-index: 1100; }
  .modal { background: var(--bg2); border: 1px solid var(--line); padding: 16px 20px; width: 440px; max-width: 92vw; box-shadow: 0 12px 32px rgba(0,0,0,.5); }
  h3 { margin: 0 0 8px; font-size: 14px; }
  .msg { margin: 0; white-space: pre-wrap; line-height: 1.5; }
  .check { display: flex; align-items: center; gap: 8px; margin: 12px 0 0; color: var(--fg2); font-size: 12px; }
  .check input { width: auto; }
  .actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 16px; }
  .danger { background: #c42b1c; border-color: #c42b1c; }
</style>
