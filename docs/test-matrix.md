# Manual checks before a release

Browser behaviour has no automated tests (the unit tests cover the protocol parser, the state and
the pure modules in `web/src/lib`), so walk through these before tagging a release, in Chrome and
Firefox, on Linux and Windows where possible. Note what was checked, and on what, in the release's
pull request.

1. Join `ops` with a wrong PIN: "Wrong PIN" is shown and the lobby stays.
2. Join with the correct PIN: the name appears for everyone within 1 s.
3. Speak: the speaking highlight appears for others.
4. Mute: the `muted` badge appears for others; unmute clears it.
5. Share the screen (full screen and a single window): others see it within 2 s.
6. While sharing, another participant's Share button is disabled.
7. Stop sharing from the app button and from the browser's own bar: the view clears for others.
8. Close the tab while sharing: others see the sharer gone and the share cleared within 2 s.
9. Disable the network for 5 s: audio freezes and then resumes without a new join (media.reconnect_grace, 15 s by default). Disable it for 30 s: the status shows reconnecting, then connected, and the server log has a `rejoin` line with the reason.
10. The 16th participant gets "The room is full" (use `tools/loadgen -n 15`).
11. 15 participants (14 from loadgen plus one browser) for 10 minutes: no audio dropouts; note the
    server's CPU.
12. "Copy link" in the lobby (room chosen, PIN entered); open the link in another browser: the room
    is chosen, the PIN filled in, the mic check shown; Join enters the room.
13. "Invite" inside a room; open the link in a private window: as in 12; after joining the address
    bar shows `/`.
14. A link to a deleted room opens the lobby with "The room in the link no longer exists" and the
    room list.
15. Keyboard in the room (Linux or Windows: Ctrl; macOS: ⌘): Ctrl+D mutes and unmutes, also with
    focus on the mic button (one toggle per press) and with the Russian layout; Ctrl+E turns the
    camera on and off; Ctrl+Shift+D and Ctrl+Alt+D do nothing; in the lobby Ctrl+D is the browser's
    bookmark again.
16. While sharing the screen, Ctrl+D still mutes; typing text with spaces in another window does not.
17. Ctrl+/ opens the shortcut list, Escape closes it and focus returns where it was; the list also
    opens from More → Keyboard shortcuts, and closing it then puts focus on More.
    Pressing Ctrl+E twice quickly opens the camera once (its light goes off when it is turned off).
18. Keyboard only: Tab reaches every control, including an unavailable camera or share button, whose
    reason is read; Reaction and More open with Enter, focus lands on the first item, arrows move,
    Escape closes and returns to the button.
19. At 320 px width (devtools) the room controls fit without horizontal scrolling.
20. Noise suppression in the microphone check: with the switch on, typing barely moves the meter
    while speech does; off, typing moves it. The choice survives a reload; with
    `ui.noise_suppression: true` and no choice made, it starts on.
21. In a room, More → Noise suppression during a call: no drop for others, the echo test sounds
    cleaner with it on; muted stays muted across the switch; the microphone button shows the mark.
22. Turn the filter off and on twice in a row, and reload with it on: it still works each time.
23. In a browser without AudioWorklet (or with the WebAssembly blocked in devtools), the switch
    reads "Not available in this browser" and the microphone still works.
24. Background blur from the microphone check and from More → Background in a call: the person
    stays sharp, the room is blurred, others see the same; the status shows about 15 fps.
25. A preset and your own image (JPEG/PNG/WebP, not over 10 MB) as backgrounds; switching between
    blur and images during a call does not drop the camera for others; your own image survives a
    reload and never appears in the server's logs.
26. Copy an image into `backgrounds/` on a running server: it appears in the picker with a preview
    without a restart; remove the chosen one: on the next join the camera shows no background and
    the picker says why.
27. With devtools CPU throttling (6x) the rate drops and the status says the computer cannot keep
    up, but the background stays; with the meeting tab hidden the others still see moving video.
28. With the background unavailable (no WebGL2, or the model blocked in devtools), turning the
    camera on opens the dialog; "Turn on the camera without a background" shows the camera,
    "Cancel" leaves it off.
29. Glass blocks from the picker (microphone check and More → Background): the person stays sharp,
    the background is a wall of blocks with seams; the blocks do not flicker as you move; switching
    blur ↔ glass blocks ↔ an image does not drop the camera for others.

Meeting notes (with the `kunveno-notes` add-on installed and `notes.enabled: true`; check 30 also
without it):

30. With `notes.enabled: false` the More menu has no notes item, the lobby never mentions notes,
    and `curl -s 127.0.0.1:8080/internal/notes` on the host prints `[]`.
31. More → "Take notes": within 10 s the "Notes" participant appears with "writes down the meeting",
    the header shows "Notes" with the sheet icon (no red dot) for everyone, a screen reader announces the change, and the
    "Take notes" switch is on.
