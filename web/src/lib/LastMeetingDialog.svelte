<script lang="ts">
  // What is left of a room's last meeting, behind its PIN: download the video and the notes, or
  // delete either or both after a confirmation inside the dialog.
  import { tick } from 'svelte';
  import { i18n, t } from './i18n/i18n.svelte';
  import { LANG_TAGS } from './i18n/messages';
  import { formatUntil, type LastItem, type LastKind } from './lastMeeting';
  import { deleteNotes, fetchNotes, notesErrorText, saveFile } from './notes';
  import { deleteRecording, recordingErrorText, recordingLink, startDownload } from './recording';

  // onchange: the room's last meeting changed or turned out gone, so the room list is stale.
  let { onchange }: { onchange: () => void } = $props();

  let dialog: HTMLDialogElement;
  let form: HTMLFormElement;
  // Focus targets: the confirmation replaces the rows, so focus moves explicitly.
  let pinInput: HTMLInputElement;
  let deleteButton = $state<HTMLButtonElement>();
  let cancelButton = $state<HTMLButtonElement>();

  let slug = $state('');
  let items = $state<LastItem[]>([]);
  let pin = $state('');
  let showPin = $state(false);
  let errors = $state<string[]>([]);
  let confirming = $state(false);
  let choice = $state<LastKind | 'all'>('all');
  // One guard per action, against double clicks.
  let busy = $state({ video: false, notes: false, remove: false });

  export function open(roomSlug: string, kept: LastItem[], knownPin: string) {
    slug = roomSlug;
    items = kept;
    pin = knownPin;
    showPin = false;
    errors = [];
    confirming = false;
    dialog.showModal();
  }

  const has = (kind: LastKind) => items.some((i) => i.kind === kind);
  const until = (d: Date) => formatUntil(d, LANG_TAGS[i18n.lang]);

  function setErrors(list: string[]) {
    errors = [...new Set(list)]; // one wrong PIN reads once
  }
  // drop removes an item that is gone (deleted here or elsewhere); with nothing left the dialog closes.
  function drop(kinds: LastKind[]) {
    items = items.filter((i) => !kinds.includes(i.kind));
    if (items.length === 0 && errors.length === 0) dialog.close();
  }

  async function downloadVideo() {
    if (busy.video || !form.reportValidity()) return;
    busy.video = true;
    errors = [];
    try {
      const r = await recordingLink(fetch, slug, pin);
      if (r.ok) {
        startDownload(r.url);
        dialog.close();
        return;
      }
      setErrors([recordingErrorText(r, t)]);
      if (r.error === 'gone') {
        onchange();
        drop(['video']);
      }
      // A wrong PIN is fixed in its field; a gone item takes its button away, so focus stays near.
      if (r.error === 'bad_pin' || r.error === 'gone') pinInput.focus();
    } finally {
      busy.video = false;
    }
  }
  async function downloadNotes() {
    if (busy.notes || !form.reportValidity()) return;
    busy.notes = true;
    errors = [];
    try {
      const r = await fetchNotes(fetch, slug, pin, i18n.lang);
      if (r.ok) {
        saveFile(r.blob, r.filename);
        dialog.close();
        return;
      }
      setErrors([notesErrorText(r, t)]);
      if (r.error === 'gone') {
        onchange();
        drop(['notes']);
      }
      // A wrong PIN is fixed in its field; a gone item takes its button away, so focus stays near.
      if (r.error === 'bad_pin' || r.error === 'gone') pinInput.focus();
    } finally {
      busy.notes = false;
    }
  }
  // Enter in the PIN field downloads when there is one thing to download.
  function submit(ev: Event) {
    ev.preventDefault();
    if (confirming || items.length !== 1) return;
    void (items[0].kind === 'video' ? downloadVideo() : downloadNotes());
  }

  async function askDelete() {
    if (!form.reportValidity()) return;
    errors = [];
    choice = items.length > 1 ? 'all' : items[0].kind;
    confirming = true;
    await tick();
    cancelButton?.focus(); // the safe choice
  }
  async function cancelDelete() {
    confirming = false;
    await tick();
    deleteButton?.focus();
  }
  async function remove() {
    if (busy.remove) return;
    busy.remove = true;
    errors = [];
    const kinds: LastKind[] = choice === 'all' ? items.map((i) => i.kind) : [choice];
    const failed: string[] = [];
    const removed: LastKind[] = [];
    let badPin = false;
    let changed = false;
    try {
      for (const kind of kinds) {
        const r = kind === 'video' ? await deleteRecording(fetch, slug, pin) : await deleteNotes(fetch, slug, pin);
        if (r.ok) {
          removed.push(kind);
          changed = true;
          continue;
        }
        failed.push(kind === 'video' ? recordingErrorText(r, t, 'recording.deleteFailed') : notesErrorText(r, t, 'notes.deleteFailed'));
        if (r.error === 'gone') {
          removed.push(kind);
          changed = true;
        }
        // The same PIN fails the same way for the other item; trying it would only count another attempt.
        if (r.error === 'bad_pin' || r.error === 'too_many') {
          badPin = r.error === 'bad_pin';
          break;
        }
      }
    } finally {
      busy.remove = false;
    }
    if (changed) onchange();
    if (failed.length === 0) {
      dialog.close();
      return;
    }
    confirming = false;
    setErrors(failed);
    drop(removed);
    await tick();
    if (badPin) pinInput.focus();
    else deleteButton?.focus();
  }
