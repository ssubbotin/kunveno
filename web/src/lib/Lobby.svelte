<script lang="ts">
  import { onMount } from 'svelte';

  type RoomItem = { slug: string; name: string; participants: number; capacity: number; created: boolean };
  let { onJoin, error = '' }: { onJoin: (room: string, pin: string, name: string) => void; error?: string } = $props();

  let rooms = $state<RoomItem[]>([]);
  let room = $state('');
  let pin = $state(sessionStorage.getItem('kunveno.pin') ?? '');
  let name = $state(localStorage.getItem('kunveno.name') ?? '');
  let loadError = $state('');
  let creating = $state(false);
  let newName = $state('');
  let newPin = $state('');
  let actionError = $state('');

  const selected = $derived(rooms.find((r) => r.slug === room));

  async function loadRooms(prefer?: string) {
    try {
      const res = await fetch('/api/rooms');
      rooms = await res.json();
      const wanted = prefer ?? sessionStorage.getItem('kunveno.room') ?? '';
      room = rooms.some((r) => r.slug === wanted) ? wanted : (rooms[0]?.slug ?? '');
      loadError = '';
    } catch {
      loadError = 'Cannot load the room list. Is the server running?';
    }
  }

  onMount(() => loadRooms());

  async function errorOf(res: Response): Promise<string> {
    try {
      return (await res.json()).error ?? res.statusText;
    } catch {
      return res.statusText;
    }
  }

  async function createRoom(ev: Event) {
    ev.preventDefault();
    actionError = '';
    const res = await fetch('/api/rooms', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: newName.trim(), pin: newPin }),
    });
    if (!res.ok) {
      actionError = await errorOf(res);
      return;
    }
    const made: { slug: string } = await res.json();
    pin = newPin;
    newName = '';
    newPin = '';
    creating = false;
    await loadRooms(made.slug);
  }

  async function deleteRoom() {
    if (!selected) return;
    actionError = '';
    const res = await fetch(`/api/rooms/${encodeURIComponent(selected.slug)}`, {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ pin }),
    });
    if (!res.ok) {
      actionError = res.status === 403 ? 'Enter the room PIN to delete it.' : await errorOf(res);
      return;
    }
    await loadRooms();
  }

  function submit(ev: Event) {
    ev.preventDefault();
    localStorage.setItem('kunveno.name', name.trim());
    sessionStorage.setItem('kunveno.room', room);
    sessionStorage.setItem('kunveno.pin', pin);
    onJoin(room, pin, name.trim());
  }
</script>

<div class="lobby">
<form onsubmit={submit}>
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
  {#if selected?.created}
    <button type="button" class="secondary" disabled={selected.participants > 0} onclick={deleteRoom}
      title={selected.participants > 0 ? 'Only an empty room can be deleted' : 'Uses the PIN above'}>Delete room</button>
  {/if}
</form>

{#if actionError}<p class="error">{actionError}</p>{/if}

{#if creating}
  <form onsubmit={createRoom}>
    <h2>New room</h2>
    <label>Room name <input bind:value={newName} maxlength="48" required /></label>
    <label>PIN for the room <input bind:value={newPin} minlength="4" maxlength="16" required /></label>
    <button type="submit">Create</button>
    <button type="button" class="secondary" onclick={() => (creating = false)}>Cancel</button>
  </form>
{:else}
  <button type="button" class="secondary" onclick={() => (creating = true)}>New room…</button>
{/if}
</div>
