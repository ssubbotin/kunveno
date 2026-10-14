// nextIndex moves focus inside a menu with the arrow keys, Home and End, wrapping at the ends.
// Returns null for keys it does not handle, so the caller leaves those to the browser.
export function nextIndex(current: number, count: number, key: string): number | null {
  if (count === 0) return null;
  switch (key) {
    case 'ArrowRight':
    case 'ArrowDown':
      return current < 0 ? 0 : (current + 1) % count;
    case 'ArrowLeft':
    case 'ArrowUp':
      return current < 0 ? count - 1 : (current - 1 + count) % count;
    case 'Home':
      return 0;
    case 'End':
      return count - 1;
    default:
      return null;
  }
}
