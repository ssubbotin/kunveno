// Pace keeps the picture moving on slow computers: when recognising the person takes longer than
// a frame, recognition runs on every n-th frame (the last mask is reused), never less than once a
// second. Compositing still happens for every frame.
export class Pace {
  every = 1;
  private recent: number[] = [];
  private count = 0;
  private frames: number[] = [];

  constructor(private readonly targetFps = 15) {}

  shouldSegment(): boolean {
    this.count += 1;
    return this.count % this.every === 0;
  }

  // record takes the time one recognition took. The pace follows the median of the last nine, so
  // one stall (the first recognition in a new worker can take seconds) does not slow recognition
  // down for long, while a computer that is slow all the time is still noticed within nine samples.
  record(segmentMs: number): void {
    this.recent.push(segmentMs);
    if (this.recent.length > 9) this.recent.shift();
    const sorted = [...this.recent].sort((a, b) => a - b);
    const median = sorted[Math.floor(sorted.length / 2)];
    const budget = 1000 / this.targetFps;
    this.every = Math.min(this.targetFps, Math.max(1, Math.ceil(median / budget)));
  }

  // frame notes one composited frame at time now (ms).
  frame(now: number): void {
    this.frames.push(now);
    while (this.frames.length > 0 && this.frames[0] < now - 1000) this.frames.shift();
  }

  // fps is the number of frames composited in the last second.
  fps(): number {
    return Math.max(0, this.frames.length - 1);
  }
}
