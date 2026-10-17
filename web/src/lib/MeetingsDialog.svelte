<script lang="ts">
  // The room's meetings, newest first, behind its PIN: each meeting's notes, summary and recording
  // parts, each downloaded or deleted on its own (a delete asks once, in place).
  import { i18n, t } from './i18n/i18n.svelte';
  import { LANG_TAGS } from './i18n/messages';
  import { formatSummary, formatTime, formatUntil } from './lastMeeting';
  import { fetchMeetings, type MeetingRow } from './meetings';
  import { deleteNotes, fetchNotes, notesErrorText, saveFile } from './notes';
  import { deleteRecording, recordingErrorText, recordingLink, startDownload } from './recording';
  import { deleteSummary, fetchSummary } from './summary';

  // onchange: the room's meetings changed, so the room list is stale.
  let { onchange }: { onchange: () => void } = $props();

  type Item = { kind: 'notes' | 'summary' | 'part'; part?: Date };

  let dialog: HTMLDialogElement;
  let form: HTMLFormElement;
  let pinInput: HTMLInputElement;

  let slug = $state('');
  let roomName = $state('');
  let rows = $state<MeetingRow[] | null>(null);
  let failedToLoad = $state(false);
  let pin = $state('');
  let showPin = $state(false);
  let error = $state('');
  let busy = $state(false);
  let asking = $state(''); // the item whose deletion is being confirmed

  const keyOf = (m: MeetingRow, it: Item) => `${m.start.getTime()}:${it.kind}:${it.part?.getTime() ?? ''}`;
  const locale = () => LANG_TAGS[i18n.lang];
  const until = (d: Date) => formatUntil(d, locale());

  export async function open(roomSlug: string, name: string, knownPin: string) {
    slug = roomSlug;
    roomName = name;
    pin = knownPin;
    showPin = false;
    error = '';
    asking = '';
    rows = null;
    failedToLoad = false;
    dialog.showModal();
    await reload();
  }

  async function reload() {
    const got = await fetchMeetings(fetch, slug);
    failedToLoad = got === null;
    rows = got ?? [];
  }

  function heading(m: MeetingRow): string {
    if (m.end && m.people > 0) return formatSummary({ start: m.start, end: m.end, people: m.people }, locale(), t);
    return formatUntil(m.start, locale());
  }

  async function download(m: MeetingRow, it: Item) {
    if (busy || !form.reportValidity()) return;
    busy = true;
    error = '';
    try {
      if (it.kind === 'part' && it.part) {
        const r = await recordingLink(fetch, slug, pin, { meeting: m.start, part: it.part });
        if (r.ok) return startDownload(r.url);
        error = recordingErrorText(r, t);
        if (r.error === 'gone') await changed();
      } else {
        const r = it.kind === 'notes' ? await fetchNotes(fetch, slug, pin, i18n.lang, m.start) : await fetchSummary(fetch, slug, pin, i18n.lang, m.start);
        if (r.ok) return saveFile(r.blob, r.filename);
        error = notesErrorText(r, t);
        if (r.error === 'gone') await changed();
      }
      if (error) pinInput.focus();
    } finally {
      busy = false;
    }
  }

  async function remove(m: MeetingRow, it: Item) {
    if (busy || !form.reportValidity()) return;
    busy = true;
    error = '';
    try {
      const r =
        it.kind === 'notes' ? await deleteNotes(fetch, slug, pin, m.start)
        : it.kind === 'summary' ? await deleteSummary(fetch, slug, pin, m.start)
        : await deleteRecording(fetch, slug, pin, { meeting: m.start, part: it.part! });
      if (!r.ok) {
        error = it.kind === 'part' ? recordingErrorText(r, t, 'recording.deleteFailed') : notesErrorText(r, t, it.kind === 'notes' ? 'notes.deleteFailed' : 'summary.deleteFailed');
        if (r.error === 'bad_pin') pinInput.focus();
        if (r.error !== 'gone') return;
      }
      asking = '';
      await changed();
    } finally {
      busy = false;
    }
  }

  async function changed() {
    onchange();
    await reload();
  }
</script>

