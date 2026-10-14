// Timings collects named durations over a window (about a second) and summarises them, for the
// background effect's debug mode. Pure, so it is shared by the page and the worker and tested alone.
export type Summary = { n: number; p50: number; p95: number; max: number };

export class Timings {
  private samples = new Map<string, number[]>();

  add(name: string, ms: number): void {
    let list = this.samples.get(name);
    if (!list) this.samples.set(name, (list = []));
    list.push(ms);
  }

  // take returns the summary of every name since the last call and starts a new window.
  take(): Record<string, Summary> {
    const out: Record<string, Summary> = {};
    for (const [name, list] of this.samples) {
      if (list.length === 0) continue;
      const s = [...list].sort((a, b) => a - b);
      const at = (q: number) => s[Math.min(s.length - 1, Math.floor(q * s.length))];
      out[name] = { n: s.length, p50: round(at(0.5)), p95: round(at(0.95)), max: round(s[s.length - 1]) };
    }
    this.samples.clear();
    return out;
  }
}

const round = (ms: number) => Math.round(ms * 10) / 10;
