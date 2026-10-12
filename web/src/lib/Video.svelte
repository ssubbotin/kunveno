<script lang="ts">
  let { track, mirror = false }: { track: MediaStreamTrack; mirror?: boolean } = $props();
  let el = $state<HTMLVideoElement | null>(null);

  $effect(() => {
    if (!el) return;
    el.srcObject = new MediaStream([track]);
    void el.play().catch(() => {});
  });
</script>

<video bind:this={el} class:mirror autoplay playsinline muted></video>

<style>
  video { width: 100%; height: 100%; object-fit: cover; display: block; background: #0d141c; }
  .mirror { transform: scaleX(-1); }
</style>
