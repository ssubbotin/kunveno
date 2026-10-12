<script lang="ts">
  import Lobby from './lib/Lobby.svelte';
  import Room from './lib/Room.svelte';
  import { Session } from './lib/session';
  import { i18n } from './lib/i18n/i18n.svelte';
  import { errorText } from './lib/i18n/errors';

  const session = new Session();
  const state = session.state;

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
  <Lobby onJoin={join} error={state.errorCode ? errorText(i18n.lang, state.errorCode, { retryAfter: state.retryAfter, detail: state.errorMessage }) : ''} />
{:else}
  <Room {session} />
{/if}
