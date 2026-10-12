<script lang="ts">
  import Lobby from './lib/Lobby.svelte';
  import Room from './lib/Room.svelte';
  import { Session } from './lib/session';

  const session = new Session();
  const state = session.state;

  const errorText: Record<string, string> = {
    bad_pin: 'Wrong PIN.',
    bad_room: 'This room does not exist.',
    room_full: 'The room is full.',
    media_failed: 'Media connection failed. Rejoining.',
    bad_message: 'Protocol error. Please reload the page.',
  };

  async function join(room: string, pin: string, name: string) {
    try {
      await session.join(room, pin, name);
    } catch (e) {
      state.status = 'error';
      state.errorCode = 'no_mic';
      state.errorMessage = 'Microphone access is required: ' + (e as Error).message;
    }
  }
</script>

{#if state.status === 'idle' || (state.status === 'error' && state.selfId === '')}
  <Lobby onJoin={join} error={state.errorCode ? (errorText[state.errorCode] ?? state.errorMessage) : ''} />
{:else}
  <Room {session} />
{/if}
