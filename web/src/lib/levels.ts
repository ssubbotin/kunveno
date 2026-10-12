let shared: AudioContext | null = null;

function ctx(): AudioContext {
  if (!shared) shared = new AudioContext();
  if (shared.state === 'suspended') void shared.resume();
  return shared;
}

// Calls cb when the RMS level of the track crosses the threshold. Returns a stop function.
export function watchLevel(track: MediaStreamTrack, cb: (speaking: boolean) => void, threshold = 0.02): () => void {
  const ac = ctx();
  const src = ac.createMediaStreamSource(new MediaStream([track]));
  const analyser = ac.createAnalyser();
  analyser.fftSize = 512;
  src.connect(analyser);
  const buf = new Float32Array(analyser.fftSize);
  let last = false;
  const timer = setInterval(() => {
    analyser.getFloatTimeDomainData(buf);
    let sum = 0;
    for (const v of buf) sum += v * v;
    const speaking = Math.sqrt(sum / buf.length) > threshold;
    if (speaking !== last) {
      last = speaking;
      cb(speaking);
    }
  }, 100);
  return () => {
    clearInterval(timer);
    src.disconnect();
    analyser.disconnect();
    if (last) cb(false);
  };
}
