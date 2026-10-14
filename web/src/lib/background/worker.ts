// The background worker: recognises the person and composites each frame, on its own timer, so a
// hidden tab keeps sending.
import { Compositor } from './compositor';
import { Pace } from './pace';
import { createSegmenter, type Segmenter } from './segmenter';
import type { EffectMode, FromWorker, ToWorker } from './protocol';

const FPS = 15;
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

function process(frame: VideoFrame | ImageBitmap): void {
  if (!comp || busy || stopped) {
    frame.close();
    return;
  }
  busy = true;
  try {
    if (seg && !recovering && pace.shouldSegment()) {
      const t = performance.now();
      try {
        const mask = seg.segment(frame, t);
        pace.record(performance.now() - t);
        if (mask) comp.setMask(mask.data, mask.width, mask.height);
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
    comp.draw(frame, mode.kind);
    pace.frame(performance.now());
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
      process(value); // drops frames that arrive while one is being processed
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
      try {
        comp = new Compositor(m.canvas, m.width, m.height);
        seg = await createSegmenter(m.delegate === 'CPU' ? 'CPU' : undefined);
      } catch (err) {
        // The frames handed over would stay queued in the stream, unclosed.
        void m.frames?.cancel().catch(() => {});
        post({ type: 'error', message: String(err) });
        return;
      }
      post({ type: 'ready', delegate: seg.delegate });
      if (m.frames) void pump(m.frames);
      else ticker = setInterval(() => post({ type: 'want' }), 1000 / FPS);
      statsTimer = setInterval(() => post({ type: 'stats', fps: pace.fps(), every: pace.every }), 1000);
      break;
    case 'frame':
      process(m.frame);
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
