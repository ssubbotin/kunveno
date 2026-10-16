<script lang="ts">
  // Guest invitations of the current room (sign-in on): choose how long a link lasts, create it and
  // copy it, see the usable links with who made them and until when, revoke one. Every request
  // carries the PIN the room was entered with.
  import { i18n, t } from './i18n/i18n.svelte';
  import { LANG_TAGS, type Key } from './i18n/messages';
  import { formatUntil } from './lastMeeting';
  import CopyLink from './CopyLink.svelte';
  import { DEFAULT_HOURS, INVITE_HOURS, InviteFailure, createInvite, listInvites, revokeInvite, type Invite } from './invites';

  let { slug, pin }: { slug: string; pin: string } = $props();

  let dialog: HTMLDialogElement;
  let hours = $state<number>(DEFAULT_HOURS);
  let link = $state('');
  let list = $state<Invite[]>([]);
  let loaded = $state(false);
  let error = $state('');
  let busy = $state(false);

  const labels: Record<number, Key> = { 1: 'invite.duration.1h', 24: 'invite.duration.24h', 168: 'invite.duration.7d' };
  const until = (d: Date) => formatUntil(d, LANG_TAGS[i18n.lang]);

  function fail(e: unknown) {
    error = e instanceof InviteFailure ? t(e.key, e.vars) : t('invite.failed');
  }

  export function open() {
    hours = DEFAULT_HOURS;
    link = '';
    error = '';
    loaded = false;
    list = [];
    dialog.showModal();
    void refresh();
  }

  async function refresh() {
    try {
      list = await listInvites(slug, pin);
      loaded = true;
    } catch (e) {
      fail(e);
    }
  }

  async function create(ev: Event) {
    ev.preventDefault();
    if (busy) return;
    busy = true;
    error = '';
    try {
      link = (await createInvite(slug, pin, hours)).link;
      await refresh();
    } catch (e) {
      fail(e);
    } finally {
      busy = false;
    }
  }

  async function revoke(id: string) {
    if (busy) return;
    busy = true;
    error = '';
    try {
      await revokeInvite(slug, pin, id);
    } catch (e) {
      fail(e);
    } finally {
      busy = false;
    }
    await refresh();
  }
</script>

<dialog class="invite-dialog" bind:this={dialog} aria-labelledby="invite-title" aria-describedby="invite-hint">
  <form class="body" onsubmit={create}>
    <h2 id="invite-title">{t('invite.title')}</h2>
    <p class="hint" id="invite-hint">{t('invite.hint')}</p>
    <fieldset class="choice">
      <legend>{t('invite.duration')}</legend>
      {#each INVITE_HOURS as h (h)}
        <label><input type="radio" name="invite-hours" value={h} bind:group={hours} />{t(labels[h])}</label>
      {/each}
    </fieldset>
    <div class="row">
      <button class="btn primary" type="submit" disabled={busy}>{t('invite.create')}</button>
      {#if link}<CopyLink {link} label={t('enter.copyLink')} />{/if}
    </div>
    {#if error}<p class="error" role="alert">{error}</p>{/if}

    <h3>{t('invite.active')}</h3>
    {#if loaded && list.length === 0}
      <p class="hint">{t('invite.none')}</p>
    {:else}
      <ul class="links">
        {#each list as i (i.id)}
          <li>
            <span class="what">{i.createdBy}<small>{t('invite.expires', { time: until(i.expires) })}</small></span>
            <button class="btn quiet" type="button" disabled={busy} onclick={() => revoke(i.id)}>{t('invite.revoke')}</button>
          </li>
        {/each}
      </ul>
    {/if}
  </form>
  <form method="dialog" class="actions"><button class="btn">{t('invite.close')}</button></form>
</dialog>

<style>
  .invite-dialog {
    width: min(28rem, 92vw); padding: 1.25rem 1.5rem; border: 1px solid var(--line); border-radius: var(--radius);
    background: var(--panel-2); color: var(--text);
  }
  .invite-dialog::backdrop { background: rgba(0, 0, 0, .55); }
  .body { display: grid; gap: 1rem; }
  h2 { font-size: 1.1rem; font-weight: 500; margin: 0; }
  h3 { font-size: .95rem; font-weight: 500; color: var(--muted); margin: .5rem 0 0; }
  .hint { margin: 0; color: var(--muted); font-size: .9rem; }
  .error { margin: 0; }
  .choice { border: 0; margin: 0; padding: 0; display: flex; flex-wrap: wrap; gap: .5rem 1.25rem; }
  .choice legend { padding: 0; margin-bottom: .5rem; color: var(--muted); font-size: .9rem; }
  .choice label { display: flex; align-items: center; gap: .45rem; cursor: pointer; }
  .choice input { accent-color: var(--live); margin: 0; }
  .row { display: flex; flex-wrap: wrap; align-items: start; gap: .5rem; }
  .links { list-style: none; margin: 0; padding: 0; display: grid; gap: .6rem; }
  .links li { display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: center; gap: .75rem; }
  .what { display: grid; gap: .1rem; font-weight: 500; overflow-wrap: anywhere; }
  .what small { color: var(--muted); font-size: .85rem; font-weight: 400; }
  .actions { display: flex; justify-content: flex-end; margin-top: 1rem; padding-top: 1rem; border-top: 1px solid var(--line); }
</style>
