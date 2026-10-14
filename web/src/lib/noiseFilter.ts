// The RNNoise filter: a speech-trained neural network in WebAssembly, run in an AudioWorklet. This
// module is loaded only when the filter is turned on, and its files are fetched then.
import { loadRnnoise, RnnoiseWorkletNode } from '@sapphi-red/web-noise-suppressor';
import workletUrl from '@sapphi-red/web-noise-suppressor/rnnoiseWorklet.js?url';
import wasmUrl from '@sapphi-red/web-noise-suppressor/rnnoise.wasm?url';
import simdUrl from '@sapphi-red/web-noise-suppressor/rnnoise_simd.wasm?url';

// Fetched once; each node gets its own copy, since the worklet may take the buffer over.
let binary: Promise<ArrayBuffer> | null = null;

// filterTrack returns a track carrying raw's sound through RNNoise. RNNoise works at 48 kHz.
export async function filterTrack(raw: MediaStreamTrack): Promise<{ track: MediaStreamTrack; stop(): void }> {
  if (typeof AudioWorkletNode === 'undefined') throw new Error('AudioWorklet is not supported');
  binary ??= loadRnnoise({ url: wasmUrl, simdUrl }).catch((e) => {
    binary = null; // try again next time
    throw e;
  });
  const ctx = new AudioContext({ sampleRate: 48000 });
  try {
    const [wasm] = await Promise.all([binary, ctx.audioWorklet.addModule(workletUrl)]);
    const node = new RnnoiseWorkletNode(ctx, { maxChannels: 1, wasmBinary: wasm.slice(0) });
    const source = ctx.createMediaStreamSource(new MediaStream([raw]));
    const dest = ctx.createMediaStreamDestination();
    source.connect(node);
    node.connect(dest);
    void ctx.resume(); // a context made without a user gesture may start suspended
    const track = dest.stream.getAudioTracks()[0];
    return {
      track,
      stop() {
        source.disconnect();
        node.disconnect();
        node.destroy();
        track.stop();
        void ctx.close();
      },
    };
  } catch (e) {
    void ctx.close();
    throw e;
  }
}
