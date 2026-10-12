// Choosing the media transport for participants whose UDP path to the server is unreliable.

type Store = Pick<Storage, 'getItem' | 'setItem'>;

const KEY = 'kunveno.tcpUntil';
const REMEMBER_MS = 24 * 3600_000;

// prefersTcp reports whether this browser saw its UDP media path fail within the last day.
export function prefersTcp(store: Store, now = Date.now()): boolean {
  const until = Number(store.getItem(KEY));
  return Number.isFinite(until) && until > now;
}

// rememberTcp makes the next joins from this browser go over TCP for a day; after that UDP is
// tried again, in case the person moved to a network where it works.
export function rememberTcp(store: Store, now = Date.now()): void {
  store.setItem(KEY, String(now + REMEMBER_MS));
}

// watchConnection turns peer connection states into a single failure report: at once on
// "failed", or after "disconnected" lasts the grace period. Browsers take ~30 s to reach
// "failed" on their own, which is a long silence for a call.
export function watchConnection(onFailed: () => void, graceMs = 5000): (state: RTCPeerConnectionState) => void {
  let timer: ReturnType<typeof setTimeout> | null = null;
  let reported = false;
  const report = () => {
    if (timer) clearTimeout(timer);
    timer = null;
    if (!reported) {
      reported = true;
      onFailed();
    }
  };
  return (state) => {
    if (state === 'failed') {
      report();
    } else if (state === 'disconnected') {
      timer ??= setTimeout(report, graceMs);
    } else if (timer) {
      clearTimeout(timer);
      timer = null;
    }
  };
}

type Stat = { id: string; type: string; [k: string]: unknown };

// transportFromStats names the protocol ("udp" or "tcp") of the candidate pair in use, or ""
// before one is chosen. Chrome points at it from the transport stat; Firefox marks the pair.
export function transportFromStats(stats: Iterable<Stat>): 'udp' | 'tcp' | '' {
  const byId = new Map<string, Stat>();
  for (const s of stats) byId.set(s.id, s);
  let pair: Stat | undefined;
  for (const s of byId.values()) {
    if (s.type === 'transport' && typeof s.selectedCandidatePairId === 'string') pair = byId.get(s.selectedCandidatePairId);
  }
  pair ??= [...byId.values()].find((s) => s.type === 'candidate-pair' && (s.selected === true || (s.nominated === true && s.state === 'succeeded')));
  const local = pair && typeof pair.localCandidateId === 'string' ? byId.get(pair.localCandidateId) : undefined;
  return local?.protocol === 'udp' || local?.protocol === 'tcp' ? local.protocol : '';
}
