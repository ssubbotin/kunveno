<script lang="ts">
  import { onMount } from 'svelte';
  import { initials } from './initials';
  import { t } from './i18n/i18n.svelte';
  import { cameraConstraints, loadPrefs, savePrefs, type Prefs } from './media';
  import { MIC_KEY, dbfs, meterPercent, noiseFilterOn, rmsOf } from './micLevel';
  import { openMic, type MicInput } from './micInput';
  import { site } from './site.svelte';
  import Video from './Video.svelte';

  let { name = '' }: { name?: string } = $props();

  type Status = 'idle' | 'asking' | 'live' | 'denied';
  let status = $state<Status>('idle');
  let devices = $state<MediaDeviceInfo[]>([]);
  let selected = $state(localStorage.getItem(MIC_KEY) ?? '');
  let level = $state(0);
  let peak = $state(0);
  let heard = $state(false);
  let silentTooLong = $state(false);

  // Pre-join choices, applied when entering the room.
  let prefs = $state<Prefs>(loadPrefs(localStorage));
  function setPref(patch: Partial<Prefs>) {
    prefs = { ...prefs, ...patch };
    savePrefs(localStorage, prefs);
  }
  // The noise filter: the person's own choice, or the installation's default.
  const filterOn = $derived(noiseFilterOn(prefs.noiseFilter, site.noiseSuppression));
  let filterUnavailable = $state(false);
  // The mark on the microphone toggle: the filter is running on a microphone that will be on.
  const noiseMark = $derived(status === 'live' && prefs.micOn && filterOn && !filterUnavailable);
  function toggleFilter() {
    if (filterUnavailable) return;
    setPref({ noiseFilter: !filterOn });
    void start(); // reopen, so the meter shows the sound as it will be sent
  }

  let cameras = $state<MediaDeviceInfo[]>([]);
  // null until counted; enumerateDevices lists inputs (without names) even before permission.
  let hasCamera = $state<boolean | null>(null);
  let camTrack = $state.raw<MediaStreamTrack | null>(null);
  let camError = $state('');

  async function startCamera() {
    stopCamera();
    camError = '';
    try {
      let s: MediaStream;
      try {
        s = await navigator.mediaDevices.getUserMedia({ video: cameraConstraints(prefs.cameraId) });
      } catch (e) {
        if (!prefs.cameraId || (e as Error).name !== 'OverconstrainedError') throw e;
        s = await navigator.mediaDevices.getUserMedia({ video: cameraConstraints('') });
      }
      camTrack = s.getVideoTracks()[0];
      setPref({ cameraId: camTrack.getSettings().deviceId ?? '' });
      cameras = (await navigator.mediaDevices.enumerateDevices()).filter((d) => d.kind === 'videoinput');
    } catch {
      camError = 'camera.unavailable';
      setPref({ cameraOn: false });
    }
  }

  function stopCamera() {
    camTrack?.stop();
    camTrack = null;
  }

  function toggleCamera() {
    if (hasCamera === false) return;
    setPref({ cameraOn: !prefs.cameraOn });
    if (prefs.cameraOn) void startCamera();
    else stopCamera();
  }

  function chooseCamera(ev: Event) {
    setPref({ cameraId: (ev.currentTarget as HTMLSelectElement).value });
    void startCamera();
  }

  let input: MicInput | null = null;
  let ctx: AudioContext | null = null;
  let frame = 0;
  let silenceTimer: ReturnType<typeof setTimeout> | null = null;

  function stop() {
    cancelAnimationFrame(frame);
    if (silenceTimer) clearTimeout(silenceTimer);
    input?.stop();
    input = null;
    void ctx?.close();
    ctx = null;
    level = 0;
    peak = 0;
  }

  async function start() {
    stop();
    status = 'asking';
    heard = false;
    silentTooLong = false;
    const filter = filterOn;
    try {
      input = await openMic(selected, filter); // falls back to the default device when it is gone
    } catch {
      status = 'denied';
      return;
    }
    filterUnavailable = filter && !input.filtered;
    selected = input.deviceId || selected;
    localStorage.setItem(MIC_KEY, selected);
    devices = (await navigator.mediaDevices.enumerateDevices()).filter((d) => d.kind === 'audioinput');
    ctx = new AudioContext();
    const analyser = ctx.createAnalyser();
    analyser.fftSize = 1024;
    ctx.createMediaStreamSource(new MediaStream([input.track])).connect(analyser);
    const buf = new Float32Array(analyser.fftSize);
    status = 'live';
    silenceTimer = setTimeout(() => (silentTooLong = !heard), 8000);
    const tick = () => {
      analyser.getFloatTimeDomainData(buf);
      const db = dbfs(rmsOf(buf));
      level = meterPercent(db);
      peak = Math.max(level, peak - 1);
      if (db > -45) heard = true;
      frame = requestAnimationFrame(tick);
    };
    tick();
  }

  function choose(ev: Event) {
    selected = (ev.currentTarget as HTMLSelectElement).value;
    localStorage.setItem(MIC_KEY, selected);
    void start();
  }

  onMount(() => {
    // Start right away when the browser already allows the microphone; otherwise wait for a click.
    void navigator.permissions?.query({ name: 'microphone' as PermissionName })
      .then((p) => { if (p.state === 'granted') void start(); })
      .catch(() => {});
    void navigator.mediaDevices.enumerateDevices().then((all) => {
      hasCamera = all.some((d) => d.kind === 'videoinput');
      if (!hasCamera && prefs.cameraOn) setPref({ cameraOn: false }); // nothing to turn on here
      else if (prefs.cameraOn) void startCamera();
    }).catch(() => (hasCamera = false));
    return () => {
      stop();
      stopCamera();
    };
  });
