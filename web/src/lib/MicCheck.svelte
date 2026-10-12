<script lang="ts">
  import { onMount } from 'svelte';
  import { MIC_KEY, audioConstraints, dbfs, meterPercent, rmsOf } from './micLevel';

  type Status = 'idle' | 'asking' | 'live' | 'denied';
  let status = $state<Status>('idle');
  let devices = $state<MediaDeviceInfo[]>([]);
  let selected = $state(localStorage.getItem(MIC_KEY) ?? '');
  let level = $state(0);
  let peak = $state(0);
  let heard = $state(false);
  let silentTooLong = $state(false);

  let stream: MediaStream | null = null;
  let ctx: AudioContext | null = null;
  let frame = 0;
  let silenceTimer: ReturnType<typeof setTimeout> | null = null;

  function stop() {
    cancelAnimationFrame(frame);
    if (silenceTimer) clearTimeout(silenceTimer);
    stream?.getTracks().forEach((t) => t.stop());
    stream = null;
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
    try {
      try {
        stream = await navigator.mediaDevices.getUserMedia({ audio: audioConstraints(selected) });
      } catch (e) {
        if (!selected || (e as Error).name !== 'OverconstrainedError') throw e;
        selected = ''; // the remembered device is gone; fall back to the default
        stream = await navigator.mediaDevices.getUserMedia({ audio: audioConstraints('') });
      }
    } catch {
      status = 'denied';
      return;
    }
    const track = stream.getAudioTracks()[0];
    selected = track.getSettings().deviceId ?? selected;
    localStorage.setItem(MIC_KEY, selected);
    devices = (await navigator.mediaDevices.enumerateDevices()).filter((d) => d.kind === 'audioinput');
    ctx = new AudioContext();
    const analyser = ctx.createAnalyser();
    analyser.fftSize = 1024;
    ctx.createMediaStreamSource(stream).connect(analyser);
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
    return stop;
  });
</script>

<div class="mic">
  <span class="label">Микрофон</span>
  {#if status === 'live'}
    <select value={selected} onchange={choose} aria-label="Микрофон">
      {#each devices as d (d.deviceId)}
        <option value={d.deviceId}>{d.deviceId === 'default' ? 'По умолчанию' : d.label || 'Микрофон'}</option>
      {/each}
    </select>
    <div class="meter" role="meter" aria-label="Уровень микрофона" aria-valuemin="0" aria-valuemax="100" aria-valuenow={level}>
      <span class="fill" class:hot={level >= 75} class:clip={level >= 92} style:width="{level}%"></span>
      <span class="peak" style:left="{peak}%"></span>
    </div>
    <p class="hint" class:ok={heard} class:warn={silentTooLong && !heard}>
      {#if heard}
        Микрофон работает.
      {:else if silentTooLong}
        Звука нет. Выберите другой микрофон или проверьте, не выключен ли он в системе.
      {:else}
        Скажите что-нибудь: полоса должна двигаться.
      {/if}
    </p>
  {:else}
    <button class="btn quiet" type="button" onclick={start} disabled={status === 'asking'}>
      {status === 'asking' ? 'Ждём разрешения браузера…' : 'Проверить микрофон'}
    </button>
    {#if status === 'denied'}
      <p class="hint warn">Браузер не дал доступ к микрофону. Разрешите его в настройках сайта (значок слева от адреса).</p>
    {/if}
  {/if}
</div>

<style>
  .mic { display: grid; gap: .4rem; }
  .label { color: var(--muted); font-size: .9rem; }
  select { padding: .6rem .75rem; border-radius: var(--radius-sm); border: 1px solid var(--line); background: var(--ink); color: inherit; font: inherit; }
  .meter { position: relative; height: .6rem; border-radius: 999px; background: var(--ink); border: 1px solid var(--line); overflow: hidden; }
  .fill { position: absolute; inset: 0 auto 0 0; background: var(--ok); transition: width 60ms linear; }
  .fill.hot { background: var(--caution); }
  .fill.clip { background: var(--danger); }
  .peak { position: absolute; top: 0; bottom: 0; width: 2px; background: var(--text); opacity: .7; }
  .hint { margin: 0; font-size: .85rem; color: var(--muted); }
  .hint.ok { color: var(--ok); }
  .hint.warn { color: #f3cf92; }
</style>
