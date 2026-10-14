// The page side of the background effect: starts the worker, feeds it camera frames and hands back
// the processed track. Chrome passes the frame stream to the worker; Firefox, which cannot, sends
// a bitmap of a hidden video element whenever the worker asks.
import type { EffectMode, FromWorker, ToWorker } from './protocol';

export class BackgroundUnavailable extends Error {}

export type Effect = { track: MediaStreamTrack; setMode(mode: EffectMode): void; stop(): void };

// Recognition runs on the CPU (WebAssembly SIMD), as in Meet and Jitsi: the GPU delegate made
// weaker GPUs lose their WebGL context and freeze the page for seconds. The GPU can still be
// chosen for comparison with localStorage kunveno.bgDelegate = 'gpu'.
function delegate(): 'GPU' | 'CPU' {
  try {
    return localStorage.getItem('kunveno.bgDelegate') === 'gpu' ? 'GPU' : 'CPU';
  } catch {
    return 'CPU';
  }
}

type Processor = new (init: { track: MediaStreamTrack }) => { readable: ReadableStream<VideoFrame> };

export async function startEffect(raw: MediaStreamTrack, mode: EffectMode, onStats?: (fps: number) => void): Promise<Effect> {
  const { width = 640, height = 360 } = raw.getSettings();
  const canvas = document.createElement('canvas');
  canvas.width = width;
  canvas.height = height;
  const worker = new Worker(new URL('./worker.ts', import.meta.url), { type: 'module' });
  const send = (m: ToWorker, transfer: Transferable[] = []) => worker.postMessage(m, transfer);
  let video: HTMLVideoElement | null = null;

  // Everything from here to ready can fail (the video refusing to play, the frame stream or the
  // canvas failing to transfer, the worker's own error); the worker must not outlive the attempt.
  // Once the worker is ready, an error is a degraded picture (a frame failed, the stream ended):
  // the effect keeps running, since the background is never turned off by itself.
  let ready = false;
  try {
    const offscreen = canvas.transferControlToOffscreen();
    const MSTP = (globalThis as { MediaStreamTrackProcessor?: Processor }).MediaStreamTrackProcessor;
    if (MSTP) {
      const frames = new MSTP({ track: raw }).readable;
      send({ type: 'init', canvas: offscreen, width, height, delegate: delegate(), frames }, [offscreen, frames]);
    } else {
      video = document.createElement('video');
      video.muted = true;
      video.playsInline = true;
      video.srcObject = new MediaStream([raw]);
      await video.play();
      send({ type: 'init', canvas: offscreen, width, height, delegate: delegate() }, [offscreen]);
    }

    await new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => reject(new BackgroundUnavailable('timeout')), 20000);
      worker.onmessage = async (e: MessageEvent<FromWorker>) => {
        const m = e.data;
        if (m.type === 'ready') {
          clearTimeout(timer);
          ready = true;
          console.info('background effect ready, recognition on', m.delegate);
          resolve();
        } else if (m.type === 'error') {
          if (ready) {
            console.warn('background effect:', m.message);
            return;
          }
          clearTimeout(timer);
          reject(new BackgroundUnavailable(m.message));
        } else if (m.type === 'want' && video && video.readyState >= 2) {
          const frame = await createImageBitmap(video);
          send({ type: 'frame', frame }, [frame]);
        } else if (m.type === 'stats') {
          onStats?.(m.fps);
        }
      };
      worker.onerror = (e) => {
        if (ready) {
          console.warn('background effect:', e.message);
          return;
        }
        clearTimeout(timer);
        reject(new BackgroundUnavailable(e.message));
      };
    });
  } catch (e) {
    worker.terminate();
    if (video) video.srcObject = null;
    throw e instanceof BackgroundUnavailable ? e : new BackgroundUnavailable(String(e));
  }

  const setMode = (m: EffectMode) => send({ type: 'mode', mode: m }, m.kind === 'image' ? [m.image] : []);
  setMode(mode);
  const track = canvas.captureStream(15).getVideoTracks()[0];
  return {
    track,
    setMode,
    stop() {
      send({ type: 'stop' });
      setTimeout(() => worker.terminate(), 1000); // let it release the GPU first
      track.stop();
      if (video) video.srcObject = null;
    },
  };
}
