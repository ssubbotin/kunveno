<script lang="ts">
  import { t } from './i18n/i18n.svelte';

  // Copies a room link to the clipboard and says so for two seconds. When the clipboard is out
  // of reach (a browser that refuses, a page without HTTPS), the link is shown in a field to copy
  // by hand.
  let { link, label, disabled = false }: { link: string; label: string; disabled?: boolean } = $props();
  let copied = $state(false);
  let manual = $state(false);
  let timer: ReturnType<typeof setTimeout> | undefined;

  async function copy() {
    manual = false;
    try {
      await navigator.clipboard.writeText(link);
      copied = true;
      clearTimeout(timer);
      timer = setTimeout(() => (copied = false), 2000);
    } catch {
      manual = true;
    }
  }
</script>

<div class="copy">
  <button type="button" class="btn quiet" {disabled} onclick={copy}>
    <svg viewBox="0 0 24 24" aria-hidden="true">
      {#if copied}<path d="M5 12l5 5L20 7" />{:else}<path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1.5 1.5" /><path d="M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1.5-1.5" />{/if}
    </svg>
    <span aria-live="polite">{copied ? t('enter.linkCopied') : label}</span>
  </button>
  {#if manual}
    <label class="field manual"><span>{t('enter.copyManually')}</span>
      <input readonly value={link} onfocus={(e) => e.currentTarget.select()} onclick={(e) => e.currentTarget.select()} /></label>
  {/if}
</div>

<style>
  .copy { display: grid; gap: .5rem; min-width: 0; }
  .copy svg { fill: none; stroke: currentColor; stroke-width: 1.8; stroke-linecap: round; stroke-linejoin: round; }
  .manual input { width: 100%; font-size: .85rem; }
</style>
