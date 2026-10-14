// Messages between the page and the background worker.
export type EffectMode = { kind: 'blur' } | { kind: 'image'; image: ImageBitmap };

export type ToWorker =
  | { type: 'init'; canvas: OffscreenCanvas; width: number; height: number; delegate: 'GPU' | 'CPU'; debug: boolean; frames?: ReadableStream<VideoFrame> }
  | { type: 'frame'; frame: ImageBitmap }
  | { type: 'mode'; mode: EffectMode }
  | { type: 'stop' };

export type FromWorker =
  | { type: 'ready'; delegate: 'GPU' | 'CPU'; initMs: { compositor: number; segmenter: number } }
  | { type: 'error'; message: string }
  | { type: 'want' }
  | { type: 'stats'; fps: number; every: number }
  | { type: 'debug'; sample: WorkerSample };

// WorkerSample is one second of the worker's work in debug mode.
export type WorkerSample = {
  delegate: 'GPU' | 'CPU';
  every: number;
  fps: number;
  size: string; // the camera frame size reaching the worker
  received: number; // frames that arrived
  dropped: number; // frames dropped while one was being processed
  segmented: number; // frames sent to recognition
  contextLost: number; // WebGL context losses the browser reported
  timings: Record<string, { n: number; p50: number; p95: number; max: number }>;
};
