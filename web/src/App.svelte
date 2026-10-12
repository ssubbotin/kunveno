<script lang="ts">
  import Lobby from './lib/Lobby.svelte';
  import Room from './lib/Room.svelte';
  import { Session } from './lib/session';
  import { t } from './lib/i18n/i18n.svelte';

  const session = new Session();
  const state = session.state;

  const known = ['bad_pin', 'bad_room', 'room_full', 'media_failed', 'bad_message'] as const;
  function errorText(code: string, message: string): string {
    if (code === 'too_many_attempts') return t('pin.tooMany', { seconds: state.retryAfter });
    if (code === 'no_mic') return t('error.no_mic', { detail: message });
    return (known as readonly string[]).includes(code) ? t(`error.${code}` as `error.${(typeof known)[number]}`) : message;
  }

  async function join(room: string, pin: string, name: string) {
    try {
      await session.join(room, pin, name);
    } catch (e) {
      state.status = 'error';
      state.errorCode = 'no_mic';
      state.errorMessage = (e as Error).message;
    }
  }
</script>

{#if state.status === 'idle' || (state.status === 'error' && state.selfId === '')}
  <Lobby onJoin={join} error={state.errorCode ? errorText(state.errorCode, state.errorMessage) : ''} />
{:else}
  <Room {session} />
{/if}
