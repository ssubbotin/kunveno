// Choosing the media transport for participants whose UDP path to the server is unreliable.

type Store = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;

const KEY = 'kunveno.tcpUntil';
const REMEMBER_MS = 7 * 24 * 3600_000;

// prefersTcp reports whether this browser saw its UDP media path fail within the last day.
export function prefersTcp(store: Store, now = Date.now()): boolean {
  const until = Number(store.getItem(KEY));
  return Number.isFinite(until) && until > now;
}

// rememberTcp makes the next joins from this browser go over TCP for a week; after that UDP is
// tried again, in case the person moved to a network where it works. A day was too short: the
// first join of every morning lost ~40 s on the broken UDP path.
export function rememberTcp(store: Store, now = Date.now()): void {
  store.setItem(KEY, String(now + REMEMBER_MS));
}

// forgetTcp drops the remembered failure, so the next join tries UDP again.
export function forgetTcp(store: Store): void {
  store.removeItem(KEY);
}

// watchConnection turns peer connection states into a single failure report: at once on
// "failed", after "disconnected" lasts graceMs, or when the connection does not come up within
// connectMs of starting (a UDP path that never opens leaves it "connecting" until the server
// gives up, ~40 s). Browsers take ~30 s to reach "failed" on their own, a long silence for a call.
export function watchConnection(onFailed: () => void, graceMs = 5000, connectMs = 8000): (state: RTCPeerConnectionState) => void {
  let timer: ReturnType<typeof setTimeout> | null = null;
  let connectTimer: ReturnType<typeof setTimeout> | null = null;
  let everConnected = false;
  let reported = false;
  const stop = () => {
    if (timer) clearTimeout(timer);
    if (connectTimer) clearTimeout(connectTimer);
    timer = connectTimer = null;
  };
  const report = () => {
    stop();
    if (!reported) {
      reported = true;
      onFailed();
    }
  };
  return (state) => {
    if (state === 'failed') {
      report();
    } else if (state === 'connected') {
      everConnected = true;
      stop();
    } else if (state === 'disconnected') {
      timer ??= setTimeout(report, graceMs);
    } else if ((state === 'new' || state === 'connecting') && !everConnected) {
      connectTimer ??= setTimeout(report, connectMs);
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
