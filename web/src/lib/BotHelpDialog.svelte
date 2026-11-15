<script lang="ts">
  import { t } from './i18n/i18n.svelte';

  // How to talk to the facilitator bot, in the bot's own words. The text comes from the bot and is
  // shown as plain text with its line breaks, never as markup. A native modal dialog keeps focus
  // inside and closes on Escape; on close focus goes back to the button that opened it.
  let { name, text, onclose }: { name: string; text: string | undefined; onclose: () => void } = $props();
  let dialog: HTMLDialogElement;
  let opener: HTMLElement | null = null;

  export function open(from: HTMLElement) {
    opener = from;
    if (!dialog.open) dialog.showModal();
  }

  // The bot left or its help went away while the dialog was open.
  $effect(() => {
    if (text === undefined && dialog?.open) dialog.close();
  });

  function closed() {
    opener?.focus();
    opener = null;
    onclose();
  }
</script>

<dialog bind:this={dialog} aria-labelledby="bot-help-title" onclose={closed}>
  <h2 id="bot-help-title">{t('bot.helpTitle', { name })}</h2>
  <p class="help">{text ?? ''}</p>
  <form method="dialog"><button class="btn">{t('bot.close')}</button></form>
</dialog>

<style>
  dialog {
    width: min(32rem, 92vw); padding: 1.25rem 1.5rem; border: 1px solid var(--line); border-radius: var(--radius);
    background: var(--panel-2); color: var(--text); text-align: left;
  }
  dialog::backdrop { background: rgba(0, 0, 0, .55); }
  h2 { font-size: 1.1rem; margin: 0 0 .75rem; }
  .help { margin: 0 0 1rem; white-space: pre-wrap; overflow-wrap: anywhere; max-height: 60vh; overflow-y: auto; }
  form { display: flex; justify-content: flex-end; }
</style>
