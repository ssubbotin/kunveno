// Debug mode of the background effect, switched on with localStorage kunveno.bgDebug = '1'. It
// keeps two minutes of per-second samples from the worker and the page (main-thread and rendering
// pauses, the Firefox frame path) plus effect events, prints one line a second to the console, and
// exposes everything as window.kunvenoBgDebug (dump() returns it as JSON).
import { Timings } from './timings';
import type { WorkerSample } from './protocol';

const KEEP = 120;
type Event = { at: number; what: string; detail?: unknown };
type Sample = { at: number; worker?: WorkerSample; page: ReturnType<Timings['take']>; gaps: { main: number; render: number } };

export function debugOn(): boolean {
  try {
    return localStorage.getItem('kunveno.bgDebug') === '1';
  } catch {
    return false;
  }
}

class Debug {
  readonly page = new Timings();
  readonly samples: Sample[] = [];
  readonly events: Event[] = [];
  private worker?: WorkerSample;
  private main = 0;
  private render = 0;
  private started = false;
  private readonly t0 = performance.now();

  private at(): number {
    return Math.round(performance.now() - this.t0);
  }

  // start begins watching the page; called when an effect starts in debug mode.
  start(): void {
    if (this.started) return;
    this.started = true;
    let lastTick = performance.now();
    setInterval(() => {
      const n = performance.now();
      this.main = Math.max(this.main, n - lastTick - 50);
      lastTick = n;
    }, 50);
    let lastFrame = performance.now();
    const frame = () => {
      const n = performance.now();
      this.render = Math.max(this.render, n - lastFrame);
      lastFrame = n;
      requestAnimationFrame(frame);
    };
    requestAnimationFrame(frame);
    setInterval(() => this.flush(), 1000);
    (globalThis as unknown as { kunvenoBgDebug: unknown }).kunvenoBgDebug = {
      samples: this.samples,
      events: this.events,
      dump: () => JSON.stringify({ ua: navigator.userAgent, samples: this.samples, events: this.events }),
    };
  }

  event(what: string, detail?: unknown): void {
    const e = { at: this.at(), what, detail };
    this.events.push(e);
    if (this.events.length > 200) this.events.shift();
    console.info('[bg]', e.at, what, detail ?? '');
  }

  workerSample(s: WorkerSample): void {
    this.worker = s;
  }

  private flush(): void {
    const s: Sample = { at: this.at(), worker: this.worker, page: this.page.take(), gaps: { main: Math.round(this.main), render: Math.round(this.render) } };
    this.worker = undefined;
    this.main = this.render = 0;
    this.samples.push(s);
    if (this.samples.length > KEEP) this.samples.shift();
    const w = s.worker;
    const t = (name: string) => (w?.timings[name] ? `${w.timings[name].p50}/${w.timings[name].max}` : '-');
    console.info(
      `[bg] ${w ? `${w.delegate} ${w.fps}fps every${w.every} ${w.size} recv${w.received} drop${w.dropped} lost${w.contextLost}` : 'no worker sample'}` +
        ` | infer ${t('infer')} readback ${t('readback')} upload ${t('upload')} draw ${t('draw')} frame ${t('frame')} interval ${t('interval')}` +
        ` | page main ${s.gaps.main} render ${s.gaps.render}` +
        (s.page.bitmap ? ` bitmap ${s.page.bitmap.p50}/${s.page.bitmap.max}` : ''),
    );
  }
}

export const debug = new Debug();
