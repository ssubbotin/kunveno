// Error codes from the server, in the person's language. The server's own message text is never
// shown: it is English and meant for logs.
import { translate } from './i18n.svelte';
import type { Key, Lang } from './messages';

const CODES = ['bad_message', 'bad_room', 'bad_pin', 'room_full', 'not_joined', 'share_busy', 'media_failed', 'no_mic', 'auth_required', 'bad_invite'];

export function errorText(lang: Lang, code: string, vars: { retryAfter?: number; detail?: string }): string {
  if (code === 'too_many_attempts') return translate(lang, 'pin.tooMany', { seconds: vars.retryAfter ?? 60 });
  if (CODES.includes(code)) return translate(lang, `error.${code}` as Key, { detail: vars.detail ?? '' });
  return translate(lang, 'error.unknown', { code });
}
