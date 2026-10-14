// The noise filter: GTCRN, a small speech-enhancement neural network (2024) in WebAssembly, run in
// an AudioWorklet. This module is loaded only when the filter is turned on, and its files are
// fetched then.
import { GtcrnWorkletNode, loadGtcrn } from '@sapphi-red/web-noise-suppressor';
import workletUrl from '@sapphi-red/web-noise-suppressor/gtcrnWorklet.js?url';
import wasmUrl from '@sapphi-red/web-noise-suppressor/gtcrn.wasm?url';

// Fetched once; each node gets its own copy, since the worklet may take the buffer over.
let binary: Promise<ArrayBuffer> | null = null;

// filterTrack returns a track carrying raw's sound through the filter, at 48 kHz, in one channel.
export async function filterTrack(raw: MediaStreamTrack): Promise<{ track: MediaStreamTrack; stop(): void }> {
  if (typeof AudioWorkletNode === 'undefined') throw new Error('AudioWorklet is not supported');
  // The fetch does not check the status, and a missing file may come back as the page itself;
  // the worklet would then output silence, so anything but WebAssembly fails here.
  binary ??= loadGtcrn({ url: wasmUrl })
    .then((b) => {
      if (!WebAssembly.validate(b)) throw new Error('the noise filter file is not WebAssembly');
      return b;
    })
    .catch((e) => {
      binary = null; // try again next time
      throw e;
    });
  const ctx = new AudioContext({ sampleRate: 48000 });
  try {
    const [wasm] = await Promise.all([binary, ctx.audioWorklet.addModule(workletUrl)]);
    const node = new GtcrnWorkletNode(ctx, { maxChannels: 1, wasmBinary: wasm.slice(0) });
    const source = ctx.createMediaStreamSource(new MediaStream([raw]));
    const dest = ctx.createMediaStreamDestination();
    // The microphone reaches the graph as two channels and the filter cleans only the first; mixed
    // down to one channel before it and kept at one after, the voice is heard on both sides, as
    // without the filter.
    for (const n of [node, dest]) {
      n.channelCount = 1;
      n.channelCountMode = 'explicit';
      n.channelInterpretation = 'speakers';
    }
    source.connect(node);
    node.connect(dest);
    // A context made without a user gesture may start suspended, and browsers suspend it later
    // (a phone locks, a call comes in); a suspended context sends silence, so it is woken up again,
    // at once and on the next click or key press.
    const wake = () => {
      if (ctx.state !== 'running' && ctx.state !== 'closed') void ctx.resume();
    };
    ctx.onstatechange = wake;
    addEventListener('pointerdown', wake, true);
    addEventListener('keydown', wake, true);
    wake();
    const track = dest.stream.getAudioTracks()[0];
    return {
      track,
      stop() {
        ctx.onstatechange = null;
        removeEventListener('pointerdown', wake, true);
        removeEventListener('keydown', wake, true);
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
