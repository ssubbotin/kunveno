<script lang="ts">
  // The sign-in screen (sign-in on, no session): one button per provider. The server takes the
  // person to the provider and back to this page; a sign-in that failed comes back here with a
  // notice (cancelled, refused account, provider unavailable, took too long).
  import { signInURL } from './auth';
  import { i18n, setLang, t } from './i18n/i18n.svelte';
  import { LANGS, LANG_NAMES, type Lang } from './i18n/messages';
  import { requireSignIn, site } from './site.svelte';

  // notice: why the person is here again (a join refused for want of a session, or a sign-in that
  // failed).
  let { notice = '' }: { notice?: string } = $props();

  // No providers known: /api/info could not be read; Retry asks again.
  let retrying = $state(false);
  async function retry() {
    retrying = true;
    try {
      await requireSignIn();
    } finally {
      retrying = false;
    }
  }

  function go(type: string) {
    location.href = signInURL(type, location, sessionStorage);
  }
</script>

<main class="page">
  <section class="card" aria-labelledby="signin-title">
    <header class="head">
      <p class="site">{site.title}</p>
      <label class="lang"><span class="sr">{t('footer.language')}</span>
        <select value={i18n.lang} onchange={(e) => setLang((e.currentTarget as HTMLSelectElement).value as Lang)}>
          {#each LANGS as l (l)}<option value={l}>{LANG_NAMES[l]}</option>{/each}
        </select>
      </label>
    </header>
    <h1 id="signin-title">{t('auth.signInTitle')}</h1>
    {#if notice}<p class="error" role="alert">{notice}</p>{/if}
    {#if site.auth.providers.length === 0}
      <p class="error" role="alert">{t('auth.unavailable')}</p>
      <button class="btn wide" type="button" disabled={retrying} onclick={retry}>{t('auth.retry')}</button>
    {:else}
      <p class="hint">{t('auth.signInHint')}</p>
      <div class="providers">
        {#each site.auth.providers as p (p.type)}
          <button class="btn primary wide" type="button" onclick={() => go(p.type)}>{t('auth.signInWith', { provider: p.label })}</button>
        {/each}
      </div>
    {/if}
  </section>
</main>

<style>
  .page { min-height: 100%; display: grid; place-items: center; padding: 1.5rem 1rem; }
  .card {
    width: min(26rem, 100%); display: grid; gap: 1rem; padding: 1.5rem;
    background: var(--panel); border: 1px solid var(--line); border-radius: var(--radius);
  }
  .head { display: flex; align-items: center; justify-content: space-between; gap: 1rem; }
  .site { margin: 0; color: var(--muted); font-size: .95rem; overflow-wrap: anywhere; }
  h1 { margin: 0; font-size: 1.4rem; font-weight: 600; }
  .hint { margin: 0; color: var(--muted); }
  .error { margin: 0; }
  .providers { display: grid; gap: .6rem; }
  .wide { justify-content: center; width: 100%; }
  .lang { flex: none; }
  .lang select { background: transparent; color: var(--muted); border: 1px solid var(--line); border-radius: var(--radius-sm); padding: .35rem .5rem; font: inherit; font-size: .9rem; cursor: pointer; }
  .sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); }
</style>
