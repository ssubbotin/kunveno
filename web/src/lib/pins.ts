// PINs of rooms this browser has entered, one per room. A PIN is stored only after the server
// accepted it and dropped when the server says it is wrong (it was changed). Kept in plain text
// in localStorage: anyone with access to this browser profile can read them.

type Store = Pick<Storage, 'getItem' | 'setItem'>;

const KEY = 'kunveno.pins';

function load(store: Store): Record<string, string> {
  try {
    const v = JSON.parse(store.getItem(KEY) ?? '{}');
    if (typeof v !== 'object' || v === null || Array.isArray(v)) return {};
    return Object.fromEntries(Object.entries(v).filter((e): e is [string, string] => typeof e[1] === 'string'));
  } catch {
    return {};
  }
}

export function pinFor(store: Store, room: string): string {
  return load(store)[room] ?? '';
}

export function knownRooms(store: Store): string[] {
  return Object.keys(load(store));
}

export function rememberPin(store: Store, room: string, pin: string): void {
  store.setItem(KEY, JSON.stringify({ ...load(store), [room]: pin }));
}

export function forgetPin(store: Store, room: string): void {
  const all = load(store);
  delete all[room];
  store.setItem(KEY, JSON.stringify(all));
}
