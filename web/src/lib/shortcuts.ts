// Keyboard shortcuts of the room. Keys are matched by physical position (event.code), so they work
// with any layout, and need exactly one modifier: Ctrl, or Command on macOS. Single keys are never
// shortcuts: screen readers take them in browse mode, and dictation would trigger them (WCAG 2.1.4).
export type ShortcutAction = 'mic' | 'camera' | 'help';
export type KeyLike = Pick<KeyboardEvent, 'code' | 'ctrlKey' | 'metaKey' | 'altKey' | 'shiftKey' | 'repeat'>;

const KEYS: Record<ShortcutAction, { code: string; label: string }> = {
  mic: { code: 'KeyD', label: 'D' },
  camera: { code: 'KeyE', label: 'E' },
  help: { code: 'Slash', label: '/' },
};

export function matchShortcut(e: KeyLike, mac: boolean): ShortcutAction | null {
  // Holding the keys must not flip the microphone back and forth.
  if (e.repeat || e.altKey || e.shiftKey) return null;
  const mod = mac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey;
  if (!mod) return null;
  for (const [action, k] of Object.entries(KEYS) as [ShortcutAction, { code: string }][]) {
    if (k.code === e.code) return action;
  }
  return null;
}

export function shortcutLabel(action: ShortcutAction, mac: boolean): string {
  return (mac ? '⌘' : 'Ctrl+') + KEYS[action].label;
}

export function ariaShortcut(action: ShortcutAction, mac: boolean): string {
  return (mac ? 'Meta+' : 'Control+') + KEYS[action].label;
}

export function isMac(nav: { platform?: string; userAgentData?: { platform?: string } }): boolean {
  return /mac|iphone|ipad/i.test(nav.userAgentData?.platform ?? nav.platform ?? '');
}