</script>

<dialog class="last-dialog" bind:this={dialog} aria-labelledby="last-title" onclose={() => (confirming = false)}>
  <form bind:this={form} onsubmit={submit}>
    <h2 id="last-title">{t('last.title')}</h2>
    <label class="field"><span>{t('last.pin')}</span>
      <span class="pin-box">
        <input bind:this={pinInput} bind:value={pin} type={showPin ? 'text' : 'password'} maxlength="16" autocomplete="off" spellcheck="false" required />
        <button type="button" class="eye" aria-pressed={showPin} aria-label={showPin ? t('enter.hidePin') : t('enter.showPin')}
          title={showPin ? t('enter.hidePin') : t('enter.showPin')} onclick={() => (showPin = !showPin)}>
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M2 12s3.5-6 10-6 10 6 10 6-3.5 6-10 6S2 12 2 12z" /><circle cx="12" cy="12" r="2.5" />{#if showPin}<path d="M3 3l18 18" />{/if}</svg>
        </button>
      </span></label>
    {#if errors.length}<div class="error" role="alert">{#each errors as e (e)}<p>{e}</p>{/each}</div>{/if}
    {#if confirming}
      <p class="question" id="last-question" role="alert">{t('last.deleteWhat')}</p>
      <fieldset class="choice" aria-labelledby="last-question">
        {#if has('video')}<label><input type="radio" name="last-delete" value="video" bind:group={choice} />{t('last.videoName')}</label>{/if}
        {#if has('notes')}<label><input type="radio" name="last-delete" value="notes" bind:group={choice} />{t('last.notesName')}</label>{/if}
        {#if items.length > 1}<label><input type="radio" name="last-delete" value="all" bind:group={choice} />{t('last.all')}</label>{/if}
      </fieldset>
      <div class="actions">
        <button class="btn quiet" type="button" disabled={busy.remove} bind:this={cancelButton} onclick={cancelDelete}>{t('last.cancel')}</button>
        <button class="btn danger solid" type="button" disabled={busy.remove} onclick={remove}>{t('last.deleteYes')}</button>
      </div>
    {:else}
      <ul class="items">
        {#each items as item (item.kind)}
          <li>
            {#if item.kind === 'video'}
              <span class="icon rec" aria-hidden="true"></span>
              <span class="what">{t('last.videoName')}<small>{t('last.videoUntil', { time: until(item.until) })}</small></span>
              <button class="btn" type="button" disabled={busy.video} onclick={downloadVideo}
                aria-label="{t('last.download')}: {t('last.videoName')}">{t('last.download')}</button>
            {:else}
              <svg class="icon sheet" viewBox="0 0 24 24" aria-hidden="true"><path d="M6 3h9l3 3v15H6zM9 9h6M9 13h6M9 17h4" /></svg>
              <span class="what">{t('last.notesName')}<small>{t('last.notesUntil', { time: until(item.until) })}</small></span>
              <button class="btn" type="button" disabled={busy.notes} onclick={downloadNotes}
                aria-label="{t('last.download')}: {t('last.notesName')}">{t('last.download')}</button>
            {/if}
          </li>
        {/each}
      </ul>
      <!-- The irreversible delete keeps to the far edge, away from the downloads. -->
      <div class="actions">
        <button class="btn quiet remove" type="button" disabled={busy.remove || items.length === 0} bind:this={deleteButton} onclick={askDelete}>{t('last.delete')}</button>
        <button class="btn quiet" type="button" onclick={() => dialog.close()}>{t('last.close')}</button>
      </div>
    {/if}
  </form>
</dialog>

<style>
  .last-dialog {
    width: min(26rem, 92vw); padding: 1.25rem 1.5rem; border: 1px solid var(--line); border-radius: var(--radius);
    background: var(--panel-2); color: var(--text);
  }
  .last-dialog::backdrop { background: rgba(0, 0, 0, .55); }
  form { display: grid; gap: 1rem; }
  h2 { color: var(--text); font-size: 1.1rem; font-weight: 500; margin: 0; }
  .error p { margin: 0; }
  .pin-box { position: relative; display: block; }
  .pin-box input { padding-right: 2.6rem; }
  .eye {
    position: absolute; right: .35rem; top: 50%; transform: translateY(-50%); width: 2rem; height: 2rem;
    display: grid; place-items: center; border: 0; border-radius: var(--radius-sm); background: transparent; color: var(--muted); cursor: pointer;
  }
  .eye:hover { color: var(--text); }
  .eye svg { width: 1.15rem; height: 1.15rem; fill: none; stroke: currentColor; stroke-width: 1.8; stroke-linecap: round; stroke-linejoin: round; }

  .items { list-style: none; margin: 0; padding: 0; display: grid; gap: .6rem; }
  .items li { display: grid; grid-template-columns: 1.1rem minmax(0, 1fr) auto; align-items: center; gap: .75rem; }
  .icon { justify-self: center; }
  .rec { width: .6rem; height: .6rem; border-radius: 50%; background: var(--danger); }
  .sheet { width: 1.05rem; height: 1.05rem; fill: none; stroke: var(--muted); stroke-width: 2; stroke-linecap: round; stroke-linejoin: round; }
  .what { display: grid; gap: .1rem; font-weight: 500; }
  .what small { color: var(--muted); font-size: .85rem; font-weight: 400; }

  .choice { border: 0; margin: 0; padding: 0; display: grid; gap: .5rem; }
  .question { margin: 0; color: var(--text); }
  .choice label { display: flex; align-items: center; gap: .5rem; cursor: pointer; }
  .choice input { accent-color: var(--danger); margin: 0; }

  .actions {
    display: flex; flex-wrap: wrap; justify-content: flex-end; align-items: center; gap: .5rem;
    padding-top: 1rem; border-top: 1px solid var(--line);
  }
  /* Delete reads as a quiet red word at the far edge; its confirmation is the filled red button. */
  .remove { margin-right: auto; border-color: transparent; color: #f1b5b3; padding-inline: .5rem; }
  .remove:hover:not(:disabled) { border-color: var(--danger); }
  .danger.solid { background: var(--danger); color: #fff; }
</style>
