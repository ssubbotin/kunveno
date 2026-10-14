<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from './i18n/i18n.svelte';
  import { type BackgroundChoice } from './background/choice';
  import { fetchPresets, presetLabel, previewUrl, type Preset } from './background/presets';
  import { deleteOwn, loadOwn, saveOwn } from './background/ownImage';
  import { nextIndex } from './roving';

  // The background choices: none, blur, the server's presets and the participant's own image.
  // lost: the stored image is gone; imageError: an image could not replace the running effect.
  let { current, fps, lost, imageError = false, onchoose }: {
    current: BackgroundChoice; fps: number | null; lost: boolean; imageError?: boolean; onchoose: (c: BackgroundChoice) => void;
  } = $props();

  let presets = $state<Preset[]>([]);
  let ownUrl = $state<string | null>(null);
  let error = $state('');
  let file: HTMLInputElement;

  async function refreshOwn() {
    const b = await loadOwn();
    if (ownUrl) URL.revokeObjectURL(ownUrl);
    ownUrl = b ? URL.createObjectURL(b) : null;
  }
  onMount(() => {
    void fetchPresets().then((p) => (presets = p ?? []));
    void refreshOwn();
    return () => { if (ownUrl) URL.revokeObjectURL(ownUrl); };
  });

  type Option = { key: string; label: string; choice: BackgroundChoice; thumb?: string };
  const options = $derived<Option[]>([
    { key: 'none', label: t('bg.none'), choice: { kind: 'none' } },
    { key: 'blur', label: t('bg.blur'), choice: { kind: 'blur' } },
    ...presets.map((p) => ({ key: 'p:' + p.name, label: presetLabel(p.name), choice: { kind: 'image', source: 'server', name: p.name } as BackgroundChoice, thumb: previewUrl(p.name) })),
    ...(ownUrl ? [{ key: 'own', label: t('bg.ownLabel'), choice: { kind: 'image', source: 'own' } as BackgroundChoice, thumb: ownUrl }] : []),
  ]);
  const keyOf = (c: BackgroundChoice) => (c.kind === 'image' ? (c.source === 'own' ? 'own' : 'p:' + c.name) : c.kind);
  const selected = $derived(keyOf(current));
  // The option reached by Tab: the selected one, or the first when the choice is not listed (a
  // preset that is gone, or a list that failed to load).
  const tabKey = $derived(options.some((o) => o.key === selected) ? selected : options[0]?.key);

  function onKey(ev: KeyboardEvent, i: number) {
    const n = nextIndex(i, options.length, ev.key);
    if (n === null) return;
    ev.preventDefault();
    onchoose(options[n].choice);
    (ev.currentTarget as HTMLElement).parentElement?.querySelectorAll<HTMLElement>('[role=radio]')[n]?.focus();
  }

  async function pick(ev: Event) {
    const f = (ev.currentTarget as HTMLInputElement).files?.[0];
    (ev.currentTarget as HTMLInputElement).value = '';
    if (!f) return;
    error = '';
    try {
      await saveOwn(f);
    } catch (e) {
      error = (e as Error).message === 'size' ? t('bg.tooBig') : t('bg.badType');
      return;
    }
    await refreshOwn();
    onchoose({ kind: 'image', source: 'own' });
  }

  async function removeOwn() {
    await deleteOwn();
    await refreshOwn();
    if (selected === 'own') onchoose({ kind: 'none' });
  }
</script>

<div class="picker">
  <div class="grid" role="radiogroup" aria-label={t('bg.title')}>
    {#each options as o, i (o.key)}
      <button type="button" role="radio" class="opt" aria-checked={selected === o.key} tabindex={tabKey === o.key ? 0 : -1}
        onclick={() => onchoose(o.choice)} onkeydown={(e) => onKey(e, i)}>
        {#if o.thumb}<img src={o.thumb} alt="" />{:else}<span class="swatch {o.key}" aria-hidden="true"></span>{/if}
        <span class="label">{o.label}</span>
      </button>
    {/each}
  </div>
  <div class="own">
    <button type="button" class="btn quiet" onclick={() => file.click()}>{ownUrl ? t('bg.replace') : t('bg.own')}</button>
    {#if ownUrl}<button type="button" class="btn quiet" onclick={removeOwn}>{t('bg.delete')}</button>{/if}
    <input bind:this={file} type="file" accept="image/jpeg,image/png,image/webp" hidden onchange={pick} />
    <small>{t('bg.privacy')}</small>
  </div>
  <p class="status" role="status">
    {#if error}{error}{:else if imageError}{t('bg.imageError')}{:else if lost}{t('bg.lost')}{:else if current.kind !== 'none' && fps !== null}{fps < 12 ? t('bg.slow', { fps }) : t('bg.status', { fps })}{/if}
  </p>
</div>

<style>
  .picker { display: grid; gap: .75rem; }
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(7.5rem, 1fr)); gap: .5rem; }
  .opt { display: grid; gap: .3rem; padding: .3rem; border: 2px solid transparent; border-radius: var(--radius-sm); background: var(--panel); color: inherit; font: inherit; cursor: pointer; text-align: left; }
  .opt[aria-checked='true'] { border-color: var(--live); }
  .opt img, .swatch { width: 100%; aspect-ratio: 16 / 9; object-fit: cover; border-radius: calc(var(--radius-sm) - 2px); display: block; }
  .swatch.none { background: repeating-linear-gradient(45deg, var(--panel-2) 0 .5rem, var(--ink) .5rem 1rem); }
  .swatch.blur { background: radial-gradient(circle at 50% 40%, #9aabbd, #283849 70%); filter: blur(2px); }
  .label { font-size: .85rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .own { display: flex; flex-wrap: wrap; align-items: center; gap: .5rem; }
  .own small, .status { color: var(--muted); font-size: .85rem; margin: 0; }
</style>
