<script lang="ts">
  import ParticipantList from './ParticipantList.svelte';
  import ScreenView from './ScreenView.svelte';
  import type { Session } from './session';

  let { session }: { session: Session } = $props();
  // The session object lives for the whole page; Room never receives another one.
  // svelte-ignore state_referenced_locally
  const rs = session.state;
  let screenTrack = $state<MediaStreamTrack | null>(null);
  // svelte-ignore state_referenced_locally
  session.onScreen = (t) => (screenTrack = t);

  const sharerName = $derived(rs.participants.find((p) => p.id === rs.sharer)?.name ?? '');
  const shareBusy = $derived(rs.sharer !== '' && !rs.isSharing);
  const statusText = $derived(
    rs.status === 'connected' ? 'connected'
      : rs.status === 'reconnecting' ? 'reconnecting…'
      : rs.status === 'error' ? `error: ${rs.errorCode}`
      : rs.status,
  );
</script>

<main class="room">
  <header>
    <h1>{rs.roomName || rs.roomSlug}</h1>
    <span class="status {rs.status}">{statusText}</span>
  </header>

  <ScreenView track={screenTrack} label={rs.isSharing ? 'Your screen' : `${sharerName} is sharing`} />

  <ParticipantList participants={rs.participants} selfId={rs.selfId} sharer={rs.sharer} speaking={rs.speaking} />

  <footer class="controls">
    <button class:active={rs.muted} onclick={() => session.setMuted(!rs.muted)}>
      <span class="mic-dot" class:on={rs.selfSpeaking} aria-hidden="true"></span>
      {rs.muted ? 'Unmute' : 'Mute'}
    </button>
    {#if rs.isSharing}
      <button onclick={() => session.stopShare()}>Stop sharing</button>
    {:else}
      <button disabled={shareBusy || rs.status !== 'connected'} onclick={() => session.startShare()}>
        {shareBusy ? `${sharerName} is sharing` : 'Share screen'}
      </button>
    {/if}
    <button class="leave" onclick={() => session.leave()}>Leave</button>
  </footer>
</main>
