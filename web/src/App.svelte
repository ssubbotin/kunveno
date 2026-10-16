<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import Lobby from './lib/Lobby.svelte';
  import Room from './lib/Room.svelte';
  import SignIn from './lib/SignIn.svelte';
  import GuestJoin from './lib/GuestJoin.svelte';
  import { Session } from './lib/session';
  import { i18n, translate } from './lib/i18n/i18n.svelte';
  import { errorText } from './lib/i18n/errors';
  import { INVITE_KEY, SIGN_IN_NOTICES, fetchMe, guestFrom, restoreHash, takeSignInNotice, type Me } from './lib/auth';
  import { parseJoinLink } from './lib/link';
  import { loadInfo, requireSignIn, site } from './lib/site.svelte';

  // A room link's fragment comes back after the sign-in redirect, before anything reads it.
  restoreHash(location, sessionStorage, (u) => history.replaceState(null, '', u));
  // Why a sign-in just failed (the server's ?signin=), read once and taken off the address.
  const signInNotice = takeSignInNotice(location, (u) => history.replaceState(null, '', u));
  const startLink = parseJoinLink(location.pathname, location.hash);

  const session = new Session();
  const rs = session.state;

  // Sign-in (site.auth.enabled) is known once /api/info answers; with it off the lobby is as before.
  let ready = $state(false);
  let me = $state<Me | null>(null);
  // A guest's visit: the room and the invitation token from the link or this tab.
  let guest = $state<{ room: string; invite: string } | null>(null);
  let guestExpired = $state(false);
  let signInCode = $state(''); // why the sign-in screen came back

  onMount(() => {
    void (async () => {
      await loadInfo();
      if (site.auth.enabled) {
        guest = guestFrom(startLink, sessionStorage);
        if (!guest) me = await fetchMe();
      }
      ready = true;
    })();
  });

  // A 401 auth_required means sign-in is on, even when /api/info failed at start and the defaults
  // said otherwise: the sign-in screen (or the guest page of an invitation link) comes up.
  function authRequired() {
    me = null;
    void requireSignIn();
    if (!guest && rs.status === 'idle') guest = guestFrom(startLink, sessionStorage);
  }

  const inRoom = $derived(!(rs.status === 'idle' || (rs.status === 'error' && rs.selfId === '')));
  const joinError = $derived(rs.errorCode ? errorText(i18n.lang, rs.errorCode, { retryAfter: rs.retryAfter, detail: rs.errorMessage }) : '');

  // An invitation that expired or was revoked ends the guest's visit; a join without a session sends
  // the person to the sign-in screen. Neither is retried.
  $effect(() => {
    if (rs.status !== 'error') return;
    const code = rs.errorCode;
    if (code !== 'bad_invite' && code !== 'auth_required') return;
    untrack(() => {
      session.leave();
      if (code === 'bad_invite') {
        guestExpired = true;
        sessionStorage.removeItem(INVITE_KEY);
      } else {
        signInCode = code;
        authRequired();
      }
    });
  });

  async function join(room: string, pin: string, name: string, invite = '') {
    try {
      await session.join(room, pin, name, invite);
    } catch (e) {
      rs.status = 'error';
      rs.errorCode = 'no_mic';
      rs.errorMessage = (e as Error).message;
    }
  }

  function joinGuest(name: string) {
    if (!guest) return;
    history.replaceState(null, '', `/join/${guest.room}`); // the token leaves the address bar; the tab keeps it
    void join(guest.room, '', name, guest.invite);
  }
</script>

{#if !ready}
  <!-- /api/info decides between the lobby, the sign-in screen and the guest page. -->
{:else if inRoom}
  <Room {session} invites={site.auth.enabled && !rs.guest} />
{:else if site.auth.enabled && guest}
  <GuestJoin expired={guestExpired} error={joinError} onJoin={joinGuest} />
{:else if site.auth.enabled && !me}
  <SignIn notice={signInCode ? errorText(i18n.lang, signInCode, {}) : signInNotice ? translate(i18n.lang, SIGN_IN_NOTICES[signInNotice]) : ''} />
{:else}
  <Lobby onJoin={join} error={joinError} {me} onAuthRequired={authRequired} />
{/if}