32. Two people speak in turn (Russian with some English terms): with `notes.live: true` the tile of
    the "Notes" participant shows each phrase within about 2 s of its end, with the right name and
    time, and follows new phrases unless scrolled up; during a screen share the tile in the side
    column shows the latest phrase under "Notes".
33. Switching "Take notes" off: the Notes participant leaves within 10 s and everyone in the room
    gets the bar "Ready after the meeting:" with "Notes"; the file is named `<room>-YYYY-MM-DD-HHMM.md` and shows
    the room, the date, the time span, who spoke and the phrases in order. Switching notes off and
    on within one meeting adds "recording paused" and "recording resumed" lines to the same file.
34. Download with the interface in English and in Spanish: the fixed labels follow the language,
    the phrases stay as spoken.
35. Lobby: the room shows the notes mark, the "Meetings" button and "Notes, kept until
    16.10, 14:46" under it (day, month and time; the year only when it is not the current one).
    The dialog "Meetings" has the PIN filled in when this browser knows it, a show/hide eye,
    and a row "Notes" with "kept until …" and "Download"; a wrong PIN shows "Wrong PIN" and puts
    the focus in the PIN field; after `notes.keep` passes (try `keep: 2m`) the notes row disappears
    (the meeting stays listed for `meetings.keep`), and "Download" in a dialog still open reports
    "The notes have already been deleted".
36. The last person leaving ends the notes and the file holds the last phrase said before leaving;
    a new meeting in the room starts notes of its own, and the earlier meeting's notes stay listed.
37. Lobby → "Meetings (N)": every item of every meeting has "Delete"; pressing it asks "Delete?"
    in place with "Cancel" and "Delete" (no browser prompt); "Cancel" restores the row. With a wrong
    PIN the dialog shows "Wrong PIN" and puts the focus in the PIN field; with the right one only
    that item goes, the list and the room list update, and downloading it in a second tab with the
    dialog still open reports that it is gone. Notes still being taken (a meeting in progress with
    notes on) answer "The notes are still being taken or finished" and stay.

Meeting recording (with the `kunveno-recorder` add-on installed and `recording.enabled: true`; check
38 also without it):

38. With `recording.enabled: false` the More menu has no recording item, the lobby never mentions
    video (the "Meetings" caption and dialog list the notes only), and
    `curl -s 127.0.0.1:8080/internal/recording` on the host prints `[]`.
39. More → "Recording": within 10 s the "Recording" participant (bot) appears, the header shows
    "● Recording" for everyone, a screen reader announces the change, and the switch is on for
    everyone in the room.
40. Share a screen, speak, stop the share: the recording shows the screen while it is shared and a
    slate with the room name, the date and the speaker's name while nobody shares and the speaker's
    camera is off. With the speaker's camera on, the recording shows that camera instead of the
    slate; switching the camera off, on and off again during the meeting follows within a second.
41. Switching recording off: the Recording participant leaves, and within a minute everyone in the
    room gets the bar "Ready after the meeting:" with "Video"; the file plays with the screen and
    the audio of all. With notes on as well, the bar holds both buttons ("Video" and "Notes", each
    appearing when it is ready) and one ✕ ("Close") that dismisses both; at 360 px wide it wraps
    inside the screen, and a failed download shows its error inside the bar.
42. Lobby: the room shows "Meetings (N)". The dialog lists each meeting's video parts ("Video from
    10:55", each with its own time and "Download"): a wrong PIN shows "Wrong PIN"; the right one
    downloads the part. A large file (a meeting of an hour or more) downloads with progress, and a
    download interrupted in devtools (go offline, then online) resumes with a `Range` request
    instead of starting over.
43. Deleting a video part with the right PIN removes that part only; the meeting's other parts, notes
    and summary stay. A part being recorded is not listed until its upload.
44. The last person leaving ends the recording; the file appears in the lobby within a minute.
45. Restarting the server during a recording leaves no broken file: after the restart the lobby
    offers no half-written recording, and the data directory has no `*.tmp` files.
46. Deleting a room deletes its recording. Switching recording off and on again in the same meeting
    leaves one file, the later recording.
47. Meeting summary: with notes or recording on, Ann and Bob join, an echo or notes bot is in the
    room, Bob reloads the page (rejoins under the same name), then everyone leaves. The lobby shows
    above the "Meetings" caption a line like "10/15, 11:00 AM–11:27 AM · 27 min · 2 participants":
    the date, the start and end times, the duration and 2 people (the bot and the rejoin do not
    count); the dialog shows the same line under the room name, in each language (Russian
    "2 участника", Spanish "2 participantes"). After `docker compose restart server` the line is
    still there; a room with nothing kept shows no line, and after deleting the notes and the
    video the line goes with the button.
