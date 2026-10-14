// Person segmentation with MediaPipe's selfie model, inside the background worker. Every file it
// needs ships with the client; nothing is fetched from elsewhere.
import { ImageSegmenter } from '@mediapipe/tasks-vision';
import loaderUrl from '@mediapipe/tasks-vision/vision_wasm_module_internal.js?url';
import wasmUrl from '@mediapipe/tasks-vision/vision_wasm_module_internal.wasm?url';
import modelUrl from './model/selfie_segmenter_landscape.tflite?url';

export type Mask = { data: Uint8Array; width: number; height: number };

export type Segmenter = {
  delegate: 'GPU' | 'CPU';
  // segment returns the person mask (0..255, top row first) with its size, or null if none came
  // out. MediaPipe scales the mask to the input frame, so its size is the frame's.
  segment(frame: TexImageSource, ts: number): Mask | null;
  close(): void;
};

// The package declares WasmFileset but does not export it.
type WasmFileset = Parameters<typeof ImageSegmenter.createFromOptions>[0];

const fileset: WasmFileset = { wasmLoaderPath: loaderUrl, wasmBinaryPath: wasmUrl };

async function open(delegate: 'GPU' | 'CPU'): Promise<ImageSegmenter> {
  return ImageSegmenter.createFromOptions(fileset, {
    baseOptions: { modelAssetPath: modelUrl, delegate },
    runningMode: 'VIDEO',
    outputConfidenceMasks: true,
    outputCategoryMask: false,
  });
}

// createSegmenter tries the GPU first and falls back to the CPU; force 'CPU' skips the GPU attempt
// (for when the GPU delegate was created but failed on a frame).
export async function createSegmenter(force?: 'CPU'): Promise<Segmenter> {
  let delegate: 'GPU' | 'CPU' = 'GPU';
  let seg: ImageSegmenter;
  try {
    if (force === 'CPU') throw new Error('cpu forced');
    seg = await open('GPU');
  } catch {
    delegate = 'CPU';
    seg = await open('CPU');
  }
  let out: Uint8Array | null = null;
  return {
    delegate,
    segment(frame, ts) {
      const r = seg.segmentForVideo(frame, ts);
      try {
        const m = r.confidenceMasks?.[0];
        if (!m) return null;
        const f = m.getAsFloat32Array();
        if (!out || out.length !== f.length) out = new Uint8Array(f.length);
        for (let i = 0; i < f.length; i++) out[i] = f[i] * 255;
        return { data: out, width: m.width, height: m.height };
      } finally {
        r.close();
      }
    },
    close() {
      seg.close();
    },
  };
}
