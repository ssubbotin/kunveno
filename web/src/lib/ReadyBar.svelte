<script lang="ts">
  // What this meeting left once it ended: one bar with a download per ready item and one close. A
  // meeting recorded in several parts offers each part.
  import { i18n, t } from './i18n/i18n.svelte';
  import { LANG_TAGS } from './i18n/messages';
  import { formatTime } from './lastMeeting';
  import { fetchMeetings, latestRecording, type MeetingPart } from './meetings';
  import { fetchNotes, notesErrorText, saveFile } from './notes';
  import { recordingErrorText, recordingLink, startDownload, type PartRef } from './recording';

  let { slug, pin, video, notes, ondismiss }: { slug: string; pin: string; video: boolean; notes: boolean; ondismiss: () => void } = $props();

  let videoError = $state('');
  let videoBusy = $state(false);
  let notesError = $state('');
  let notesBusy = $state(false);
  // The parts of the meeting's recording; empty until known (one "Video" button for the latest part).
  let meeting = $state(0);
  let parts = $state<MeetingPart[]>([]);

  $effect(() => {
    if (!video) return;
    void fetchMeetings(fetch, slug).then((rows) => {
      const r = rows ? latestRecording(rows) : null;
      if (r) {
        meeting = r.meeting;
        parts = r.parts;
      }
    });
  });

  async function downloadVideo(at?: PartRef) {
    if (videoBusy) return;
    videoBusy = true;
    videoError = '';
    try {
      const r = await recordingLink(fetch, slug, pin, at);
      if (r.ok) startDownload(r.url);
      else videoError = recordingErrorText(r, t);
    } finally {
      videoBusy = false;
    }
  }
  async function downloadNotes() {
    if (notesBusy) return;
    notesBusy = true;
    notesError = '';
    try {
      const r = await fetchNotes(fetch, slug, pin, i18n.lang);
      if (r.ok) saveFile(r.blob, r.filename);
      else notesError = notesErrorText(r, t);
    } finally {
      notesBusy = false;
    }
  }
</script>

<div class="ready" role="status">
  <span>{t('last.ready')}</span>
  {#if video && parts.length > 1}
    {#each parts as p (p.part)}
      <button type="button" class="btn primary" disabled={videoBusy} onclick={() => downloadVideo({ meeting, part: p.part })}>
        {t('meetings.part', { time: formatTime(p.start, LANG_TAGS[i18n.lang]) })}</button>
    {/each}
  {:else if video}
    <button type="button" class="btn primary" disabled={videoBusy} onclick={() => downloadVideo()}>{t('last.videoName')}</button>
  {/if}
  {#if notes}
    <button type="button" class="btn primary" disabled={notesBusy} onclick={downloadNotes}>{t('last.notesName')}</button>
  {/if}
  <button type="button" class="btn quiet close" aria-label={t('last.dismiss')} title={t('last.dismiss')} onclick={ondismiss}>
    <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18" /></svg>
  </button>
  {#if video && videoError}<span class="error" role="alert">{videoError}</span>{/if}
  {#if notes && notesError}<span class="error" role="alert">{notesError}</span>{/if}
</div>

<style>
  .ready {
    position: fixed; left: 50%; bottom: 5.5rem; transform: translateX(-50%); z-index: 6;
    display: flex; flex-wrap: wrap; align-items: center; gap: .6rem; max-width: 92vw;
    padding: .7rem 1rem; border: 1px solid var(--line); border-radius: var(--radius); background: var(--panel-2);
    box-shadow: 0 6px 20px rgba(0, 0, 0, .35);
  }
  .close { padding: .45rem; }
  .close svg { fill: none; stroke: currentColor; stroke-width: 2; stroke-linecap: round; }
  .error { flex-basis: 100%; color: #f1b5b3; font-size: .9rem; }
</style>
