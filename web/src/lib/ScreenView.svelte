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
  <figure class="screen">
    <video bind:this={video} autoplay playsinline muted></video>
    <figcaption>{label}</figcaption>
  </figure>
{/if}

<style>
  .screen { margin: 0; min-height: 0; display: grid; grid-template-rows: 1fr auto; gap: .5rem; }
  video { width: 100%; height: 100%; min-height: 0; object-fit: contain; background: #0d141c; border-radius: var(--radius); border: 1px solid var(--line); }
  figcaption { color: var(--muted); font-size: .9rem; }
</style>
