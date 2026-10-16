<script lang="ts">
  import { onMount } from 'svelte';
  import { build, repoUrl } from './build';
  import { forgetPin, knownRooms, pinFor } from './pins';
  import { parseJoinLink, roomLink } from './link';
  import CopyLink from './CopyLink.svelte';
  import { formatDate, i18n, setLang, t } from './i18n/i18n.svelte';
  import { LANGS, LANG_NAMES, LANG_TAGS, type Lang } from './i18n/messages';
  import { notesUntil } from './notes';
  import { recordingUntil } from './recording';
  import { formatSummary, formatUntil, lastCaption, lastItems, lastSummary } from './lastMeeting';
  import LastMeetingDialog from './LastMeetingDialog.svelte';
  import { loadInfo, site } from './site.svelte';
  import MicCheck from './MicCheck.svelte';
  import { fixedName, signOut, type Me } from './auth';

  type RoomItem = { slug: string; name: string; participants: number; capacity: number; created: boolean; notes_until?: string; recording_until?: string; summary?: string; summary_until?: string; last_meeting?: unknown };
  // me: the signed-in person (sign-in on), whose name is fixed by the account; onAuthRequired: the
  // server asked for a sign-in (the session ended), so the sign-in screen comes back.
  let { onJoin, error = '', me = null, onAuthRequired = () => {} }: {
    onJoin: (room: string, pin: string, name: string) => void; error?: string; me?: Me | null; onAuthRequired?: () => void;
  } = $props();

  let rooms = $state<RoomItem[]>([]);
  let room = $state('');
  // The PIN field follows the chosen room: the PIN this browser last entered it with, if any.
  let pin = $state('');
  let saved = $state<string[]>(knownRooms(localStorage));
  let showPin = $state(false);
  // Opened through a room link: that room is chosen and its PIN filled in, ahead of a remembered one.
  const fromLink = parseJoinLink(location.pathname, location.hash);
  let linkRoomMissing = $state(false);
  $effect(() => {
    pin = fromLink && room === fromLink.room && fromLink.pin ? fromLink.pin : pinFor(localStorage, room);
    showPin = false; // a newly chosen room starts hidden
  });
  let name = $state(localStorage.getItem('kunveno.name') ?? '');
  const shownName = $derived(me ? fixedName(me) : name);
  let loadError = $state('');
  let creating = $state(false);
  let newName = $state('');
  let newPin = $state('');
  let actionError = $state('');

  const selected = $derived(rooms.find((r) => r.slug === room));
  // What is left of the chosen room's last meeting: one button and one PIN dialog for the video
  // and the notes, filled in from the PINs this browser knows.
  const lastKept = $derived(lastItems(selected, site));
  const lastNote = $derived(lastCaption(lastKept));
  // When, how long and how many people (no names); absent for meetings saved before summaries.
  const lastMeet = $derived(lastSummary(selected?.last_meeting));
  const lastLine = $derived(lastMeet ? formatSummary(lastMeet, LANG_TAGS[i18n.lang], t) : '');
  let lastDialog: LastMeetingDialog;
  function openLast() {
    if (!selected) return;
    lastDialog.open(selected.slug, selected.name, lastKept, pinFor(localStorage, selected.slug) || pin, lastMeet);
  }

  async function loadRooms(prefer?: string) {
    try {
      const res = await fetch('/api/rooms');
      if (res.status === 401) {
        onAuthRequired();
        return;
      }
      rooms = await res.json();
      const wanted = prefer ?? sessionStorage.getItem('kunveno.room') ?? '';
      room = rooms.some((r) => r.slug === wanted) ? wanted : (rooms[0]?.slug ?? '');
      loadError = '';
    } catch {
      loadError = 'rooms.loadError';
    }
  }

  onMount(() => {
    if (!site.loaded) void loadInfo(); // App loaded it at start; again only if that failed
    void loadRooms(fromLink?.room).then(() => {
      if (fromLink && room !== fromLink.room) linkRoomMissing = true;
    });
    // Keep occupancy fresh while the lobby is open.
    const timer = setInterval(() => void loadRooms(room), 5000);
    return () => clearInterval(timer);
  });

  async function createRoom(ev: Event) {
    ev.preventDefault();
    actionError = '';
    const res = await fetch('/api/rooms', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: newName.trim(), pin: newPin }),
    });
    if (res.status === 401) {
      onAuthRequired();
      return;
    }
    if (!res.ok) {
      actionError = t('rooms.createFailed');
      return;
    }
    const made: { slug: string } = await res.json();
    pin = newPin;
    newName = '';
    newPin = '';
    creating = false;
    await loadRooms(made.slug);
  }

  async function deleteRoom() {
    if (!selected) return;
    actionError = '';
    const res = await fetch(`/api/rooms/${encodeURIComponent(selected.slug)}`, {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ pin }),
    });
    if (res.status === 401) {
      onAuthRequired();
      return;
    }
    if (!res.ok) {
      actionError = res.status === 403 ? t('rooms.deleteNeedsPin')
        : res.status === 429 ? t('pin.tooMany', { seconds: res.headers.get('Retry-After') ?? 60 })
        : t('rooms.deleteFailed');
      return;
    }
    await loadRooms();
  }

  function submit(ev: Event) {
    ev.preventDefault();
    if (!me) localStorage.setItem('kunveno.name', name.trim()); // an account's name is not this browser's
    sessionStorage.setItem('kunveno.room', room);
    if (fromLink) history.replaceState(null, '', '/'); // the PIN leaves the address bar
    onJoin(room, pin, shownName.trim());
  }

  function forgetThisPin() {
    forgetPin(localStorage, room);
    saved = knownRooms(localStorage);
    pin = '';
  }

  function occupancy(r: RoomItem): string {
    return r.participants === 0 ? t('rooms.empty') : t('rooms.occupancy', { n: r.participants, capacity: r.capacity });
  }
