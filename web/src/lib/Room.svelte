<script lang="ts">
  import ParticipantList from './ParticipantList.svelte';
  import ScreenView from './ScreenView.svelte';
  import type { Session } from './session';
  import { i18n, t } from './i18n/i18n.svelte';
  import { errorText } from './i18n/errors';
  import { site } from './site.svelte';
  import CopyLink from './CopyLink.svelte';
  import { roomLink } from './link';
  import { nextIndex } from './roving';
  import { tick, untrack } from 'svelte';
  import { fetchNotes, notesErrorText, saveFile } from './notes';
  import NotesPanel from './NotesPanel.svelte';
  import Announcer from './Announcer.svelte';
  import ShortcutsDialog from './ShortcutsDialog.svelte';
  import { ariaShortcut, isMac, matchShortcut, shortcutLabel, type ShortcutAction } from './shortcuts';
  import BackgroundPicker from './BackgroundPicker.svelte';
  import { NONE, type BackgroundChoice } from './background/choice';
  import { presetLabel } from './background/presets';
  import { loadPrefs } from './media';

  let { session }: { session: Session } = $props();
  // The session object lives for the whole page; Room never receives another one.
  // svelte-ignore state_referenced_locally
  const rs = session.state;
  let screenTrack = $state<MediaStreamTrack | null>(null);
  // svelte-ignore state_referenced_locally
  session.onScreen = (t) => (screenTrack = t);

  const sharerName = $derived(rs.participants.find((p) => p.id === rs.sharer)?.name ?? '');
  const shareBusy = $derived(rs.sharer !== '' && !rs.isSharing);
  let picking = $state(false);
  let more = $state(false);
  const mac = isMac(navigator as Navigator & { userAgentData?: { platform?: string } });
  let shortcuts: ShortcutsDialog;
  let announcement = $state('');
  let announceSeq = $state(0);
  function announce(text: string) {
    announcement = text;
    announceSeq += 1;
  }
  // Why the camera button cannot be used now; empty when it can.
  const cameraReason = $derived(!rs.hasCamera ? t('camera.notFound') : rs.status !== 'connected' ? t('control.cameraWait') : '');
  function withKeys(label: string, action: ShortcutAction): string {
    return t('control.withKeys', { label, keys: shortcutLabel(action, mac) });
  }

  // Shortcut actions are announced; clicks are not, since the focused button's state is read anyway.
  function toggleMic() {
    const muted = !rs.muted;
    session.setMuted(muted);
    announce(muted ? t('control.micOff') : t('control.micOn'));
  }
  // Opening a camera takes a moment; a second switch meanwhile would open another track and leave
  // it running, so it is ignored.
  let cameraBusy = false;
  // The camera counts as on from the click: starting it with a background takes about a second, and
  // a click meanwhile switches it off again (cancels the start).
  const cameraWanted = $derived(rs.cameraOn || rs.cameraStarting);
  async function switchCamera(on: boolean): Promise<boolean> {
    if (cameraBusy && on) return false;
    cameraBusy = true;
    try {
      await session.setCamera(on);
    } finally {
      cameraBusy = false;
    }
    return true;
  }
  async function toggleCamera() {
    if (cameraReason) {
      announce(cameraReason);
      return;
    }
    const on = !cameraWanted;
    if (!(await switchCamera(on))) return;
    // Read the result: opening the camera can fail (another app holds it).
    announce(on && !rs.cameraOn ? t('camera.unavailable') : rs.cameraOn ? t('camera.on') : t('camera.off'));
  }

  async function toggleNoise() {
    await session.setNoiseFilter(rs.noiseFilter !== 'on');
    announce(rs.noiseFilter === 'on' ? t('noise.on') : rs.noiseFilter === 'unavailable' ? t('noise.unavailableSaid') : t('noise.off'));
  }

  function onKey(ev: KeyboardEvent) {
    const action = matchShortcut(ev, mac);
    if (action) {
      ev.preventDefault(); // Ctrl+D would bookmark the page, Ctrl+E open the search
      if (action === 'mic') toggleMic();
      else if (action === 'camera') void toggleCamera();
      else openShortcuts();
      return;
    }
    if (ev.key === 'Escape') {
      const back = picking ? reactBtn : more ? moreBtn : null;
      closePopovers();
      back?.focus();
    }
  }
  // Popovers close on Escape and on a click anywhere outside them.
  function closePopovers() {
    picking = false;
    more = false;
  }
  let reactBtn: HTMLButtonElement;
  let moreBtn: HTMLButtonElement;
  let pickerEl = $state<HTMLElement | null>(null);
  let menuEl = $state<HTMLElement | null>(null);
  function items(el: HTMLElement): HTMLElement[] {
    return [...el.querySelectorAll<HTMLElement>('button:not(:disabled)')];
  }
  // Opening a menu moves focus to its first item, as the disclosure pattern expects.
  async function openPicker() {
    picking = !picking;
    more = false;
    await tick();
    if (picking && pickerEl) items(pickerEl)[0]?.focus();
  }
  async function openMore() {
    more = !more;
    picking = false;
    await tick();
    if (more && menuEl) items(menuEl)[0]?.focus();
  }
  function onMenuKey(ev: KeyboardEvent) {
    const list = items(ev.currentTarget as HTMLElement);
    const n = nextIndex(list.indexOf(document.activeElement as HTMLElement), list.length, ev.key);
    if (n === null) return;
    ev.preventDefault();
    list[n].focus();
  }
  // The dialog returns focus to the element focused before it opened; a menu item disappears with
  // its menu, so focus goes to the menu's button first.
  function openShortcuts() {
    const back = picking ? reactBtn : more ? moreBtn : null;
    closePopovers();
    back?.focus();
    shortcuts.open();
  }

  // The background picker. The stored choice is shown while it matches what runs; after a failed
  // switch the stored choice and the running kind can differ, and the running kind wins.
  let bgDialog: HTMLDialogElement;
  let unavailableDialog: HTMLDialogElement;
  let storedBg = $state<BackgroundChoice>(loadPrefs(localStorage).background ?? NONE);
  const currentChoice = $derived<BackgroundChoice>(
    storedBg.kind === rs.background ? storedBg
      : rs.background === 'blur' ? { kind: 'blur' }
      : rs.background === 'glass' ? { kind: 'glass' }
      : rs.background === 'none' ? NONE
      : storedBg,
  );
  function bgLabel(c: BackgroundChoice): string {
    if (c.kind === 'none') return t('bg.none');
    if (c.kind === 'blur') return t('bg.blur');
    if (c.kind === 'glass') return t('bg.glass');
    return c.source === 'own' ? t('bg.ownLabel') : presetLabel(c.name);
  }
  // The picker is mounted while the dialog is open, so each opening lists the server's current
  // presets; focus starts on the current choice.
  let bgOpen = $state(false);
  async function openBackground() {
    const back = more ? moreBtn : null;
    closePopovers();
    back?.focus();
    storedBg = loadPrefs(localStorage).background ?? NONE;
    bgOpen = true;
    await tick();
    if (!bgDialog.open) bgDialog.showModal();
    // The presets and the own image load after the picker mounts; focus waits for the current
    // choice for up to a second, unless it was moved meanwhile.
    const first = document.activeElement;
    for (let i = 0; i < 20 && bgDialog.open && document.activeElement === first; i++) {
      const cur = bgDialog.querySelector<HTMLElement>('[role=radio][aria-checked=true]');
      if (cur) {
        cur.focus();
        break;
      }
      await new Promise((r) => setTimeout(r, 50));
    }
  }
  // The lost notice stays until another choice; the note about an image that failed to load is
  // for the switch just made.
  function closedBackground() {
    bgOpen = false;
    rs.backgroundImageError = false;
  }
  async function chooseBg(c: BackgroundChoice) {
    try {
      await session.setBackground(c);
    } catch (e) {
      console.warn('background', e); // the running camera stays as it was
    }
    storedBg = loadPrefs(localStorage).background ?? NONE;
    announce(t('bg.set', { name: bgLabel(currentChoice) }));
  }
  $effect(() => {
    if (rs.backgroundError && !unavailableDialog.open) unavailableDialog.showModal();
  });
  function withoutBackground() {
    unavailableDialog.close();
    void session.cameraWithoutBackground().finally(() => (storedBg = loadPrefs(localStorage).background ?? NONE));
  }
  function cancelBackground() {
    rs.backgroundError = false;
    if (unavailableDialog.open) unavailableDialog.close();
  }

  function onPointer(ev: PointerEvent) {
    if ((ev.target as Element).closest('.popover-anchor')) return;
    closePopovers();
  }
  const shareReason = $derived(rs.isSharing ? '' : shareBusy ? t('control.shareBusy', { name: sharerName }) : rs.status !== 'connected' ? t('control.shareWait') : '');
  const echoPresent = $derived(rs.participants.some((p) => p.botKind === 'echo'));
  const notesPresent = $derived(rs.participants.some((p) => p.botKind === 'notes'));
  // The notes state is announced on joining and on every change.
  let notesSeen = false;
  $effect(() => {
    const on = rs.notes;
    if (on !== notesSeen) untrack(() => announce(on ? t('notes.recording') : t('notes.stopped')));
    notesSeen = on;
  });
  let notesError = $state('');
  let notesBusy = $state(false);
  async function downloadNotes() {
    if (notesBusy) return;
    notesBusy = true;
    notesError = '';
    try {
      const r = await fetchNotes(fetch, rs.roomSlug, session.pin, i18n.lang);
      if (r.ok) saveFile(r.blob, r.filename);
      else notesError = notesErrorText(r, t);
    } finally {
      notesBusy = false;
    }
  }
  function dismissNotes() {
    rs.notesReady = false;
    notesError = '';
  }
  let showLive = $state(true);
  const statusText = $derived(
    rs.status === 'connected' ? (rs.transport === 'tcp' ? t('status.backup') : t('status.connected'))
      : rs.status === 'reconnecting' ? t('status.reconnecting')
      : rs.status === 'connecting' ? t('status.connecting')
      : rs.status === 'error' ? errorText(i18n.lang, rs.errorCode, { retryAfter: rs.retryAfter, detail: rs.errorMessage })
      : '',
  );

  function people(n: number): string {
    return t('room.people', { n });
  }

