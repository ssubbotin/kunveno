<script lang="ts">
  let { track, label }: { track: MediaStreamTrack | null; label: string } = $props();
  let video = $state<HTMLVideoElement | null>(null);

  $effect(() => {
    if (!video) return;
    video.srcObject = track ? new MediaStream([track]) : null;
    if (track) void video.play().catch(() => {});
  });
</script>

{#if track}
  <section class="screen">
    <header>{label}</header>
    <video bind:this={video} autoplay playsinline muted></video>
  </section>
{/if}
