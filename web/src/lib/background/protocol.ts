// Messages between the page and the background worker.
export type EffectMode = { kind: 'blur' } | { kind: 'image'; image: ImageBitmap };

export type ToWorker =
  | { type: 'init'; canvas: OffscreenCanvas; width: number; height: number; frames?: ReadableStream<VideoFrame> }
  | { type: 'frame'; frame: ImageBitmap }
  | { type: 'mode'; mode: EffectMode }
  | { type: 'stop' };

export type FromWorker =
  | { type: 'ready'; delegate: 'GPU' | 'CPU' }
  | { type: 'error'; message: string }
  | { type: 'want' }
  | { type: 'stats'; fps: number; every: number };