</script>

<svelte:window onkeydown={onKey} onpointerdown={onPointer} />

<main class="room" class:sharing={screenTrack !== null}>
  <Announcer message={announcement} seq={announceSeq} />
  <ShortcutsDialog bind:this={shortcuts} {mac} />
  <dialog class="bg-dialog" id="bg-dialog" bind:this={bgDialog} aria-labelledby="bg-title" onclose={closedBackground}>
    <h2 id="bg-title">{t('bg.title')}</h2>
    {#if bgOpen}
      <BackgroundPicker current={currentChoice} fps={rs.cameraOn ? rs.backgroundFps : null} lost={rs.backgroundLost} imageError={rs.backgroundImageError} onchoose={chooseBg} />
    {/if}
    <form method="dialog"><button class="btn">{t('bg.close')}</button></form>
  </dialog>
  <!-- The camera stays off: cancel comes first and takes the focus, so Enter never shows the room. -->
  <dialog class="bg-dialog" bind:this={unavailableDialog} aria-labelledby="bg-unavailable" oncancel={cancelBackground}>
    <p id="bg-unavailable">{rs.backgroundLost ? t('bg.lost') : t('bg.unavailable')}</p>
    <div class="actions">
      <button type="button" class="btn" onclick={cancelBackground}>{t('bg.cancel')}</button>
      <button type="button" class="btn quiet" onclick={withoutBackground}>{t('bg.withoutBg')}</button>
    </div>
  </dialog>
  <header class="bar">
    <h1>{rs.roomName || rs.roomSlug}</h1>
    <span class="status {rs.status}" class:backup={rs.transport === 'tcp'}
      title={rs.transport === 'tcp' ? t('status.backupHint') : ''}>
      <span class="dot" aria-hidden="true"></span>{statusText}
    </span>
    {#if site.notesEnabled && rs.notes}
      <span class="notes-mark" role="status"><span class="rec" aria-hidden="true"></span>{t('notes.recording')}</span>
    {/if}
    {#if site.notesLive && (rs.notes || rs.phrases.length > 0)}
      <button type="button" class="btn quiet live-toggle" aria-expanded={showLive} aria-controls="notes-live" onclick={() => (showLive = !showLive)}>
        {showLive ? t('notes.hideLive') : t('notes.showLive')}
      </button>
    {/if}
    <span class="invite"><CopyLink link={roomLink(location.origin, rs.roomSlug, session.pin)} label={t('control.invite')} /></span>
    <span class="count">{people(rs.peopleCount)}</span>
  </header>

  <div class="stage">
    {#if screenTrack}
      <ScreenView track={screenTrack} label={rs.isSharing ? t('screen.yours') : t('screen.theirs', { name: sharerName })} />
    {/if}
    <ParticipantList participants={rs.participants} selfId={rs.selfId} sharer={rs.sharer} speaking={rs.speaking} videos={rs.videos} reactions={rs.reactions} compact={screenTrack !== null} />
  </div>

  {#if site.notesLive && showLive && (rs.notes || rs.phrases.length > 0)}
    <NotesPanel phrases={rs.phrases} />
  {/if}

  {#if site.notesEnabled && rs.notesReady}
    <div class="notes-ready" role="status">
      <span>{t('notes.ready')}</span>
      <button type="button" class="btn primary" disabled={notesBusy} onclick={downloadNotes}>{t('notes.download')}</button>
      <button type="button" class="btn quiet" onclick={dismissNotes}>{t('notes.dismiss')}</button>
      {#if notesError}<span class="notes-error" role="alert">{notesError}</span>{/if}
    </div>
  {/if}

  <!-- Labels stay the same whatever the state, so buttons never change width; the icon, the colour
       and aria-pressed carry the state. Below 1024 px only the icons remain. -->
  <footer class="controls">
    <div class="main">
      <button class="btn mic" class:off={rs.muted} aria-pressed={!rs.muted} aria-keyshortcuts={ariaShortcut('mic', mac)}
        title={rs.noiseFilter === 'on' ? t('noise.withMic', { label: withKeys(t('control.mic'), 'mic') }) : withKeys(t('control.mic'), 'mic')}
        aria-describedby={rs.noiseFilter === 'on' ? 'mic-noise' : undefined} onclick={() => session.setMuted(!rs.muted)}>
        {#if rs.muted}
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M9 5a3 3 0 0 1 6 0v6M5 11a7 7 0 0 0 11.5 5.4M12 18v3M4 4l16 16" /></svg>
        {:else}
          <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="9" y="2" width="6" height="12" rx="3" /><path d="M5 11a7 7 0 0 0 14 0M12 18v3" /></svg>
        {/if}
        <span class="label">{t('control.mic')}</span>
        {#if !rs.muted}<span class="level" class:on={rs.selfSpeaking} aria-hidden="true"></span>{/if}
        {#if rs.noiseFilter === 'on'}<span class="nf" aria-hidden="true"></span>{/if}
      </button>

      <button class="btn" class:off={rs.hasCamera && !cameraWanted} class:starting={rs.cameraStarting} aria-pressed={cameraWanted}
        aria-busy={rs.cameraStarting} aria-disabled={cameraReason !== ''}
        aria-describedby={cameraReason ? 'camera-reason' : undefined}
        title={cameraReason || (rs.cameraStarting ? t('camera.starting') : rs.cameraError ? t('camera.unavailable') : withKeys(t('control.camera'), 'camera'))} aria-keyshortcuts={ariaShortcut('camera', mac)}
        onclick={() => { if (!cameraReason) void switchCamera(!cameraWanted); }}>
        <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="6" width="13" height="12" rx="2" /><path d="m16 10 5-3v10l-5-3" />{#if !cameraWanted}<path d="M3 3l18 18" />{/if}</svg>
        <span class="label">{t('control.camera')}</span>
      </button>

      <button class="btn" class:active={rs.isSharing} aria-pressed={rs.isSharing} aria-disabled={shareReason !== ''}
        aria-describedby={shareReason ? 'share-reason' : undefined}
        title={shareReason || (rs.isSharing ? t('control.stopShare') : t('control.share'))}
        onclick={() => { if (shareReason) return; void (rs.isSharing ? session.stopShare() : session.startShare()); }}>
        <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="4" width="18" height="12" rx="1.5" />{#if rs.isSharing}<path d="M8 20h8M9 8l6 4M15 8l-6 4" />{:else}<path d="M8 20h8M12 13V7M9.5 9.5 12 7l2.5 2.5" />{/if}</svg>
        <span class="label">{t('control.share')}</span>
      </button>

      <div class="popover-anchor">
        <button class="btn" class:active={picking} aria-expanded={picking} aria-controls="react-menu" disabled={rs.status !== 'connected'} title={t('control.react')}
          bind:this={reactBtn} onclick={openPicker}>
          <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="9" /><path d="M8.5 14.5a4.5 4.5 0 0 0 7 0M9 9.5h.01M15 9.5h.01" /></svg>
          <span class="label">{t('control.react')}</span>
        </button>
        {#if picking}
          <!-- The group only moves focus between its buttons with the arrow keys. -->
          <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
          <div class="picker" id="react-menu" role="group" aria-label={t('control.react')} bind:this={pickerEl} onkeydown={onMenuKey}>
            {#each site.reactions as e (e)}
              <button type="button" onclick={() => { session.react(e); picking = false; reactBtn.focus(); }} aria-label={e}>{e}</button>
            {/each}
          </div>
        {/if}
      </div>

      <div class="popover-anchor">
        <button class="btn" class:active={more} class:flag={!more && (rs.echo || !rs.receiveVideo || rs.notes)} aria-expanded={more} aria-controls="more-menu" title={t('control.more')}
          bind:this={moreBtn} onclick={openMore}>
          <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="5" cy="12" r="1.3" /><circle cx="12" cy="12" r="1.3" /><circle cx="19" cy="12" r="1.3" /></svg>
          <span class="label">{t('control.more')}</span>
        </button>
        {#if more}
          <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
          <div class="menu" id="more-menu" role="group" aria-label={t('control.more')} bind:this={menuEl} onkeydown={onMenuKey}>
            <button type="button" class="row" role="switch" aria-checked={rs.echo} aria-label={t('control.echo')} aria-describedby="echo-hint"
              disabled={rs.status !== 'connected'} onclick={() => session.setEcho(!rs.echo)}>
              <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 12h2l2-5 3 10 3-8 2 3h4" /></svg>
              <span class="text">{t('control.echo')}<small id="echo-hint">{rs.echo && !echoPresent ? t('control.echoStarting') : t('control.echoHint')}</small></span>
              <span class="switch" aria-hidden="true"></span>
            </button>
            {#if site.notesEnabled}
              <button type="button" class="row" aria-describedby="notes-hint" disabled={rs.status !== 'connected'}
                onclick={() => session.setNotes(!rs.notes)}>
                <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 3h9l3 3v15H6zM9 9h6M9 13h6M9 17h4" /></svg>
                <span class="text">{rs.notes ? t('control.notesStop') : t('control.notes')}<small id="notes-hint">{rs.notes && !notesPresent ? t('notes.starting') : t('control.notesHint')}</small></span>
              </button>
            {/if}
            <button type="button" class="row" role="switch" aria-checked={!rs.receiveVideo} aria-label={t('control.noVideo')} aria-describedby="novideo-hint"
              onclick={() => session.setReceiveVideo(!rs.receiveVideo)}>
              <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M2 12s3.5-6 10-6 10 6 10 6-3.5 6-10 6S2 12 2 12z" /><circle cx="12" cy="12" r="2.5" /><path d="M3 3l18 18" /></svg>
              <span class="text">{t('control.noVideo')}<small id="novideo-hint">{t('control.noVideoHint')}</small></span>
              <span class="switch" aria-hidden="true"></span>
            </button>
            <button type="button" class="row" role="switch" aria-checked={rs.noiseFilter === 'on'} aria-label={t('noise.label')}
              aria-describedby="noise-room-hint" onclick={toggleNoise}>
              <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 12h2M7 8v8M11 5v14M15 8v8M19 11v2" /></svg>
              <span class="text">{t('noise.label')}<small id="noise-room-hint">{rs.noiseFilter === 'unavailable' ? t('noise.unavailable') : t('noise.roomHint')}</small></span>
              <span class="switch" aria-hidden="true"></span>
            </button>
            {#if rs.hasCamera}
              <button type="button" class="row" aria-haspopup="dialog" aria-label={t('bg.title')} aria-describedby="bg-current" onclick={openBackground}>
                <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="4" width="18" height="16" rx="2" /><circle cx="12" cy="11" r="3" /><path d="M6.5 20a5.5 5.5 0 0 1 11 0" /></svg>
                <span class="text">{t('bg.title')}<small id="bg-current">{bgLabel(currentChoice)}</small></span>
              </button>
            {/if}
            <button type="button" class="row" aria-keyshortcuts={ariaShortcut('help', mac)} onclick={openShortcuts}>
              <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="2" y="6" width="20" height="12" rx="2" /><path d="M6 10h.01M10 10h.01M14 10h.01M18 10h.01M7 14h10" /></svg>
              <span class="text">{t('control.shortcuts')}</span>
              <kbd class="keys" aria-hidden="true">{shortcutLabel('help', mac)}</kbd>
            </button>
          </div>
        {/if}
      </div>
    </div>
    <span id="camera-reason" class="sr">{cameraReason}</span>
    <span id="mic-noise" hidden>{t('noise.on')}</span>
    <span id="share-reason" class="sr">{shareReason}</span>

    <button class="btn danger leave" title={t('control.leave')} onclick={() => session.leave()}>
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M15 4h4v16h-4M10 8l-4 4 4 4M6 12h10" /></svg>
      <span class="label">{t('control.leave')}</span>
    </button>
  </footer>
</main>

<style>
  .room { height: 100%; display: grid; grid-template-rows: auto 1fr auto; }
  .bar { display: flex; align-items: center; gap: 1rem; flex-wrap: wrap; padding: .75rem 1.5rem; min-height: 3.75rem; border-bottom: 1px solid var(--line); }
  h1 { font-size: 1.25rem; font-weight: 600; margin: 0; }
  .status { display: inline-flex; align-items: center; gap: .4rem; color: var(--muted); font-size: .9rem; }
  .status .dot { width: .5rem; height: .5rem; border-radius: 50%; background: var(--muted); }
  .status.connected .dot { background: var(--ok); }
  .status.connected.backup .dot { background: var(--caution); }
  .status.reconnecting .dot, .status.connecting .dot { background: var(--caution); }
  .status.error { color: #f1b5b3; }
  .status.error .dot { background: var(--danger); }
  .invite { margin-left: auto; }
  .invite :global(.btn) { padding: .45rem .9rem; }
  .invite :global(.copy) { display: inline-grid; }
  .count { color: var(--muted); font-size: .9rem; font-variant-numeric: tabular-nums; }

  .stage { min-height: 0; overflow: auto; padding: 1.5rem; }
  .room:not(.sharing) .stage { display: grid; align-content: center; }
  .sharing .stage { display: grid; grid-template-columns: 1fr 17rem; gap: 1.25rem; overflow: hidden; }
  .sharing .stage :global(.people) { overflow: auto; }

  /* Three zones: an empty left column balances Leave on the right, so the main group is centred. */
  .controls {
    display: grid; grid-template-columns: 1fr auto 1fr; align-items: center; gap: .75rem;
    padding: .9rem 1.5rem; border-top: 1px solid var(--line); background: var(--panel);
  }
  .main { grid-column: 2; display: flex; gap: .5rem; }
  .leave { grid-column: 3; justify-self: end; }
  .controls .btn { padding: .7rem 1.1rem; justify-content: center; }
  .controls svg { fill: none; stroke: currentColor; stroke-width: 1.8; stroke-linecap: round; stroke-linejoin: round; }
  .btn.active { border-color: var(--live); background: var(--live-soft); }
  .btn.off { border-color: var(--caution); background: rgba(227, 161, 59, .15); color: #f3cf92; }
  .btn.flag { box-shadow: inset 0 -2px 0 var(--live); }
  .btn.mic { position: relative; }
  .btn.starting svg { animation: starting 1s ease-in-out infinite; }
  @keyframes starting { 50% { opacity: .35; } }
  @media (prefers-reduced-motion: reduce) { .btn.starting svg { animation: none; opacity: .6; } }
  .nf { position: absolute; right: .3rem; bottom: .3rem; width: .5rem; height: .5rem; border-radius: 50%; background: var(--ok); box-shadow: 0 0 0 2px var(--panel); }
  .level { width: .55rem; height: .55rem; border-radius: 50%; background: var(--line); transition: background .1s, box-shadow .1s; }
  .level.on { background: var(--live); box-shadow: 0 0 0 4px var(--live-soft); }
  .popover-anchor { position: relative; }
  .picker, .menu {
    position: absolute; bottom: calc(100% + .5rem); z-index: 5;
    background: var(--panel-2); border: 1px solid var(--line); box-shadow: 0 6px 20px rgba(0, 0, 0, .35);
  }
  .picker {
    left: 50%; transform: translateX(-50%);
    display: flex; flex-wrap: wrap; justify-content: center; gap: .15rem; padding: .35rem; width: max-content; max-width: min(92vw, 30rem);
    border-radius: 1.5rem;
  }
  .picker button { font-size: 1.5rem; line-height: 1; padding: .3rem; border: 0; border-radius: 50%; background: transparent; cursor: pointer; }
  .picker button:hover, .picker button:focus-visible { background: var(--panel); }
  .menu { right: 0; width: min(22rem, 92vw); display: grid; padding: .35rem; border-radius: var(--radius); }
  .row {
    display: grid; grid-template-columns: auto 1fr auto; align-items: center; gap: .75rem; text-align: left;
    padding: .6rem .7rem; border: 0; border-radius: var(--radius-sm); background: transparent; cursor: pointer;
  }
  .row:hover:not(:disabled), .row:focus-visible { background: var(--panel); }
  .row:disabled { opacity: .45; cursor: default; }
  .row svg { width: 1.15rem; height: 1.15rem; }
  .text { display: grid; gap: .1rem; }
  .sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; }
  .bg-dialog {
    width: min(40rem, 92vw); padding: 1.25rem 1.5rem; border: 1px solid var(--line); border-radius: var(--radius);
    background: var(--panel-2); color: var(--text);
  }
  .bg-dialog::backdrop { background: rgba(0, 0, 0, .55); }
  .bg-dialog h2 { font-size: 1.1rem; margin: 0 0 .75rem; }
  .bg-dialog p { margin: 0 0 1rem; }
  .bg-dialog form, .actions { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: .5rem; margin-top: 1rem; }
  .keys { font: inherit; font-size: .8rem; color: var(--muted); }
  .text small { color: var(--muted); font-size: .8rem; line-height: 1.3; }
  .switch { width: 2rem; height: 1.15rem; border-radius: 999px; background: var(--line); position: relative; transition: background .15s; }
  .switch::after { content: ''; position: absolute; top: .15rem; left: .15rem; width: .85rem; height: .85rem; border-radius: 50%; background: var(--text); transition: transform .15s; }
  .row[aria-checked='true'] .switch { background: var(--live); }
  .row[aria-checked='true'] .switch::after { transform: translateX(.85rem); }

  .live-toggle { padding: .35rem .8rem; font-size: .85rem; }
  .notes-mark { display: inline-flex; align-items: center; gap: .4rem; font-size: .9rem; color: #f1b5b3; }
  .notes-mark .rec { width: .55rem; height: .55rem; border-radius: 50%; background: var(--danger); }
  .notes-ready {
    position: fixed; left: 50%; bottom: 5.5rem; transform: translateX(-50%); z-index: 6;
    display: flex; flex-wrap: wrap; align-items: center; gap: .6rem; max-width: 92vw;
    padding: .7rem 1rem; border: 1px solid var(--line); border-radius: var(--radius); background: var(--panel-2);
    box-shadow: 0 6px 20px rgba(0, 0, 0, .35);
  }
  .notes-error { color: #f1b5b3; font-size: .9rem; }
  @media (max-width: 1024px) {
    .controls .label { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); }
    .controls .btn { min-width: 2.75rem; min-height: 2.75rem; padding: .6rem; }
  }
  @media (max-width: 720px) {
    .sharing .stage { grid-template-columns: 1fr; grid-template-rows: minmax(0, 1fr) auto; }
  }
  @media (max-width: 480px) {
    .controls { padding: .6rem .75rem; gap: .5rem; }
    .main { gap: .35rem; }
    .bar { padding: .75rem 1rem; }
  }
  /* At 320 px the empty balancing column no longer fits: Leave takes its own row (WCAG 1.4.10). */
  @media (max-width: 360px) {
    .controls { grid-template-columns: 1fr; }
    .main { grid-column: 1; justify-self: center; }
    .leave { grid-column: 1; justify-self: end; }
  }
</style>
