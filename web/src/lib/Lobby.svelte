<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { build, repoUrl } from './build';
  import { forgetPin, knownRooms, pinFor } from './pins';
  import { parseJoinLink, roomLink } from './link';
  import CopyLink from './CopyLink.svelte';
  import { formatDate, i18n, setLang, t } from './i18n/i18n.svelte';
  import { LANGS, LANG_NAMES, LANG_TAGS, type Lang } from './i18n/messages';
  import { deleteNotes, fetchNotes, notesErrorText, notesUntil, saveFile } from './notes';
  import { deleteRecording, recordingErrorText, recordingLink, recordingUntil, startDownload } from './recording';
  import { loadInfo, site } from './site.svelte';
  import MicCheck from './MicCheck.svelte';

  type RoomItem = { slug: string; name: string; participants: number; capacity: number; created: boolean; notes_until?: string; recording_until?: string };
  let { onJoin, error = '' }: { onJoin: (room: string, pin: string, name: string) => void; error?: string } = $props();

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
  let loadError = $state('');
  let creating = $state(false);
  let newName = $state('');
  let newPin = $state('');
  let actionError = $state('');

  const selected = $derived(rooms.find((r) => r.slug === room));
  const selectedNotes = $derived(site.notesEnabled && selected ? notesUntil(selected.notes_until) : null);
  // The notes download and deletion: a PIN dialog for the chosen room, filled in from the PINs this
  // browser knows. Deleting asks for a confirmation inside the dialog first.
  let notesDialog: HTMLDialogElement;
  let notesForm: HTMLFormElement;
  // Focus targets: the confirmation replaces the buttons, so focus moves explicitly.
  let notesPinInput: HTMLInputElement;
  let notesDeleteButton = $state<HTMLButtonElement>();
  let notesCancelButton = $state<HTMLButtonElement>();  let notesConfirm = $state(false);
  let notesSlug = $state('');
  let notesRoomName = $state('');
  let notesKeptUntil = $state('');
  let notesPin = $state('');
  let notesError = $state('');
  let notesBusy = $state(false);
  function openNotes() {
    if (!selected) return;
    notesSlug = selected.slug;
    notesRoomName = selected.name;
    notesKeptUntil = selectedNotes ? t('notes.until', { time: untilText(selectedNotes) }) : '';
    notesPin = pinFor(localStorage, selected.slug) || pin;
    notesError = '';
    notesConfirm = false;
    notesDialog.showModal();
  }
  async function downloadNotes(ev: Event) {
    ev.preventDefault();
    if (notesBusy || notesConfirm) return;
    notesBusy = true;
    notesError = '';
    try {
      const r = await fetchNotes(fetch, notesSlug, notesPin, i18n.lang);
      if (r.ok) {
        saveFile(r.blob, r.filename);
        notesDialog.close();
      } else {
        notesError = notesErrorText(r, t);
        if (r.error === 'gone') void loadRooms(room);
      }
    } finally {
      notesBusy = false;
    }
  }
  async function askDeleteNotes() {
    if (!notesForm.reportValidity()) return;
    notesError = '';
    notesConfirm = true;
    await tick();
    notesCancelButton?.focus(); // the safe choice
  }
  async function cancelDeleteNotes() {
    notesConfirm = false;
    await tick();
    notesDeleteButton?.focus();
  }
  async function removeNotes() {
    if (notesBusy) return;
    notesBusy = true;
    notesError = '';
    try {
      const r = await deleteNotes(fetch, notesSlug, notesPin);
      if (r.ok) {
        notesDialog.close();
        void loadRooms(room);
      } else {
        notesConfirm = false;
        notesError = notesErrorText(r, t, 'notes.deleteFailed');
        if (r.error === 'gone') void loadRooms(room);
        notesBusy = false;
        await tick();
        if (r.error === 'bad_pin') notesPinInput.focus();
        else notesDeleteButton?.focus();
      }
    } finally {
      notesBusy = false;
    }
  }
  const selectedRecording = $derived(site.recordingEnabled && selected ? recordingUntil(selected.recording_until) : null);
  // The recording download and deletion: a PIN dialog for the chosen room, filled in from the PINs this
  // browser knows. Deleting asks for a confirmation inside the dialog first.
  let recordingDialog: HTMLDialogElement;
  let recordingForm: HTMLFormElement;
  // Focus targets: the confirmation replaces the buttons, so focus moves explicitly.
  let recordingPinInput: HTMLInputElement;
  let recordingDeleteButton = $state<HTMLButtonElement>();
  let recordingCancelButton = $state<HTMLButtonElement>();
  let recordingConfirm = $state(false);
  let recordingSlug = $state('');
  let recordingKeptUntil = $state('');
  let recordingPin = $state('');
  let recordingError = $state('');
  let recordingBusy = $state(false);
  function openRecording() {
    if (!selected) return;
    recordingSlug = selected.slug;
    recordingKeptUntil = selectedRecording ? t('recording.until', { time: untilText(selectedRecording) }) : '';
    recordingPin = pinFor(localStorage, selected.slug) || pin;
    recordingError = '';
    recordingConfirm = false;
    recordingDialog.showModal();
  }
  async function downloadRecording(ev: Event) {
    ev.preventDefault();
    if (recordingBusy || recordingConfirm) return;
    recordingBusy = true;
    recordingError = '';
    try {
      const r = await recordingLink(fetch, recordingSlug, recordingPin);
      if (r.ok) {
        startDownload(r.url);
        recordingDialog.close();
      } else {
        recordingError = recordingErrorText(r, t);
        if (r.error === 'gone') void loadRooms(room);
      }
    } finally {
      recordingBusy = false;
    }
  }
  async function askDeleteRecording() {
    if (!recordingForm.reportValidity()) return;
    recordingError = '';
    recordingConfirm = true;
    await tick();
    recordingCancelButton?.focus(); // the safe choice
  }
  async function cancelDeleteRecording() {
    recordingConfirm = false;
    await tick();
    recordingDeleteButton?.focus();
  }
  async function removeRecording() {
    if (recordingBusy) return;
    recordingBusy = true;
    recordingError = '';
    try {
      const r = await deleteRecording(fetch, recordingSlug, recordingPin);
      if (r.ok) {
        recordingDialog.close();
        void loadRooms(room);
      } else {
        recordingConfirm = false;
        recordingError = recordingErrorText(r, t, 'recording.deleteFailed');
        if (r.error === 'gone') void loadRooms(room);
        recordingBusy = false;
        await tick();
        if (r.error === 'bad_pin') recordingPinInput.focus();
        else recordingDeleteButton?.focus();
      }
    } finally {
      recordingBusy = false;
    }
  }
  function untilText(d: Date): string {
    return new Intl.DateTimeFormat(LANG_TAGS[i18n.lang], { dateStyle: 'short', timeStyle: 'short' }).format(d);
  }

  async function loadRooms(prefer?: string) {
    try {
      const res = await fetch('/api/rooms');
      rooms = await res.json();
      const wanted = prefer ?? sessionStorage.getItem('kunveno.room') ?? '';
      room = rooms.some((r) => r.slug === wanted) ? wanted : (rooms[0]?.slug ?? '');
      loadError = '';
    } catch {
      loadError = 'rooms.loadError';
    }
  }

  onMount(() => {
    void loadInfo();
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
    localStorage.setItem('kunveno.name', name.trim());
    sessionStorage.setItem('kunveno.room', room);
    if (fromLink) history.replaceState(null, '', '/'); // the PIN leaves the address bar
    onJoin(room, pin, name.trim());
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
      {#if selectedNotes}
        <button class="btn" type="button" aria-haspopup="dialog" onclick={openNotes}>{t('notes.last')}</button>
        <p class="hint">{t('notes.until', { time: untilText(selectedNotes) })}</p>
      {/if}
      {#if selectedRecording}
        <button class="btn" type="button" aria-haspopup="dialog" onclick={openRecording}>{t('recording.last')}</button>
        <p class="hint">{t('recording.until', { time: untilText(selectedRecording) })}</p>
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
    <MicCheck {name} />
    <div class="pair">
      <label class="field"><span>{t('enter.name')}</span><input bind:value={name} maxlength="32" autocomplete="name" required /></label>
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

  <dialog class="notes-dialog" bind:this={notesDialog} aria-labelledby="notes-title" onclose={() => (notesConfirm = false)}>
    <form bind:this={notesForm} onsubmit={downloadNotes}>
      <div class="head">
        <h2 id="notes-title">{t('notes.pinTitle', { room: notesRoomName })}</h2>
        {#if notesConfirm}
          <p class="confirm" role="alert">{t('notes.deleteConfirm')}</p>
        {:else if notesKeptUntil}
          <p class="kept">{notesKeptUntil}</p>
        {/if}
      </div>
      <label class="field"><span>{t('notes.pin')}</span>
        <input bind:this={notesPinInput} bind:value={notesPin} type="password" maxlength="16" autocomplete="off" spellcheck="false" required /></label>
      {#if notesError}<p class="error" role="alert">{notesError}</p>{/if}
      <!-- One row: the irreversible delete keeps to the far edge, away from the main action. -->
      {#if notesConfirm}
        <div class="actions">
          <button class="btn quiet" type="button" disabled={notesBusy} bind:this={notesCancelButton} onclick={cancelDeleteNotes}>{t('notes.cancel')}</button>
          <button class="btn danger solid" type="button" disabled={notesBusy} onclick={removeNotes}>{t('notes.deleteYes')}</button>
        </div>
      {:else}
        <div class="actions">
          <button class="btn quiet remove" type="button" disabled={notesBusy} bind:this={notesDeleteButton} onclick={askDeleteNotes}>{t('notes.deleteYes')}</button>
          <button class="btn quiet" type="button" onclick={() => notesDialog.close()}>{t('notes.cancel')}</button>
          <button class="btn primary" type="submit" disabled={notesBusy}>{t('notes.download')}</button>
        </div>
      {/if}
    </form>
  </dialog>

  <dialog class="recording-dialog" bind:this={recordingDialog} aria-labelledby="recording-title" onclose={() => (recordingConfirm = false)}>
    <form bind:this={recordingForm} onsubmit={downloadRecording}>
      <div class="head">
        <h2 id="recording-title">{t('recording.last')}</h2>
        {#if recordingConfirm}
          <p class="confirm" role="alert">{t('recording.deleteConfirm')}</p>
        {:else if recordingKeptUntil}
          <p class="kept">{recordingKeptUntil}</p>
        {/if}
      </div>
      <label class="field"><span>{t('recording.pin')}</span>
        <input bind:this={recordingPinInput} bind:value={recordingPin} type="password" maxlength="16" autocomplete="off" spellcheck="false" required /></label>
      {#if recordingError}<p class="error" role="alert">{recordingError}</p>{/if}
      <!-- One row: the irreversible delete keeps to the far edge, away from the main action. -->
      {#if recordingConfirm}
        <div class="actions">
          <button class="btn quiet" type="button" disabled={recordingBusy} bind:this={recordingCancelButton} onclick={cancelDeleteRecording}>{t('recording.cancel')}</button>
          <button class="btn danger solid" type="button" disabled={recordingBusy} onclick={removeRecording}>{t('recording.deleteYes')}</button>
        </div>
      {:else}
        <div class="actions">
          <button class="btn quiet remove" type="button" disabled={recordingBusy} bind:this={recordingDeleteButton} onclick={askDeleteRecording}>{t('recording.deleteYes')}</button>
          <button class="btn quiet" type="button" onclick={() => recordingDialog.close()}>{t('recording.cancel')}</button>
          <button class="btn primary" type="submit" disabled={recordingBusy}>{t('recording.download')}</button>
        </div>
      {/if}
    </form>
  </dialog>
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
  .brand { grid-column: 1 / -1; display: flex; align-items: center; gap: 1rem; }
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

  /* The footer sits at the bottom of the viewport on short pages and spans its full width. */
  .page { min-height: 100%; display: flex; flex-direction: column; }
  .lobby { flex: 1 0 auto; width: 100%; align-content: start; }
  .about {
    display: flex; flex-wrap: wrap; gap: .25rem 1.25rem; padding: .7rem 1.5rem;
    border-top: 1px solid var(--line); color: var(--muted); font-size: .8rem;
  }
  .about a { color: inherit; text-decoration: none; }
  .lang { flex: none; }
  .lang select { background: transparent; color: var(--muted); border: 1px solid var(--line); border-radius: var(--radius-sm); padding: .35rem .5rem; font: inherit; font-size: .9rem; cursor: pointer; }
  .lang select:hover { color: var(--text); border-color: var(--muted); }
  .sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); }
  .about a:hover { color: var(--text); text-decoration: underline; }
  .about span { font-variant-numeric: tabular-nums; }

  @media (max-width: 760px) {
    .lobby { grid-template-columns: 1fr; }
    .pair { grid-template-columns: 1fr; }
  }
  .notes-dialog {
    width: min(26rem, 92vw); padding: 1.25rem 1.5rem; border: 1px solid var(--line); border-radius: var(--radius);
    background: var(--panel-2); color: var(--text);
  }
  .notes-dialog::backdrop { background: rgba(0, 0, 0, .55); }
  .notes-dialog form { display: grid; gap: 1rem; }
  .notes-dialog .head { display: grid; gap: .3rem; }
  .notes-dialog h2 { color: var(--text); font-size: 1.1rem; margin: 0; }
  .notes-dialog .kept { color: var(--muted); font-size: .85rem; margin: 0; }
  .notes-dialog .confirm { color: var(--text); margin: 0; }
  .notes-dialog .actions {
    display: flex; flex-wrap: wrap; justify-content: flex-end; align-items: center; gap: .5rem;
    padding-top: 1rem; border-top: 1px solid var(--line);
  }
  /* Delete reads as a quiet red word at the far edge; its confirmation is the filled red button. */
  .notes-dialog .remove { margin-right: auto; border-color: transparent; color: #f1b5b3; padding-inline: .5rem; }
  .notes-dialog .remove:hover:not(:disabled) { border-color: var(--danger); }
  .notes-dialog .danger.solid { background: var(--danger); color: #fff; }
  .recording-dialog {
    width: min(26rem, 92vw); padding: 1.25rem 1.5rem; border: 1px solid var(--line); border-radius: var(--radius);
    background: var(--panel-2); color: var(--text);
  }
  .recording-dialog::backdrop { background: rgba(0, 0, 0, .55); }
  .recording-dialog form { display: grid; gap: 1rem; }
  .recording-dialog .head { display: grid; gap: .3rem; }
  .recording-dialog h2 { color: var(--text); font-size: 1.1rem; margin: 0; }
  .recording-dialog .kept { color: var(--muted); font-size: .85rem; margin: 0; }
  .recording-dialog .confirm { color: var(--text); margin: 0; }
  .recording-dialog .actions {
    display: flex; flex-wrap: wrap; justify-content: flex-end; align-items: center; gap: .5rem;
    padding-top: 1rem; border-top: 1px solid var(--line);
  }
  /* Delete reads as a quiet red word at the far edge; its confirmation is the filled red button. */
  .recording-dialog .remove { margin-right: auto; border-color: transparent; color: #f1b5b3; padding-inline: .5rem; }
  .recording-dialog .remove:hover:not(:disabled) { border-color: var(--danger); }
  .recording-dialog .danger.solid { background: var(--danger); color: #fff; }
</style>
