<script lang="ts">
  import { t } from './i18n/i18n.svelte';
  import type { Key } from './i18n/messages';
  import { shortcutLabel, type ShortcutAction } from './shortcuts';

  // The list of keyboard shortcuts. A native modal dialog keeps focus inside, closes on Escape and
  // returns focus to where it was.
  let { mac }: { mac: boolean } = $props();
  let dialog: HTMLDialogElement;
  const rows: [ShortcutAction, Key][] = [['mic', 'shortcuts.mic'], ['camera', 'shortcuts.camera'], ['help', 'shortcuts.help']];

  export function open() {
    if (!dialog.open) dialog.showModal();
  }
</script>

<dialog bind:this={dialog} aria-labelledby="shortcuts-title">
  <h2 id="shortcuts-title">{t('shortcuts.title')}</h2>
  <p>{t('shortcuts.intro')}</p>
  <table>
    <tbody>
      {#each rows as [action, key] (action)}
        <tr><th scope="row">{t(key)}</th><td><kbd>{shortcutLabel(action, mac)}</kbd></td></tr>
      {/each}
    </tbody>
  </table>
  <ul>
    <li>{t('shortcuts.sharing', { mod: mac ? '⌘' : 'Ctrl' })}</li>
    <li>{t('shortcuts.spoken')}</li>
    <li>{t('shortcuts.layout')}</li>
  </ul>
  <form method="dialog"><button class="btn">{t('shortcuts.close')}</button></form>
</dialog>

<style>
  dialog {
    width: min(32rem, 92vw); padding: 1.25rem 1.5rem; border: 1px solid var(--line); border-radius: var(--radius);
    background: var(--panel-2); color: var(--text);
  }
  dialog::backdrop { background: rgba(0, 0, 0, .55); }
  h2 { font-size: 1.1rem; margin: 0 0 .75rem; }
  table { width: 100%; border-collapse: collapse; }
  th { text-align: left; font-weight: 400; padding: .4rem 0; }
  td { text-align: right; padding: .4rem 0 .4rem 1rem; white-space: nowrap; }
  kbd { font: inherit; padding: .1rem .45rem; border: 1px solid var(--line); border-radius: var(--radius-sm); background: var(--panel); }
  p { margin: 0 0 .75rem; }
  ul { color: var(--muted); font-size: .9rem; padding-left: 1.1rem; display: grid; gap: .35rem; }
  form { display: flex; justify-content: flex-end; }
</style>
