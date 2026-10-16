<script lang="ts">
  // The guest page (sign-in on, a link with #invite=): a name, the microphone check and Join; no room
  // list and no PIN. An expired or revoked link shows only that.
  import MicCheck from './MicCheck.svelte';
  import { i18n, setLang, t } from './i18n/i18n.svelte';
  import { LANGS, LANG_NAMES, type Lang } from './i18n/messages';
  import { site } from './site.svelte';

  let { expired = false, error = '', onJoin }: { expired?: boolean; error?: string; onJoin: (name: string) => void } = $props();

  let name = $state(localStorage.getItem('kunveno.name') ?? '');

  function submit(ev: Event) {
    ev.preventDefault();
    localStorage.setItem('kunveno.name', name.trim());
    onJoin(name.trim());
  }
</script>

<main class="page">
  <section class="card" aria-labelledby="guest-title">
    <header class="head">
      <p class="site">{site.title}</p>
      <label class="lang"><span class="sr">{t('footer.language')}</span>
        <select value={i18n.lang} onchange={(e) => setLang((e.currentTarget as HTMLSelectElement).value as Lang)}>
          {#each LANGS as l (l)}<option value={l}>{LANG_NAMES[l]}</option>{/each}
        </select>
      </label>
    </header>
    <h1 id="guest-title">{t('guest.title')}</h1>
    {#if expired}
      <p class="error" role="alert">{t('error.bad_invite')}</p>
    {:else}
      <form class="enter" onsubmit={submit}>
        <p class="hint">{t('guest.hint')}</p>
        {#if error}<p class="error" role="alert">{error}</p>{/if}
        <MicCheck {name} />
        <label class="field"><span>{t('enter.name')}</span><input bind:value={name} maxlength="32" autocomplete="name" required /></label>
        <button class="btn primary wide" type="submit">{t('guest.join')}</button>
      </form>
    {/if}
  </section>
</main>

<style>
  .page { min-height: 100%; display: grid; place-items: center; padding: 1.5rem 1rem; }
  .card {
    width: min(36rem, 100%); display: grid; gap: 1rem; padding: 1.5rem;
    background: var(--panel); border: 1px solid var(--line); border-radius: var(--radius);
  }
  .head { display: flex; align-items: center; justify-content: space-between; gap: 1rem; }
  .site { margin: 0; color: var(--muted); font-size: .95rem; overflow-wrap: anywhere; }
  h1 { margin: 0; font-size: 1.4rem; font-weight: 600; }
  .enter { display: grid; gap: 1.1rem; min-width: 0; }
  .hint { margin: 0; color: var(--muted); }
  .wide { justify-content: center; width: 100%; }
  .lang { flex: none; }
  .lang select { background: transparent; color: var(--muted); border: 1px solid var(--line); border-radius: var(--radius-sm); padding: .35rem .5rem; font: inherit; font-size: .9rem; cursor: pointer; }
  .sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); }
</style>
