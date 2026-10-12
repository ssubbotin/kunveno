<script lang="ts">
  import type { Participant } from './protocol';
  let { participants, selfId, sharer, speaking }: {
    participants: Participant[]; selfId: string; sharer: string; speaking: Record<string, boolean>;
  } = $props();
</script>

<ul class="participants">
  {#each participants as p (p.id)}
    <li class:speaking={speaking[p.id]} class:me={p.id === selfId}>
      <span class="name">{p.name}{p.id === selfId ? ' (you)' : ''}</span>
      {#if p.muted}<span class="badge" title="muted">muted</span>{/if}
      {#if p.id === sharer}<span class="badge share" title="sharing screen">sharing</span>{/if}
    </li>
  {/each}
</ul>
