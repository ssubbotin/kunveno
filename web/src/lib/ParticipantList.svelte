<script lang="ts">
  import type { Participant } from './protocol';
  let { participants, selfId, sharer, speaking, compact = false }: {
    participants: Participant[]; selfId: string; sharer: string; speaking: Record<string, boolean>; compact?: boolean;
  } = $props();

  function initials(name: string): string {
    const parts = name.trim().split(/\s+/).filter(Boolean);
    return ((parts[0]?.[0] ?? '') + (parts[1]?.[0] ?? '')).toUpperCase() || '?';
  }
</script>

<ul class="people" class:compact aria-label="Участники">
  {#each participants as p (p.id)}
    <li class="tile" class:speaking={speaking[p.id]} class:bot={p.bot} class:me={p.id === selfId}>
      <span class="avatar" aria-hidden="true">
        {#if p.bot}
          <svg viewBox="0 0 24 24"><path d="M4 12h2l2-5 3 10 3-8 2 3h4" /></svg>
        {:else}
          {initials(p.name)}
        {/if}
      </span>
      <span class="who">
        <span class="name">{p.name}</span>
        <span class="tags">
          {#if p.id === selfId}<span>вы</span>{/if}
          {#if p.bot}<span>повторяет звук комнаты</span>{/if}
          {#if p.id === sharer}<span class="share">показывает экран</span>{/if}
        </span>
      </span>
      {#if p.muted}
        <span class="muted" title="Микрофон выключен">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M9 5a3 3 0 0 1 6 0v6M5 11a7 7 0 0 0 11.5 5.4M12 18v3M4 4l16 16" /></svg>
          <span class="sr">микрофон выключен</span>
        </span>
      {/if}
      {#if speaking[p.id]}<span class="sr">говорит</span>{/if}
    </li>
  {/each}
</ul>

<style>
  .people {
    list-style: none; margin: 0; padding: 0;
    display: grid; gap: .75rem; grid-template-columns: repeat(auto-fill, minmax(11rem, 1fr)); align-content: start;
  }
  .tile {
    position: relative; display: grid; justify-items: center; gap: .6rem; padding: 1.25rem .75rem 1rem;
    background: var(--panel); border: 1px solid var(--line); border-radius: var(--radius); text-align: center;
    transition: border-color .15s, box-shadow .15s;
  }
  .avatar {
    width: 4.5rem; height: 4.5rem; border-radius: 50%; display: grid; place-items: center;
    background: var(--panel-2); font-size: 1.4rem; font-weight: 600; letter-spacing: .02em;
    box-shadow: 0 0 0 3px transparent; transition: box-shadow .12s;
  }
  .avatar svg { width: 2rem; height: 2rem; fill: none; stroke: var(--muted); stroke-width: 1.8; stroke-linecap: round; stroke-linejoin: round; }
  .speaking { border-color: var(--live); }
  .speaking .avatar { box-shadow: 0 0 0 3px var(--live), 0 0 0 9px var(--live-soft); }
  .speaking .avatar svg { stroke: var(--live); }
  .bot { border-style: dashed; }
  .who { display: grid; gap: .15rem; min-width: 0; max-width: 100%; }
  .name { font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .tags { display: flex; flex-wrap: wrap; justify-content: center; gap: .25rem .5rem; color: var(--muted); font-size: .8rem; }
  .tags .share { color: var(--live); }
  .muted {
    position: absolute; top: .6rem; right: .6rem; width: 1.75rem; height: 1.75rem; border-radius: 50%;
    display: grid; place-items: center; background: rgba(227, 161, 59, .15);
  }
  .muted svg { width: 1rem; height: 1rem; fill: none; stroke: var(--caution); stroke-width: 2; stroke-linecap: round; }
  .sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); }

  .compact { grid-template-columns: 1fr; gap: .4rem; }
  .compact .tile { grid-template-columns: auto 1fr; justify-items: start; align-items: center; text-align: left; padding: .5rem .75rem; gap: .75rem; }
  .compact .avatar { width: 2.5rem; height: 2.5rem; font-size: .95rem; }
  .compact .speaking .avatar { box-shadow: 0 0 0 2px var(--live), 0 0 0 6px var(--live-soft); }
  .compact .tags { justify-content: start; }
  .compact .muted { top: 50%; transform: translateY(-50%); }
</style>
