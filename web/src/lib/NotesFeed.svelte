<script lang="ts">
  // notes.live: the phrases of the meeting as they are recognised, shown in the notes bot's tile in
  // place of a picture, newest at the bottom. It follows new phrases unless the reader scrolled up.
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

<div class="feed">
  {#if phrases.length === 0}<p class="empty">{t('notes.liveEmpty')}</p>{/if}
  <ol bind:this={list} onscroll={onScroll}>
    {#each phrases as p (p.seq)}
      <li><time>{clock(p.start)}</time> <b>{p.name}</b> {p.text}</li>
    {/each}
  </ol>
</div>

<style>
  /* Fills the tile like a camera picture; the bottom is left to the name over it. */
  .feed {
    position: absolute; inset: 0; display: flex; flex-direction: column;
    padding: .75rem .9rem 2.75rem; text-align: left;
  }
  .empty { color: var(--muted); font-size: .85rem; margin: auto 0; text-align: center; }
  ol { list-style: none; margin: 0; padding: 0; overflow: auto; display: grid; gap: .35rem; align-content: start; flex: 1; min-height: 0; }
  li { font-size: .85rem; line-height: 1.35; overflow-wrap: anywhere; }
  time { color: var(--muted); font-variant-numeric: tabular-nums; }
</style>