</script>

<div class="check">
  <!-- Preview: the avatar others will see; the camera picture goes here later (object-fit: cover). -->
  <div class="preview">
    {#if camTrack && prefs.cameraOn}
      <div class="cam"><Video track={camTrack} mirror /></div>
    {:else}
      <div class="center">
        <span class="avatar" class:live={status === 'live' && prefs.micOn} style:--ring="{Math.round(level * 0.22)}px" aria-hidden="true">{initials(name)}</span>
        {#if status !== 'live'}
          <button class="btn" type="button" onclick={start} disabled={status === 'asking'}>
            {status === 'asking' ? t('mic.asking') : t('mic.check')}
          </button>
        {/if}
      </div>
    {/if}
    <span id="mic-check-noise" hidden>{t('noise.on')}</span>
    <div class="toggles">
      <button type="button" class="round" class:off={!prefs.micOn} aria-pressed={prefs.micOn} onclick={() => setPref({ micOn: !prefs.micOn })}
        title={(prefs.micOn ? t('mic.joinMuted') : t('mic.joinUnmuted')) + (noiseMark ? ' · ' + t('noise.on') : '')}
        aria-describedby={noiseMark ? 'mic-check-noise' : undefined}>
        <svg viewBox="0 0 24 24" aria-hidden="true">{#if prefs.micOn}<rect x="9" y="2" width="6" height="12" rx="3" /><path d="M5 11a7 7 0 0 0 14 0M12 18v3" />{:else}<path d="M9 5a3 3 0 0 1 6 0v6M5 11a7 7 0 0 0 11.5 5.4M12 18v3M4 4l16 16" />{/if}</svg>
        <span class="sr">{prefs.micOn ? t('mic.on') : t('mic.off')}</span>
        {#if noiseMark}<span class="nf" aria-hidden="true"></span>{/if}
      </button>
      <button type="button" class="round" class:off={!prefs.cameraOn} aria-pressed={prefs.cameraOn} onclick={toggleCamera}
        disabled={hasCamera === false}
        title={hasCamera === false ? t('camera.notFound') : prefs.cameraOn ? t('camera.joinWithout') : t('camera.joinWith')}>
        <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="6" width="13" height="12" rx="2" /><path d="m16 10 5-3v10l-5-3" />{#if !prefs.cameraOn}<path d="M3 3l18 18" />{/if}</svg>
        <span class="sr">{prefs.cameraOn ? t('camera.on') : t('camera.off')}</span>
      </button>
    </div>
  </div>
  {#if camError}<p class="hint warn">{t('camera.unavailable')}</p>{/if}

  <div class="devices">
  {#if prefs.cameraOn && cameras.length > 0}
    <label class="field"><span>{t('camera.label')}</span>
      <select value={prefs.cameraId} onchange={chooseCamera}>
        {#each cameras as d (d.deviceId)}
          <option value={d.deviceId}>{d.label || t('camera.label')}</option>
        {/each}
      </select>
    </label>
  {/if}
  {#if status === 'live'}
    <label class="field"><span>{t('mic.label')}</span>
      <select value={selected} onchange={choose}>
        {#each devices as d (d.deviceId)}
          <option value={d.deviceId}>{d.deviceId === 'default' ? t('mic.default') : d.label || t('mic.label')}</option>
        {/each}
      </select>
    </label>
  {/if}
  </div>
  {#if status === 'live'}
    <button type="button" class="switch-row" role="switch" aria-checked={filterOn && !filterUnavailable}
      aria-label={t('noise.label')} aria-describedby="noise-hint" aria-disabled={filterUnavailable} onclick={toggleFilter}>
      <span class="text">{t('noise.label')}<small id="noise-hint">{filterUnavailable ? t('noise.unavailable') : t('noise.hint')}</small></span>
      <span class="switch" aria-hidden="true"></span>
    </button>
  {/if}
  {#if status === 'live'}
    <div class="level">
      <div class="meter" role="meter" aria-label={t('mic.level')} aria-valuemin="0" aria-valuemax="100" aria-valuenow={level}>
        <span class="fill" class:hot={level >= 75} class:clip={level >= 92} style:width="{level}%"></span>
        <span class="peak" style:left="{peak}%"></span>
      </div>
      <p class="hint" class:ok={heard} class:warn={silentTooLong && !heard}>
        {#if !prefs.micOn}
          {t('mic.willBeMuted')}
        {:else if heard}
          {t('mic.works')}
        {:else if silentTooLong}
          {t('mic.silent')}
        {:else}
          {t('mic.speak')}
        {/if}
      </p>
    </div>
  {:else if status === 'denied'}
    <p class="hint warn">{t('mic.denied')}</p>
  {/if}
</div>

<style>
  .check { display: grid; gap: .9rem; min-width: 0; }
  .preview {
    position: relative; height: clamp(9rem, 26vh, 17rem); display: grid; place-items: center; gap: 1rem;
    background: radial-gradient(circle at 50% 45%, var(--panel-2), var(--ink) 70%);
    border: 1px solid var(--line); border-radius: var(--radius); overflow: hidden;
  }
  .avatar {
    width: 6rem; height: 6rem; border-radius: 50%; display: grid; place-items: center;
    background: var(--panel-2); font-size: 2rem; font-weight: 600;
    box-shadow: 0 0 0 3px var(--line); transition: box-shadow 80ms linear;
  }
  .avatar.live { box-shadow: 0 0 0 3px var(--live), 0 0 0 var(--ring) var(--live-soft); }
  .devices { display: grid; grid-template-columns: repeat(auto-fit, minmax(12rem, 1fr)); gap: .75rem 1rem; }
  .devices:empty { display: none; }
  .center { display: grid; justify-items: center; gap: .9rem; }
  .cam { position: absolute; inset: 0; }
  /* The preview is wider than the camera's 16:9: show the whole picture, as others will see it,
     rather than a crop. */
  .cam :global(video) { object-fit: contain; background: transparent; }
  .toggles { position: absolute; right: .75rem; bottom: .75rem; display: flex; gap: .5rem; }
  .round {
    width: 2.6rem; height: 2.6rem; border-radius: 50%; display: grid; place-items: center; cursor: pointer;
    border: 1px solid var(--line); background: rgba(22, 32, 43, .8); color: var(--text);
  }
  .round:disabled { opacity: .45; cursor: default; }
  .round { position: relative; }
  .nf { position: absolute; right: .1rem; bottom: .1rem; width: .55rem; height: .55rem; border-radius: 50%; background: var(--ok); box-shadow: 0 0 0 2px var(--panel); }
  .switch-row {
    display: grid; grid-template-columns: 1fr auto; align-items: center; gap: .75rem; text-align: left;
    padding: .25rem 0; border: 0; background: transparent; cursor: pointer; color: inherit; font: inherit;
  }
  .switch-row[aria-disabled='true'] { opacity: .55; cursor: default; }
  .text { display: grid; gap: .1rem; }
  .text small { color: var(--muted); font-size: .8rem; line-height: 1.3; }
  .switch { width: 2rem; height: 1.15rem; border-radius: 999px; background: var(--line); position: relative; transition: background .15s; }
  .switch::after { content: ''; position: absolute; top: .15rem; left: .15rem; width: .85rem; height: .85rem; border-radius: 50%; background: var(--text); transition: transform .15s; }
  .switch-row[aria-checked='true'] .switch { background: var(--live); }
  .switch-row[aria-checked='true'] .switch::after { transform: translateX(.85rem); }
  .round.off { border-color: var(--caution); background: rgba(227, 161, 59, .2); color: #f3cf92; }
  .round svg { width: 1.2rem; height: 1.2rem; fill: none; stroke: currentColor; stroke-width: 1.8; stroke-linecap: round; stroke-linejoin: round; }
  .sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); }
  select {
    width: 100%; min-width: 0; padding: .6rem .75rem; border-radius: var(--radius-sm); border: 1px solid var(--line);
    background: var(--ink); color: inherit; font: inherit; text-overflow: ellipsis;
  }
  .level { display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: center; gap: .75rem; }
  .meter { position: relative; height: .5rem; border-radius: 999px; background: var(--ink); border: 1px solid var(--line); overflow: hidden; }
  .fill { position: absolute; inset: 0 auto 0 0; background: var(--ok); transition: width 60ms linear; }
  .fill.hot { background: var(--caution); }
  .fill.clip { background: var(--danger); }
  .peak { position: absolute; top: 0; bottom: 0; width: 2px; background: var(--text); opacity: .7; }
  .hint { margin: 0; font-size: .85rem; color: var(--muted); }
  .hint.ok { color: var(--ok); }
  .hint.warn { color: #f3cf92; }
  .level .hint.warn { grid-column: 1 / -1; }
</style>
