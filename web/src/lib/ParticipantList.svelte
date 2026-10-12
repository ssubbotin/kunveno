<script lang="ts">
  import { initials } from './initials';
  import { t } from './i18n/i18n.svelte';
  import Video from './Video.svelte';
  import type { Participant } from './protocol';
  let { participants, selfId, sharer, speaking, videos = {}, compact = false }: {
    participants: Participant[]; selfId: string; sharer: string; speaking: Record<string, boolean>;
    videos?: Record<string, MediaStreamTrack>; compact?: boolean;
  } = $props();


  const bars = { good: 3, fair: 2, poor: 1 };

</script>

<!-- Grid sized to the room: ceil(sqrt(n)) columns, and tiles small enough that every row fits the window. -->
<ul class="people" class:compact aria-label={t('people.label')}
  style:--cols={Math.max(1, Math.ceil(Math.sqrt(participants.length)))}
  style:--rows={Math.max(1, Math.ceil(participants.length / Math.max(1, Math.ceil(Math.sqrt(participants.length)))))}>
  {#each participants as p (p.id)}
    {@const video = !compact && p.camera ? videos[p.id] : undefined}
    <li class="tile" class:speaking={speaking[p.id]} class:bot={p.bot} class:me={p.id === selfId} class:has-video={video}>
      {#if video}
        <div class="video"><Video track={video} mirror={p.id === selfId} /></div>
      {/if}
      <span class="avatar" aria-hidden="true">
        {#if p.bot}
          <svg viewBox="0 0 24 24"><path d="M4 12h2l2-5 3 10 3-8 2 3h4" /></svg>
        {:else}
          {initials(p.name)}
        {/if}
      </span>
      <span class="who">
        <span class="name">{p.bot ? t('people.echoName') : p.name}</span>
        <span class="tags">
          {#if p.id === selfId}<span>{t('people.you')}</span>{/if}
          {#if p.bot}<span>{t('people.echoTag')}</span>{/if}
          {#if p.id === sharer}<span class="share">{t('people.sharing')}</span>{/if}
        </span>
      </span>
      {#if p.quality && !p.bot}
        <span class="signal {p.quality}" title={t(`quality.${p.quality}`)}>
          <svg viewBox="0 0 16 14" aria-hidden="true">
            {#each [1, 2, 3] as i (i)}<rect x={(i - 1) * 5.5} y={14 - i * 4.5} width="4" height={i * 4.5} rx="1" class:on={i <= bars[p.quality]} />{/each}
          </svg>
          <span class="sr">{t(`quality.${p.quality}`)}</span>
        </span>
      {/if}
      {#if p.muted}
        <span class="muted" title={t('mic.off')}>
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M9 5a3 3 0 0 1 6 0v6M5 11a7 7 0 0 0 11.5 5.4M12 18v3M4 4l16 16" /></svg>
          <span class="sr">{t('people.muted')}</span>
        </span>
      {/if}
      {#if speaking[p.id]}<span class="sr">{t('people.speaking')}</span>{/if}
    </li>
  {/each}
</ul>

<style>
  .people {
    list-style: none; margin: 0; padding: 0;
    display: grid; gap: .75rem; align-content: start; margin: 0 auto; width: 100%;
    grid-template-columns: repeat(var(--cols), minmax(0, 1fr));
    /* tiles are 16:10; header, controls and padding take about 11rem of the height */
    max-width: calc((100dvh - 11rem - (var(--rows) - 1) * .75rem) / var(--rows) * 1.6 * var(--cols) + (var(--cols) - 1) * .75rem);
  }
  .tile {
    position: relative; display: grid; justify-items: center; align-content: center; gap: .6rem; padding: 1.25rem .75rem 1rem;
    aspect-ratio: 16 / 10; overflow: hidden;
    background: var(--panel); border: 1px solid var(--line); border-radius: var(--radius); text-align: center;
    transition: border-color .15s, box-shadow .15s;
  }
  /* With a camera the picture fills the tile and the name sits over it at the bottom. */
  .video { position: absolute; inset: 0; }
  .has-video .avatar { display: none; }
  .has-video .who {
    position: absolute; left: .5rem; bottom: .5rem; right: .5rem; justify-items: start; text-align: left;
    padding: .25rem .5rem; border-radius: var(--radius-sm); background: rgba(22, 32, 43, .72); width: fit-content; max-width: calc(100% - 1rem);
  }
  .has-video .tags { justify-content: start; }
  .has-video.speaking { box-shadow: inset 0 0 0 3px var(--live); }
  .has-video .muted, .has-video .signal { z-index: 1; }
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
  .signal { position: absolute; top: .7rem; left: .7rem; display: grid; }
  .signal svg { width: 1rem; height: .9rem; }
  .signal rect { fill: var(--line); }
  .signal.good rect.on { fill: var(--ok); }
  .signal.fair rect.on { fill: var(--caution); }
  .signal.poor rect.on { fill: var(--danger); }
  .compact .signal { top: 50%; left: auto; right: 2.9rem; transform: translateY(-50%); }
  .sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); }

  .compact { grid-template-columns: 1fr; gap: .4rem; max-width: none; }
  @media (max-width: 720px) {
    .people:not(.compact) { grid-template-columns: repeat(auto-fill, minmax(10rem, 1fr)); max-width: none; }
  }
  .compact .tile { grid-template-columns: auto 1fr; justify-items: start; align-items: center; text-align: left; padding: .5rem .75rem; gap: .75rem; aspect-ratio: auto; }
  .compact .avatar { width: 2.5rem; height: 2.5rem; font-size: .95rem; }
  .compact .speaking .avatar { box-shadow: 0 0 0 2px var(--live), 0 0 0 6px var(--live-soft); }
  .compact .tags { justify-content: start; }
  .compact .muted { top: 50%; transform: translateY(-50%); }
</style>
