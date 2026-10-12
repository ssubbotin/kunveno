<script lang="ts">
  import { onMount } from 'svelte';

  type RoomItem = { slug: string; name: string; participants: number; capacity: number };
  let { onJoin, error = '' }: { onJoin: (room: string, pin: string, name: string) => void; error?: string } = $props();

  let rooms = $state<RoomItem[]>([]);
  let room = $state('');
  let pin = $state(sessionStorage.getItem('kunveno.pin') ?? '');
  let name = $state(localStorage.getItem('kunveno.name') ?? '');
  let loadError = $state('');

  onMount(async () => {
    try {
      const res = await fetch('/api/rooms');
      rooms = await res.json();
      room = sessionStorage.getItem('kunveno.room') ?? rooms[0]?.slug ?? '';
    } catch {
      loadError = 'Cannot load the room list. Is the server running?';
    }
  });

  function submit(ev: Event) {
    ev.preventDefault();
    localStorage.setItem('kunveno.name', name.trim());
    sessionStorage.setItem('kunveno.room', room);
    sessionStorage.setItem('kunveno.pin', pin);
    onJoin(room, pin, name.trim());
  }
</script>

<form class="lobby" onsubmit={submit}>
  <h1>kunveno</h1>
  {#if loadError}<p class="error">{loadError}</p>{/if}
  {#if error}<p class="error">{error}</p>{/if}
  <label>Room
    <select bind:value={room} required>
      {#each rooms as r (r.slug)}
        <option value={r.slug}>{r.name} ({r.participants}/{r.capacity})</option>
      {/each}
    </select>
  </label>
  <label>Your name <input bind:value={name} maxlength="32" required /></label>
  <label>Room PIN <input bind:value={pin} type="password" maxlength="16" required /></label>
  <button type="submit" disabled={!room}>Join</button>
</form>
