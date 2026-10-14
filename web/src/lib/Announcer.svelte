<script lang="ts">
  // A polite live region for short spoken notices. Screen readers announce a change of text; the
  // trailing no-break space alternates with seq, so the same notice twice in a row is read again.
  let { message, seq }: { message: string; seq: number } = $props();
</script>

<div class="sr" role="status">{message}{seq % 2 ? ' ' : ''}</div>

<style>
  .sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; }
</style>