</script>

<div class="page">
<div class="lobby">
  <header class="brand">
    <svg viewBox="0 0 40 40" aria-hidden="true"><circle cx="20" cy="20" r="17" /><circle cx="20" cy="20" r="9" /><path d="M20 3v8M20 29v8M3 20h8M29 20h8" /><circle class="dot" cx="20" cy="20" r="2.5" /></svg>
    <h1>{site.title}</h1>
    <label class="lang"><span class="sr">{t('footer.language')}</span>
      <select value={i18n.lang} onchange={(e) => setLang((e.currentTarget as HTMLSelectElement).value as Lang)}>
        {#each LANGS as l (l)}<option value={l}>{LANG_NAMES[l]}</option>{/each}
      </select>
    </label>
    {#if me}
      <span class="me" title={t('auth.signedInAs', { name: me.login })}>{me.name || me.login}</span>
      <button class="btn quiet" type="button" onclick={() => void signOut()}>{t('auth.signOut')}</button>
    {/if}
  </header>

  <section class="rooms" aria-labelledby="rooms-title">
    <h2 id="rooms-title">{t('rooms.title')}</h2>
    {#if loadError}<p class="error">{t('rooms.loadError')}</p>{/if}
    {#if linkRoomMissing}<p class="error">{t('rooms.linkRoomMissing')}</p>{/if}
    <div class="list" role="radiogroup" aria-labelledby="rooms-title">
      {#each rooms as r (r.slug)}
        <label class="room" class:selected={r.slug === room}>
          <input type="radio" name="room" value={r.slug} bind:group={room} />
          <span class="live" class:on={r.participants > 0} aria-hidden="true"></span>
          <span class="room-name">{r.name}</span>
          <span class="count">
            {#if saved.includes(r.slug)}<svg class="key" viewBox="0 0 24 24" role="img" aria-label={t('rooms.pinKnown')}><title>{t('rooms.pinKnown')}</title><circle cx="8" cy="12" r="4" /><path d="M12 12h9M18 12v3M21 12v2" /></svg>{/if}
            {#if site.recordingEnabled && recordingUntil(r.recording_until)}<svg class="key" viewBox="0 0 24 24" role="img" aria-label={t('recording.last')}><title>{t('recording.last')}</title><circle cx="12" cy="12" r="8" /><circle cx="12" cy="12" r="3" fill="currentColor" /></svg>{/if}
            {#if site.notesEnabled && notesUntil(r.notes_until)}<svg class="key" viewBox="0 0 24 24" role="img" aria-label={t('notes.last')}><title>{t('notes.last')}</title><path d="M6 3h9l3 3v15H6zM9 9h6M9 13h6M9 17h4" /></svg>{/if}
            {occupancy(r)}
          </span>
        </label>
      {/each}
      {#if creating}
        <form class="create" onsubmit={createRoom}>
          <label class="field"><span>{t('rooms.name')}</span><input bind:value={newName} maxlength="48" required /></label>
          <label class="field"><span>{t('rooms.pinRule')}</span><input bind:value={newPin} minlength="6" maxlength="16" required /></label>
          <div class="row">
            <button class="btn primary" type="submit">{t('rooms.create')}</button>
            <button class="btn quiet" type="button" onclick={() => (creating = false)}>{t('rooms.cancel')}</button>
          </div>
        </form>
      {:else}
        <button class="room add" type="button" onclick={() => (creating = true)}>
          <span class="plus" aria-hidden="true">+</span><span class="room-name">{t('rooms.new')}</span>
        </button>
      {/if}
    </div>
    {#if actionError}<p class="error">{actionError}</p>{/if}
    <!-- What to do with the chosen room: share it, or delete one created in the lobby. -->
    <div class="room-actions">
      <CopyLink link={roomLink(location.origin, room, pin)} label={t('enter.copyLink')} disabled={!selected || pin === ''} />
      <p class="hint">{t('enter.linkHint')}</p>
      {#if lastNote}
        <button class="btn" type="button" aria-haspopup="dialog" aria-describedby="last-hint" onclick={openLast}>{t('last.title')}</button>
        <p class="hint" id="last-hint">
          {#if lastLine}<span class="line">{lastLine}</span>{/if}
          <span class="line">{t(lastNote.key, { time: formatUntil(lastNote.until, LANG_TAGS[i18n.lang]) })}</span>
        </p>
      {/if}
      {#if selected?.created}
        <button class="btn danger" type="button" disabled={selected.participants > 0} onclick={deleteRoom}>
          {selected.participants > 0 ? t('rooms.deleteOnlyEmpty') : t('rooms.delete')}
        </button>
      {/if}
    </div>
  </section>

  <section class="join" aria-labelledby="join-title">
  <h2 id="join-title">{t('enter.title')}</h2>
  <form class="enter" onsubmit={submit}>
    {#if error}<p class="error">{error}</p>{/if}
    <MicCheck name={shownName} />
    <div class="pair">
      {#if me}
        <label class="field"><span>{t('enter.name')}</span><input value={shownName} readonly /></label>
      {:else}
        <label class="field"><span>{t('enter.name')}</span><input bind:value={name} maxlength="32" autocomplete="name" required /></label>
      {/if}
      <label class="field"><span class="pin-label">{t('enter.pin')}
        {#if saved.includes(room)}<button type="button" class="link" onclick={forgetThisPin}>{t('enter.forgetPin')}</button>{/if}</span>
        <span class="pin-box">
          <input bind:value={pin} type={showPin ? 'text' : 'password'} maxlength="16" autocomplete="new-password" spellcheck="false" required />
          <button type="button" class="eye" aria-pressed={showPin} aria-label={showPin ? t('enter.hidePin') : t('enter.showPin')}
            title={showPin ? t('enter.hidePin') : t('enter.showPin')} onclick={() => (showPin = !showPin)}>
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M2 12s3.5-6 10-6 10 6 10 6-3.5 6-10 6S2 12 2 12z" /><circle cx="12" cy="12" r="2.5" />{#if showPin}<path d="M3 3l18 18" />{/if}</svg>
          </button>
        </span></label>
    </div>
    <button class="btn primary wide" type="submit" disabled={!selected}>
      {selected ? t('enter.join', { room: selected.name }) : t('enter.chooseRoom')}
    </button>
  </form>
  </section>

  <LastMeetingDialog bind:this={lastDialog} onchange={() => void loadRooms(room)} />
</div>

<footer class="about">
  <a href={repoUrl} target="_blank" rel="noopener noreferrer">kunveno</a>
  <span>{t('footer.version', { version: build.version })}</span>
  {#if build.iso}<span>{t('footer.built', { date: formatDate(build.iso) })}</span>{/if}
</footer>
</div>

<style>
  .lobby {
    max-width: 66rem; margin: 0 auto; padding: clamp(1rem, 3vh, 3.5rem) 1.5rem;
    display: grid; grid-template-columns: 19rem minmax(0, 1fr); gap: 1.5rem 2.5rem; align-items: start;
  }
  .brand { grid-column: 1 / -1; display: flex; flex-wrap: wrap; align-items: center; gap: 1rem; }
  .brand svg { width: 2.25rem; height: 2.25rem; fill: none; stroke: var(--muted); stroke-width: 1.5; flex: none; }
  .brand .dot { fill: var(--live); stroke: none; }
  h1 { flex: 1; font-size: clamp(1.35rem, 2.4vw, 1.6rem); font-weight: 600; line-height: 1.15; margin: 0; letter-spacing: -0.01em; }
  h2 { font-size: 1rem; font-weight: 500; color: var(--muted); margin: 0 0 .75rem; }

  .list { display: grid; border: 1px solid var(--line); border-radius: var(--radius); overflow: hidden; background: var(--panel); }
  .room {
    display: grid; grid-template-columns: .75rem 1fr auto; align-items: center; gap: .75rem;
    padding: .85rem 1rem; border: 0; border-bottom: 1px solid var(--line); background: none; text-align: left; cursor: pointer;
  }
  .room:last-child { border-bottom: 0; }
  .room input { position: absolute; opacity: 0; pointer-events: none; }
  .room:has(input:focus-visible) { outline: 2px solid var(--live); outline-offset: -2px; }
  .room.selected { background: var(--panel-2); box-shadow: inset 3px 0 0 var(--live); }
  .room-name { font-weight: 500; }
  .count { color: var(--muted); font-variant-numeric: tabular-nums; font-size: .9rem; display: inline-flex; align-items: center; gap: .4rem; }
  .key { width: .95rem; height: .95rem; fill: none; stroke: var(--muted); stroke-width: 2; stroke-linecap: round; }
  .pin-box { position: relative; display: block; }
  .pin-box input { padding-right: 2.6rem; }
  .eye {
    position: absolute; right: .35rem; top: 50%; transform: translateY(-50%); width: 2rem; height: 2rem;
    display: grid; place-items: center; border: 0; border-radius: var(--radius-sm); background: transparent; color: var(--muted); cursor: pointer;
  }
  .eye:hover { color: var(--text); }
  .eye svg { width: 1.15rem; height: 1.15rem; fill: none; stroke: currentColor; stroke-width: 1.8; stroke-linecap: round; stroke-linejoin: round; }
  .pin-label { display: flex; justify-content: space-between; align-items: baseline; }
  .link { background: none; border: 0; padding: 0; color: var(--muted); font: inherit; font-size: .85rem; text-decoration: underline; cursor: pointer; }
  .link:hover { color: var(--text); }
  .live { width: .55rem; height: .55rem; border-radius: 50%; background: var(--line); }
  .live.on { background: var(--live); }
  .add { color: var(--muted); }
  .add:hover { color: var(--text); }
  .plus { font-size: 1.2rem; line-height: 1; text-align: center; }
  .create { display: grid; gap: .75rem; padding: 1rem; background: var(--panel-2); }
  .row { display: flex; gap: .5rem; flex-wrap: wrap; }

  .enter {
    display: grid; gap: 1.1rem; min-width: 0; padding: 1.25rem 1.5rem 1.5rem;
    background: var(--panel); border: 1px solid var(--line); border-radius: var(--radius);
  }
  .pair { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 12rem); gap: 1rem; }
  .room-actions { display: grid; gap: .6rem; justify-items: start; margin-top: .9rem; }
  .wide { justify-content: center; width: 100%; }
  .hint { margin: 0; color: var(--muted); font-size: .85rem; }
  .hint .line { display: block; }

  /* The footer sits at the bottom of the viewport on short pages and spans its full width. */
  .page { min-height: 100%; display: flex; flex-direction: column; }
  .lobby { flex: 1 0 auto; width: 100%; align-content: start; }
  .about {
    display: flex; flex-wrap: wrap; gap: .25rem 1.25rem; padding: .7rem 1.5rem;
    border-top: 1px solid var(--line); color: var(--muted); font-size: .8rem;
  }
  .about a { color: inherit; text-decoration: none; }
  .lang { flex: none; }
  .me { color: var(--muted); font-size: .9rem; max-width: 14rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .lang select { background: transparent; color: var(--muted); border: 1px solid var(--line); border-radius: var(--radius-sm); padding: .35rem .5rem; font: inherit; font-size: .9rem; cursor: pointer; }
  .lang select:hover { color: var(--text); border-color: var(--muted); }
  .sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); }
  .about a:hover { color: var(--text); text-decoration: underline; }
  .about span { font-variant-numeric: tabular-nums; }

  @media (max-width: 760px) {
    .lobby { grid-template-columns: 1fr; }
    .pair { grid-template-columns: 1fr; }
  }
</style>