48. Backup channel: in devtools, run `localStorage['kunveno.tcpUntil'] = Date.now() + 864e5` and
    rejoin: the status reads "connected (backup channel)" with "try the fast one" next to it.
    Press it: audio drops for a couple of seconds, the status reads "connected", the button is
    gone and the key is removed. With UDP 7000 blocked by a firewall rule, pressing it brings the
    browser back to the backup channel within about 10 s.
49. Sign-in (`auth.enabled: true`, Yandex provider, `allow: ["*@example.com"]`). An organization
    account signs in from the sign-in screen and reaches the lobby, which shows its name and "Sign
    out". Joining a room asks for the PIN; the participant name is the account's, whatever the
    client sends.
50. A personal Yandex account (login without "@") is refused with a page saying the account is not
    allowed; no session cookie is set.
51. A room link (`/join/ops#pin=...` or the room's usual link) opened while signed out shows the
    sign-in screen; after sign-in the browser lands on that room with the PIN filled.
52. Guest by link: More → "Invite a guest" → 24 hours → "Create link". Open the link in a private
    window: only a name is asked, no PIN and no account; the guest joins, is marked "guest" in the
    list, and can speak, turn on the camera and share a screen. The lobby is out of reach: `/api/rooms`
    answers 401 in that window.
53. A guest cannot switch recording, notes or the echo test (the controls are absent or ignored)
    and cannot download notes or recordings. Recording an employee switched on goes on after the
    last employee leaves.
54. Revoke: with the guest in the room, the employee opens the dialog and presses "Revoke" on the
    link; the guest is disconnected at once and sees that the link was revoked. Opening the link
    again fails the same way.
55. Expiry: a link with a short life (set `invite_max: 1h` and choose 1 hour, or wait) opened after
    it expires is refused with "expired or revoked"; a guest already inside stays to the end of
    the meeting.
56. "Sign out" in the lobby returns to the sign-in screen; a reload stays signed out. After
    `docker compose exec server rm /var/lib/kunveno/auth.key && docker compose restart server`
    an open session is refused (401) and the employee must sign in again.
57. With a signed-in employee, "Meetings" → "Download" of a recording works (the file plays
    and a download interrupted and resumed continues).
58. With `auth.enabled: false` (or no `auth:` block) nothing changes: no sign-in screen, no "Invite
    a guest" in the menu, the lobby and the rooms open as before, `/api/info` reports no sign-in.

Meeting summary (with the `kunveno-notes` add-on's summary overlay installed, its model reachable,
and `notes.enabled: true`, `notes.summary: true`; check 59 also without them):

59. With `notes.summary: false` the "Meetings" dialog has no summary row and
    `curl -s 127.0.0.1:8080/internal/notes/summaries` on the host prints `[]`.
60. Take notes in a meeting, then everyone leaves: the dialog shows "Summary · being prepared"
    under the notes; within about two minutes (reopen the dialog) it reads "written automatically",
    and "Download" saves `<room>-<date>-<time>-summary.md` with the title, the meeting's times and the
    note that it was written automatically.
61. Switch notes on again in the same room before the summary is ready, then leave: the old summary
    never appears; a new one is prepared for the longer notes.
62. Delete the summary alone in the dialog: the notes stay, the summary row is gone and does not come
    back after a server restart. Delete the notes alone: the summary stays, downloads, and the lobby
    still offers "Meetings" ("Summary, kept until …") when nothing else is left.
63. Stop the model: the next meeting's summary reads "could not be written" after the add-on's three
    attempts.

Meeting history (with notes and recording on, `notes.keep: 2d`, `recording.keep: 2d`):

64. Two meetings in a room, each with notes: "Meetings (2)" lists both, newest first, each with its
    own notes; the earlier meeting's notes and summary did not change when the second began.
65. Recording switched off and on again within one meeting: the meeting lists two parts ("Video from
    …"), each downloads its own file.
66. Upgrade from 0.6 on a copy of a real stack (volumes backed up first): after the first start the
    room's last notes, summary and recording are listed under their meeting in "Meetings", the
    server log has "moved to the meeting history layout" lines, and a second restart moves nothing.
67. Grafana "Kept on disk" ("Хранится на диске") shows notes and recording bytes; deleting a part
    lowers the recording line within a minute.
68. A room removed from `config.yaml` (server restarted) keeps its files until they expire; adding
    the room back lists its meetings again.

Facilitator (`facilitator.enabled: true`, `facilitator.rooms` lists one room):

69. With `facilitator.enabled` off no room shows the facilitator switch.
70. The switch is shown in the listed room only, to people; a guest and an unlisted room do not see it.
71. With the add-on running: switching it on brings the bot into the room (listed with its own name,
    marked as a helper); switching it off removes it within 5 s; the last person leaving removes it too.
