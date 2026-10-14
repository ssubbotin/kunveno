<script lang="ts">
  // notes.live: the phrases of the meeting as they are recognised, newest at the bottom. It follows
  // new phrases unless the reader scrolled up.
  import { tick } from 'svelte';
  import { i18n, t } from './i18n/i18n.svelte';
  import { LANG_TAGS } from './i18n/messages';
  import type { LivePhrase } from './state.svelte';

  let { phrases }: { phrases: LivePhrase[] } = $props();
  let list = $state<HTMLOListElement | null>(null);
  let follow = true;
  function onScroll() {
    if (list) follow = list.scrollHeight - list.scrollTop - list.clientHeight < 40;
  }
  $effect(() => {
    void phrases.length;
    if (follow) void tick().then(() => list?.scrollTo({ top: list.scrollHeight }));
  });
  function clock(ms: number): string {
    return new Intl.DateTimeFormat(LANG_TAGS[i18n.lang], { hour: '2-digit', minute: '2-digit', second: '2-digit' }).format(ms);
  }
</script>

<aside class="notes-panel" id="notes-live" aria-labelledby="notes-live-title">
  <h2 id="notes-live-title">{t('notes.live')}</h2>
  {#if phrases.length === 0}<p class="empty">{t('notes.liveEmpty')}</p>{/if}
  <ol bind:this={list} onscroll={onScroll}>
    {#each phrases as p (p.seq)}
      <li><time>{clock(p.start)}</time> <b>{p.name}</b> {p.text}</li>
    {/each}
  </ol>
</aside>

<style>
  .notes-panel {
    position: fixed; right: 0; top: 3.75rem; bottom: 4.75rem; z-index: 4; width: min(24rem, 90vw);
    display: grid; grid-template-rows: auto auto 1fr; padding: .75rem 1rem;
    background: var(--panel-2); border-left: 1px solid var(--line);
  }
  h2 { font-size: 1rem; margin: 0 0 .5rem; }
  .empty { color: var(--muted); font-size: .9rem; margin: 0; }
  ol { list-style: none; margin: 0; padding: 0; overflow: auto; display: grid; gap: .45rem; align-content: start; }
  li { font-size: .9rem; line-height: 1.35; }
  time { color: var(--muted); font-variant-numeric: tabular-nums; }
</style>
