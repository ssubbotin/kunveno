// Reconnector schedules rejoin attempts: one pending at a time, backing off 1, 2, 4, 8, 10 s.
// A single failure can be reported more than once (a socket fires error and close); without the
// one-pending rule each report would add a timer, and they would double on every failure.
export class Reconnector {
  private attempt = 0;
  private timer: ReturnType<typeof setTimeout> | null = null;

  constructor(private readonly run: () => void) {}

  // schedule starts a retry unless one is already pending; returns its delay, or null.
  schedule(): number | null {
    if (this.timer) return null;
    const delay = Math.min(10_000, 1000 * 2 ** this.attempt);
    this.attempt += 1;
    this.timer = setTimeout(() => {
      this.timer = null;
      this.run();
    }, delay);
    return delay;
  }

  get pending(): boolean {
    return this.timer !== null;
  }

  // succeeded resets the back-off after a join went through.
  succeeded(): void {
    this.attempt = 0;
  }

  cancel(): void {
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
  }
}

// newClientId names this page for the server, which replaces a page's earlier session when the
// same id joins again. One per page load: a duplicated tab gets its own.
export function newClientId(): string {
  const b = new Uint8Array(12);
  crypto.getRandomValues(b);
  return Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
}
