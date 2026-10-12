<script lang="ts">
  import Lobby from './lib/Lobby.svelte';
  import Room from './lib/Room.svelte';
  import { Session } from './lib/session';

  const session = new Session();
  const state = session.state;

  const errorText: Record<string, string> = {
    bad_pin: 'Неверный PIN комнаты.',
    bad_room: 'Такой комнаты нет. Обновите список.',
    room_full: 'В комнате нет мест.',
    media_failed: 'Связь со звуком прервалась, подключаемся заново.',
    bad_message: 'Ошибка протокола. Обновите страницу.',
  };

  async function join(room: string, pin: string, name: string) {
    try {
      await session.join(room, pin, name);
    } catch (e) {
      state.status = 'error';
      state.errorCode = 'no_mic';
      state.errorMessage = 'Нужен доступ к микрофону: ' + (e as Error).message;
    }
  }
</script>

{#if state.status === 'idle' || (state.status === 'error' && state.selfId === '')}
  <Lobby onJoin={join} error={state.errorCode === 'too_many_attempts'
    ? `Слишком много неверных PIN. Попробуйте через ${state.retryAfter} с.`
    : state.errorCode ? (errorText[state.errorCode] ?? state.errorMessage) : ''} />
{:else}
  <Room {session} />
{/if}
