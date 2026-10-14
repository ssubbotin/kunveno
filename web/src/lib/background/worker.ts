// The background worker: recognises the person and composites each frame, on its own timer, so a
// hidden tab keeps sending.
import { Compositor } from './compositor';
import { Pace } from './pace';
import { createSegmenter, type Segmenter } from './segmenter';
import type { EffectMode, FromWorker, ToWorker } from './protocol';
import { Timings } from './timings';

const FPS = 15;
// The model works at 256x144; recognising a frame reduced to that size first, as Meet and Jitsi do,
// gives a small mask (MediaPipe returns the mask at the input's size) and spares MediaPipe the
// reduction and the enlargement of the mask on every frame.
const SEG_W = 256;
const SEG_H = 144;
let comp: Compositor | null = null;
let seg: Segmenter | null = null;
let mode: EffectMode = { kind: 'blur' };
const pace = new Pace(FPS);
let busy = false;
let stopped = false;
let recovering = false;
let cpuErrorReported = false;
let drawErrorReported = false;
let ticker: ReturnType<typeof setInterval> | null = null;
let statsTimer: ReturnType<typeof setInterval> | null = null;

const post = (m: FromWorker) => postMessage(m);

// Debug mode (localStorage kunveno.bgDebug = '1' on the page): per-second timings and counters.
let probe: Timings | undefined;
const counts = { received: 0, dropped: 0, segmented: 0, contextLost: 0 };
let lastArrival = 0;
let size = '';

// The GPU delegate can be created and still throw on a frame. The first such error moves
// recognition to the CPU; frames meanwhile are composited with the last mask.
async function recover(old: Segmenter): Promise<void> {
  seg = null; // so that 'stop' during recovery does not close the old one twice
  try {
    old.close();
  } catch {
    // the failed segmenter is being discarded anyway
  }
  try {
    const next = await createSegmenter('CPU');
    if (stopped) next.close();
    else seg = next;
  } catch (err) {
    post({ type: 'error', message: String(err) });
  } finally {
    recovering = false;
  }
}

async function process(frame: VideoFrame | ImageBitmap): Promise<void> {
  const start = performance.now();
  if (probe) {
    counts.received++;
    if (lastArrival) probe.add('interval', start - lastArrival);
    lastArrival = start;
    size = 'displayWidth' in frame ? `${frame.displayWidth}x${frame.displayHeight}` : `${frame.width}x${frame.height}`;
  }
  if (!comp || busy || stopped) {
    counts.dropped++;
    frame.close();
    return;
  }
  busy = true;
  try {
    if (seg && !recovering && pace.shouldSegment()) {
      const t = performance.now();
      counts.segmented++;
      try {
        const small = await createImageBitmap(frame, { resizeWidth: SEG_W, resizeHeight: SEG_H, resizeQuality: 'low' });
        probe?.add('scale', performance.now() - t);
        let mask;
        try {
          mask = seg.segment(small, t);
        } finally {
          small.close();
        }
        pace.record(performance.now() - t);
        const u = performance.now();
        if (mask) comp.setMask(mask.data, mask.width, mask.height);
        probe?.add('upload', performance.now() - u);
      } catch (err) {
        if (seg.delegate === 'GPU') {
          recovering = true;
          void recover(seg);
        } else if (!cpuErrorReported) {
          cpuErrorReported = true;
          post({ type: 'error', message: String(err) });
        }
      }
    }
    const d = performance.now();
    comp.draw(frame, mode.kind);
    probe?.add('draw', performance.now() - d);
    pace.frame(performance.now());
    probe?.add('frame', performance.now() - start);
  } catch (err) {
    // One bad frame must not stop the stream: report the first failure and carry on.
    if (!drawErrorReported) {
      drawErrorReported = true;
      post({ type: 'error', message: String(err) });
    }
  } finally {
    frame.close();
    busy = false;
  }
}

async function pump(frames: ReadableStream<VideoFrame>): Promise<void> {
  const reader = frames.getReader();
  try {
    while (!stopped) {
      const { value, done } = await reader.read();
      if (done) {
        if (!stopped) post({ type: 'error', message: 'camera frames ended' });
        return;
      }
      void process(value); // frames arriving while one is processed are dropped (busy)
    }
    await reader.cancel();
  } catch (err) {
    if (!stopped) post({ type: 'error', message: String(err) });
  }
}

self.onmessage = async (e: MessageEvent<ToWorker>) => {
  const m = e.data;
  switch (m.type) {
    case 'init':
      const t0 = performance.now();
      let t1 = t0;
      try {
        if (m.debug) {
          probe = new Timings();
          m.canvas.addEventListener('webglcontextlost', () => counts.contextLost++);
        }
        comp = new Compositor(m.canvas, m.width, m.height);
        t1 = performance.now();
        seg = await createSegmenter(m.delegate === 'CPU' ? 'CPU' : undefined, probe);
      } catch (err) {
        // The frames handed over would stay queued in the stream, unclosed.
        void m.frames?.cancel().catch(() => {});
        post({ type: 'error', message: String(err) });
        return;
      }
      post({ type: 'ready', delegate: seg.delegate, initMs: { compositor: Math.round(t1 - t0), segmenter: Math.round(performance.now() - t1) } });
      if (m.frames) void pump(m.frames);
      else ticker = setInterval(() => post({ type: 'want' }), 1000 / FPS);
      statsTimer = setInterval(() => {
        post({ type: 'stats', fps: pace.fps(), every: pace.every });
        if (probe && seg) {
          post({ type: 'debug', sample: { delegate: seg.delegate, every: pace.every, fps: pace.fps(), size, ...counts, timings: probe.take() } });
          counts.received = counts.dropped = counts.segmented = 0;
        }
      }, 1000);
      break;
    case 'frame':
      void process(m.frame);
      break;
    case 'mode':
      mode = m.mode;
      comp?.setImage(m.mode.kind === 'image' ? m.mode.image : null);
      if (m.mode.kind === 'image') m.mode.image.close(); // uploaded; the bitmap is no longer needed
      break;
    case 'stop':
      stopped = true;
      if (ticker) clearInterval(ticker);
      if (statsTimer) clearInterval(statsTimer);
      seg?.close();
      comp?.dispose();
      close();
      break;
  }
};