{#snippet item(m: MeetingRow, it: Item, name: string, note: string, canDownload: boolean)}
  <li>
    <span class="what">{name}<small>{note}</small></span>
    {#if asking === keyOf(m, it)}
      <span class="ask">
        <span>{t('meetings.deleteAsk')}</span>
        <button class="btn quiet" type="button" disabled={busy} onclick={() => (asking = '')}>{t('last.cancel')}</button>
        <button class="btn danger solid" type="button" disabled={busy} onclick={() => remove(m, it)}>{t('last.deleteYes')}</button>
      </span>
    {:else}
      <span class="buttons">
        {#if canDownload}
          <button class="btn" type="button" disabled={busy} onclick={() => download(m, it)} aria-label="{t('last.download')}: {name}">{t('last.download')}</button>
        {/if}
        <button class="btn quiet remove" type="button" disabled={busy} onclick={() => (asking = keyOf(m, it))} aria-label="{t('last.deleteYes')}: {name}">{t('last.deleteYes')}</button>
      </span>
    {/if}
  </li>
{/snippet}

<dialog class="meetings-dialog" bind:this={dialog} aria-labelledby="meetings-title meetings-room" onclose={() => (asking = '')}>
  <form bind:this={form} onsubmit={(e) => e.preventDefault()}>
    <div class="head">
      <h2 id="meetings-title">{t('meetings.title')}</h2>
      <p class="room" id="meetings-room">{roomName}</p>
    </div>
    <label class="field"><span>{t('last.pin')}</span>
      <span class="pin-box">
        <input bind:this={pinInput} bind:value={pin} type={showPin ? 'text' : 'password'} maxlength="16" spellcheck="false" required
          autocomplete="new-password" data-1p-ignore data-lpignore="true" data-bwignore />
        <button type="button" class="eye" aria-pressed={showPin} aria-label={showPin ? t('enter.hidePin') : t('enter.showPin')}
          title={showPin ? t('enter.hidePin') : t('enter.showPin')} onclick={() => (showPin = !showPin)}>
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M2 12s3.5-6 10-6 10 6 10 6-3.5 6-10 6S2 12 2 12z" /><circle cx="12" cy="12" r="2.5" />{#if showPin}<path d="M3 3l18 18" />{/if}</svg>
        </button>
      </span></label>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <div class="list">
      {#if rows === null}
        <p class="muted">…</p>
      {:else if failedToLoad}
        <p class="error" role="alert">{t('meetings.loadFailed')}</p>
      {:else if rows.length === 0}
        <p class="muted">{t('meetings.empty')}</p>
      {:else}
        {#each rows as m (m.start.getTime())}
          <section class="meeting">
            <h3>{heading(m)}</h3>
            <ul class="items">
              {#each m.parts as p (p.start.getTime())}
                {@render item(m, { kind: 'part', part: p.start }, t('meetings.part', { time: formatTime(p.start, locale()) }), t('last.videoUntil', { time: until(p.until) }), true)}
              {/each}
              {#if m.notes}
                {@render item(m, { kind: 'notes' }, t('last.notesName'), t('last.notesUntil', { time: until(m.notes) }), true)}
              {/if}
              {#if m.summary}
                {@render item(m, { kind: 'summary' }, t('last.summaryName'),
                  m.summary.state === 'ready' ? t('last.summaryReady', { time: until(m.summary.until) }) : t(m.summary.state === 'pending' ? 'last.summaryPending' : 'last.summaryFailed'),
                  m.summary.state === 'ready')}
              {/if}
              {#if m.parts.length === 0 && !m.notes && !m.summary}
                <li class="muted">{t('meetings.nothing')}</li>
              {/if}
            </ul>
          </section>
        {/each}
      {/if}
    </div>
    <div class="actions">
      <button class="btn quiet" type="button" onclick={() => dialog.close()}>{t('last.close')}</button>
    </div>
  </form>
</dialog>

<style>
  .meetings-dialog {
    width: min(32rem, 94vw); max-height: 90vh; padding: 1.25rem 1.5rem; border: 1px solid var(--line); border-radius: var(--radius);
    background: var(--panel-2); color: var(--text);
  }
  .meetings-dialog::backdrop { background: rgba(0, 0, 0, .55); }
  form { display: grid; gap: 1rem; }
  h2 { color: var(--text); font-size: 1.1rem; font-weight: 500; margin: 0; }
  h3 { font-size: .95rem; font-weight: 500; margin: 0 0 .5rem; color: var(--text); }
  .head { display: grid; gap: .3rem; }
  .room, .muted { color: var(--muted); font-size: .9rem; margin: 0; }
  .error { margin: 0; }
  .pin-box { position: relative; display: block; }
  .pin-box input { padding-right: 2.6rem; }
  .eye {
    position: absolute; right: .35rem; top: 50%; transform: translateY(-50%); width: 2rem; height: 2rem;
    display: grid; place-items: center; border: 0; border-radius: var(--radius-sm); background: transparent; color: var(--muted); cursor: pointer;
  }
  .eye:hover { color: var(--text); }
  .eye svg { width: 1.15rem; height: 1.15rem; fill: none; stroke: currentColor; stroke-width: 1.8; stroke-linecap: round; stroke-linejoin: round; }
  .list { display: grid; gap: 1rem; max-height: 55vh; overflow-y: auto; }
  .meeting { padding-bottom: .75rem; border-bottom: 1px solid var(--line); }
  .items { list-style: none; margin: 0; padding: 0; display: grid; gap: .5rem; }
  .items li { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: .5rem; }
  .what { display: grid; gap: .1rem; font-weight: 500; }
  .what small { color: var(--muted); font-size: .85rem; font-weight: 400; }
  .buttons, .ask { display: flex; align-items: center; gap: .4rem; }
  .remove { border-color: transparent; color: #f1b5b3; padding-inline: .5rem; }
  .remove:hover:not(:disabled) { border-color: var(--danger); }
  .danger.solid { background: var(--danger); color: #fff; }
  .actions { display: flex; justify-content: flex-end; padding-top: .5rem; }
</style>
