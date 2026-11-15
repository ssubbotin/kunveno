// The facilitator's state words (bot_state) and the pacing of their spoken announcement.
export const BOT_STATES = ['listening', 'thinking', 'speaking', 'waiting'] as const;
export type BotState = (typeof BOT_STATES)[number];

// The longest help text the server relays, in characters after trimming.
export const MAX_BOT_HELP = 800;

// The least time between two spoken announcements of the facilitator's state.
export const ANNOUNCE_GAP = 3000;

// Throttle passes values on at most once per gap. A value pushed sooner waits for the end of the
// gap; when several wait, only the latest is passed on, so the listener hears where things ended.
export class Throttle<T> {
  private last = -Infinity;
  private held: { value: T } | null = null;
  private timer: ReturnType<typeof setTimeout> | null = null;

  constructor(private gap: number, private emit: (value: T) => void) {}

  push(value: T): void {
    const wait = this.last + this.gap - Date.now();
    if (wait <= 0 && this.timer === null) {
      this.fire(value);
      return;
    }
    this.held = { value };
    this.timer ??= setTimeout(() => {
      this.timer = null;
      const held = this.held;
      this.held = null;
      if (held) this.fire(held.value);
    }, wait);
  }

  // stop drops a waiting value; for a component that goes away.
  stop(): void {
    if (this.timer !== null) clearTimeout(this.timer);
    this.timer = null;
    this.held = null;
  }

  private fire(value: T): void {
    this.last = Date.now();
    this.emit(value);
  }
}

type Focusable = { isConnected: boolean };

// focusAfterHelp picks where focus goes when the help dialog closes: back to the button that opened
// it while that is still in the page, else to the participant list (the button went away with the
// bot); null when neither is in the page.
export function focusAfterHelp<T extends Focusable>(opener: T | null, list: T | null): T | null {
  if (opener?.isConnected) return opener;
  return list?.isConnected ? list : null;
}
