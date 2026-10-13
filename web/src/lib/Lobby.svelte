<script lang="ts">
  import { onMount } from 'svelte';
  import { build, repoUrl } from './build';
  import { forgetPin, knownRooms, pinFor } from './pins';
  import { formatDate, i18n, setLang, t } from './i18n/i18n.svelte';
  import { LANGS, LANG_NAMES, type Lang } from './i18n/messages';
  import { loadInfo, site } from './site.svelte';
  import MicCheck from './MicCheck.svelte';

  type RoomItem = { slug: string; name: string; participants: number; capacity: number; created: boolean };
  let { onJoin, error = '' }: { onJoin: (room: string, pin: string, name: string) => void; error?: string } = $props();

  let rooms = $state<RoomItem[]>([]);
  let room = $state('');
  // The PIN field follows the chosen room: the PIN this browser last entered it with, if any.
  let pin = $state('');
  let saved = $state<string[]>(knownRooms(localStorage));
  $effect(() => {
    pin = pinFor(localStorage, room);
  });
  let name = $state(localStorage.getItem('kunveno.name') ?? '');
  let loadError = $state('');
  let creating = $state(false);
  let newName = $state('');
  let newPin = $state('');
  let actionError = $state('');

  const selected = $derived(rooms.find((r) => r.slug === room));

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
    void loadRooms();
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
  </header>

  <section class="rooms" aria-labelledby="rooms-title">
    <h2 id="rooms-title">{t('rooms.title')}</h2>
    {#if loadError}<p class="error">{t('rooms.loadError')}</p>{/if}
    <div class="list" role="radiogroup" aria-labelledby="rooms-title">
      {#each rooms as r (r.slug)}
        <label class="room" class:selected={r.slug === room}>
          <input type="radio" name="room" value={r.slug} bind:group={room} />
          <span class="live" class:on={r.participants > 0} aria-hidden="true"></span>
          <span class="room-name">{r.name}</span>
          <span class="count">
            {#if saved.includes(r.slug)}<svg class="key" viewBox="0 0 24 24" role="img" aria-label={t('rooms.pinKnown')}><title>{t('rooms.pinKnown')}</title><circle cx="8" cy="12" r="4" /><path d="M12 12h9M18 12v3M21 12v2" /></svg>{/if}
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
  </section>

  <form class="enter" onsubmit={submit}>
    <h2>{t('enter.title')}</h2>
    {#if error}<p class="error">{error}</p>{/if}
    <MicCheck {name} />
    <div class="pair">
      <label class="field"><span>{t('enter.name')}</span><input bind:value={name} maxlength="32" autocomplete="name" required /></label>
      <label class="field"><span class="pin-label">{t('enter.pin')}
        {#if saved.includes(room)}<button type="button" class="link" onclick={forgetThisPin}>{t('enter.forgetPin')}</button>{/if}</span>
        <input bind:value={pin} type="password" maxlength="16" autocomplete="new-password" required /></label>
    </div>
    <button class="btn primary wide" type="submit" disabled={!selected}>
      {selected ? t('enter.join', { room: selected.name }) : t('enter.chooseRoom')}
    </button>
    {#if selected?.created}
      <button class="btn danger wide" type="button" disabled={selected.participants > 0} onclick={deleteRoom}>
        {selected.participants > 0 ? t('rooms.deleteOnlyEmpty') : t('rooms.delete')}
      </button>
    {/if}
  </form>
</div>

<footer class="about">
  <a href={repoUrl} target="_blank" rel="noopener noreferrer">kunveno</a>
  <span>{t('footer.version', { version: build.version })}</span>
  {#if build.iso}<span>{t('footer.built', { date: formatDate(build.iso) })}</span>{/if}
  <label class="lang"><span class="sr">{t('footer.language')}</span>
    <select value={i18n.lang} onchange={(e) => setLang((e.currentTarget as HTMLSelectElement).value as Lang)}>
      {#each LANGS as l (l)}<option value={l}>{LANG_NAMES[l]}</option>{/each}
    </select>
  </label>
</footer>
</div>

<style>
  .lobby {
    max-width: 66rem; margin: 0 auto; padding: clamp(1.5rem, 5vh, 3.5rem) 1.5rem;
    display: grid; grid-template-columns: 19rem minmax(0, 1fr); gap: 2rem 2.5rem; align-items: start;
  }
  .brand { grid-column: 1 / -1; display: flex; align-items: center; gap: 1rem; }
  .brand svg { width: 2.75rem; height: 2.75rem; fill: none; stroke: var(--muted); stroke-width: 1.5; flex: none; }
  .brand .dot { fill: var(--live); stroke: none; }
  h1 { font-size: clamp(1.5rem, 3vw, 2rem); font-weight: 600; line-height: 1.15; margin: 0; letter-spacing: -0.01em; }
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
  .enter h2 { margin: 0 0 -.25rem; }
  .wide { justify-content: center; width: 100%; }

  /* The footer sits at the bottom of the viewport on short pages and spans its full width. */
  .page { min-height: 100%; display: flex; flex-direction: column; }
  .lobby { flex: 1 0 auto; width: 100%; align-content: start; }
  .about {
    display: flex; flex-wrap: wrap; gap: .25rem 1.25rem; padding: .7rem 1.5rem;
    border-top: 1px solid var(--line); color: var(--muted); font-size: .8rem; opacity: .8;
  }
  .about a { color: inherit; text-decoration: none; }
  .lang { margin-left: auto; }
  .lang select { background: transparent; color: inherit; border: 1px solid var(--line); border-radius: var(--radius-sm); padding: .1rem .3rem; font: inherit; }
  .sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); }
  .about a:hover { color: var(--text); text-decoration: underline; }
  .about span { font-variant-numeric: tabular-nums; }

  @media (max-width: 760px) {
    .lobby { grid-template-columns: 1fr; }
    .pair { grid-template-columns: 1fr; }
  }
</style>
