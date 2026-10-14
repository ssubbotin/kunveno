// The participant's own background image: kept in this browser's IndexedDB, reduced to at most
// 1920 px on the long side, and never sent anywhere.
const DB = 'kunveno';
const STORE = 'background';
const KEY = 'own';
const MAX_BYTES = 10 << 20;
const MAX_SIDE = 1920;
const TYPES = ['image/jpeg', 'image/png', 'image/webp'];

function db(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const r = indexedDB.open(DB, 1);
    r.onupgradeneeded = () => r.result.createObjectStore(STORE);
    r.onsuccess = () => resolve(r.result);
    r.onerror = () => reject(r.error);
  });
}

async function tx<T>(mode: IDBTransactionMode, op: (s: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  const d = await db();
  return new Promise((resolve, reject) => {
    const r = op(d.transaction(STORE, mode).objectStore(STORE));
    r.onsuccess = () => resolve(r.result);
    r.onerror = () => reject(r.error);
  });
}

export async function saveOwn(file: File): Promise<void> {
  if (!TYPES.includes(file.type)) throw new Error('type');
  if (file.size > MAX_BYTES) throw new Error('size');
  const bmp = await createImageBitmap(file);
  const k = Math.min(1, MAX_SIDE / Math.max(bmp.width, bmp.height));
  const canvas = new OffscreenCanvas(Math.round(bmp.width * k), Math.round(bmp.height * k));
  canvas.getContext('2d')!.drawImage(bmp, 0, 0, canvas.width, canvas.height);
  bmp.close();
  const blob = await canvas.convertToBlob({ type: 'image/jpeg', quality: 0.9 });
  await tx('readwrite', (s) => s.put(blob, KEY));
}

export async function loadOwn(): Promise<Blob | null> {
  try {
    return ((await tx('readonly', (s) => s.get(KEY))) as Blob | undefined) ?? null;
  } catch {
    return null;
  }
}

export async function deleteOwn(): Promise<void> {
  await tx('readwrite', (s) => s.delete(KEY));
}

export async function hasOwn(): Promise<boolean> {
  return (await loadOwn()) !== null;
}
