<script lang="ts">
  import ParticipantList from './ParticipantList.svelte';
  import ScreenView from './ScreenView.svelte';
  import type { Session } from './session';
  import { i18n, t } from './i18n/i18n.svelte';
  import { errorText } from './i18n/errors';

  let { session }: { session: Session } = $props();
  // The session object lives for the whole page; Room never receives another one.
  // svelte-ignore state_referenced_locally
  const rs = session.state;
  let screenTrack = $state<MediaStreamTrack | null>(null);
  // svelte-ignore state_referenced_locally
  session.onScreen = (t) => (screenTrack = t);

  const sharerName = $derived(rs.participants.find((p) => p.id === rs.sharer)?.name ?? '');
  const shareBusy = $derived(rs.sharer !== '' && !rs.isSharing);
  const echoPresent = $derived(rs.participants.some((p) => p.botKind === 'echo'));
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

<main class="room" class:sharing={screenTrack !== null}>
  <header class="bar">
    <h1>{rs.roomName || rs.roomSlug}</h1>
    <span class="status {rs.status}" class:backup={rs.transport === 'tcp'}
      title={rs.transport === 'tcp' ? t('status.backupHint') : ''}>
      <span class="dot" aria-hidden="true"></span>{statusText}
    </span>
    <span class="count">{people(rs.peopleCount)}</span>
  </header>

  <div class="stage">
    {#if screenTrack}
      <ScreenView track={screenTrack} label={rs.isSharing ? t('screen.yours') : t('screen.theirs', { name: sharerName })} />
    {/if}
    <ParticipantList participants={rs.participants} selfId={rs.selfId} sharer={rs.sharer} speaking={rs.speaking} videos={rs.videos} compact={screenTrack !== null} />
  </div>

  <footer class="controls">
    <button class="btn mic" class:off={rs.muted} aria-pressed={!rs.muted} onclick={() => session.setMuted(!rs.muted)}>
      {#if rs.muted}
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M9 5a3 3 0 0 1 6 0v6M5 11a7 7 0 0 0 11.5 5.4M12 18v3M4 4l16 16" /></svg>
        {t('control.micOff')}
      {:else}
        <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="9" y="2" width="6" height="12" rx="3" /><path d="M5 11a7 7 0 0 0 14 0M12 18v3" /></svg>
        {t('control.mic')}
        <span class="level" class:on={rs.selfSpeaking} aria-hidden="true"></span>
      {/if}
    </button>

    <button class="btn" class:active={rs.cameraOn} aria-pressed={rs.cameraOn} disabled={rs.status !== 'connected' || !rs.hasCamera}
      title={!rs.hasCamera ? t('camera.notFound') : rs.cameraError ? t('camera.unavailable') : ''}
      onclick={() => void session.setCamera(!rs.cameraOn)}>
      <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="6" width="13" height="12" rx="2" /><path d="m16 10 5-3v10l-5-3" />{#if !rs.cameraOn}<path d="M3 3l18 18" />{/if}</svg>
      {!rs.hasCamera ? t('control.cameraNone') : rs.cameraOn ? t('control.camera') : t('control.cameraOff')}
    </button>

    {#if rs.isSharing}
      <button class="btn active" onclick={() => session.stopShare()}>
        <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="4" width="18" height="12" rx="1.5" /><path d="M8 20h8M9 8l6 4M15 8l-6 4" /></svg>
        {t('control.stopShare')}
      </button>
    {:else}
      <button class="btn" disabled={shareBusy || rs.status !== 'connected'} onclick={() => session.startShare()}
        title={shareBusy ? t('control.shareBusy', { name: sharerName }) : ''}>
        <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="4" width="18" height="12" rx="1.5" /><path d="M8 20h8M12 13V7M9.5 9.5 12 7l2.5 2.5" /></svg>
        {t('control.share')}
      </button>
    {/if}

    <button class="btn" class:active={rs.echo} aria-pressed={rs.echo} disabled={rs.status !== 'connected'}
      onclick={() => session.setEcho(!rs.echo)}
      title={t('control.echoHint')}>
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 12h2l2-5 3 10 3-8 2 3h4" /></svg>
      {rs.echo ? (echoPresent ? t('control.echoStop') : t('control.echoStarting')) : t('control.echo')}
    </button>

    <button class="btn" class:active={!rs.receiveVideo} aria-pressed={!rs.receiveVideo}
      onclick={() => session.setReceiveVideo(!rs.receiveVideo)}
      title={t('control.noVideoHint')}>
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M2 12s3.5-6 10-6 10 6 10 6-3.5 6-10 6S2 12 2 12z" /><circle cx="12" cy="12" r="2.5" />{#if !rs.receiveVideo}<path d="M3 3l18 18" />{/if}</svg>
      {rs.receiveVideo ? t('control.noVideo') : t('control.noVideoActive')}
    </button>

    <button class="btn danger leave" onclick={() => session.leave()}>
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M15 4h4v16h-4M10 8l-4 4 4 4M6 12h10" /></svg>
      {t('control.leave')}
    </button>
  </footer>
</main>

<style>
  .room { height: 100%; display: grid; grid-template-rows: auto 1fr auto; }
  .bar { display: flex; align-items: baseline; gap: 1rem; flex-wrap: wrap; padding: 1rem 1.5rem; border-bottom: 1px solid var(--line); }
  h1 { font-size: 1.25rem; font-weight: 600; margin: 0; }
  .status { display: inline-flex; align-items: center; gap: .4rem; color: var(--muted); font-size: .9rem; }
  .status .dot { width: .5rem; height: .5rem; border-radius: 50%; background: var(--muted); }
  .status.connected .dot { background: var(--ok); }
  .status.connected.backup .dot { background: var(--caution); }
  .status.reconnecting .dot, .status.connecting .dot { background: var(--caution); }
  .status.error { color: #f1b5b3; }
  .status.error .dot { background: var(--danger); }
  .count { margin-left: auto; color: var(--muted); font-size: .9rem; font-variant-numeric: tabular-nums; }

  .stage { min-height: 0; overflow: auto; padding: 1.5rem; }
  .room:not(.sharing) .stage { display: grid; align-content: center; }
  .sharing .stage { display: grid; grid-template-columns: 1fr 17rem; gap: 1.25rem; overflow: hidden; }
  .sharing .stage :global(.people) { overflow: auto; }

  .controls {
    display: flex; flex-wrap: wrap; gap: .6rem; justify-content: center; align-items: center;
    padding: .9rem 1.5rem; border-top: 1px solid var(--line); background: var(--panel);
  }
  .controls .btn { padding: .7rem 1.1rem; }
  .controls svg { fill: none; stroke: currentColor; stroke-width: 1.8; stroke-linecap: round; stroke-linejoin: round; }
  .btn.active { border-color: var(--live); background: var(--live-soft); }
  .mic.off { border-color: var(--caution); background: rgba(227, 161, 59, .15); color: #f3cf92; }
  .level { width: .55rem; height: .55rem; border-radius: 50%; background: var(--line); transition: background .1s, box-shadow .1s; }
  .level.on { background: var(--live); box-shadow: 0 0 0 4px var(--live-soft); }
  .leave { margin-left: auto; }

  @media (max-width: 720px) {
    .sharing .stage { grid-template-columns: 1fr; grid-template-rows: minmax(0, 1fr) auto; }
    .leave { margin-left: 0; }
  }
</style>
