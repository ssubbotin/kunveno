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
35. Lobby: the room shows the notes mark, the "Last meeting" button and "Notes, kept until
    16.10, 14:46" under it (day, month and time; the year only when it is not the current one).
    The dialog "Last meeting" has the PIN filled in when this browser knows it, a show/hide eye,
    and a row "Notes" with "kept until …" and "Download"; a wrong PIN shows "Wrong PIN" and puts
    the focus in the PIN field; after `notes.keep` passes (try `keep: 2m`) the button disappears,
    and "Download" in a dialog still open reports "The notes have already been deleted".
36. The last person leaving ends the recording and the file holds the last phrase said before
    leaving; the first phrase of a new meeting in the room replaces the old notes.
37. Lobby → "Last meeting" → "Delete…": the dialog asks "What to delete?" with a choice for each
    item kept ("Video", "Notes", and "Everything", preselected, when both are there), then "Cancel"
    and "Delete" (no browser prompt); "Cancel" returns to the dialog with everything intact.
    Keyboard focus: opening the confirmation puts it on "Cancel", "Cancel" returns it to
    "Delete…", an error returns it to "Delete…" (to the PIN field after a wrong PIN). With a wrong
    PIN "Delete" shows "Wrong PIN" once, also for "Everything"; with the right one the dialog
    closes, the marks and the button update in the room list, and downloading in a second tab with
    the dialog still open reports "The notes have already been deleted". Notes in progress: with
    notes switched off in a running meeting, open the lobby dialog and press "Delete…"; then in the
    room switch notes on again and speak one phrase (the notes reopen the same file); now deleting
    "Notes" shows "The notes are still being taken or finished: stop them, or try again in a
    minute", returns to the dialog, and the file stays (the same answer comes right after a
    switch-off while the Notes participant is still leaving). Deleting the notes of a meeting still
    in progress (notes switched off) and switching notes on again starts a fresh file holding only
    the new phrases.

Meeting recording (with the `kunveno-recorder` add-on installed and `recording.enabled: true`; check
38 also without it):

38. With `recording.enabled: false` the More menu has no recording item, the lobby never mentions
    video (the "Last meeting" caption and dialog list the notes only), and
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
42. Lobby: the room shows the recording mark and "Last meeting" with "Video, kept until …" (or
    "Video and notes, kept until …" with the nearer of the two times). The dialog lists "● Video"
    and "Notes", each with its own time and "Download": a wrong PIN shows "Wrong PIN"; the right one
    downloads the file. A large file (a meeting of an hour or more) downloads with progress, and a
    download interrupted in devtools (go offline, then online) resumes with a `Range` request
    instead of starting over.
43. Lobby → "Last meeting" → "Delete…" → "Video" with the right PIN removes the file and the
    recording mark; "Everything" removes both and the button. While the room is being recorded,
    deleting "Video" shows "The meeting is still being recorded: switch the recording off or try
    again in a minute" (409) and the file stays; with "Everything" the notes are still deleted and
    the video's failure is listed.
44. The last person leaving ends the recording; the file appears in the lobby within a minute.
45. Restarting the server during a recording leaves no broken file: after the restart the lobby
    offers no half-written recording, and the data directory has no `*.tmp` files.
46. Deleting a room deletes its recording. Switching recording off and on again in the same meeting
    leaves one file, the later recording.
47. Meeting summary: with notes or recording on, Ann and Bob join, an echo or notes bot is in the
    room, Bob reloads the page (rejoins under the same name), then everyone leaves. The lobby shows
    above the "Last meeting" caption a line like "10/15, 11:00 AM–11:27 AM · 27 min · 2 participants":
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
57. With a signed-in employee, "Last meeting" → "Download" of a recording works (the file plays
    and a download interrupted and resumed continues).
58. With `auth.enabled: false` (or no `auth:` block) nothing changes: no sign-in screen, no "Invite
    a guest" in the menu, the lobby and the rooms open as before, `/api/info` reports no sign-in.

Meeting summary (with the `kunveno-notes` add-on's summary overlay installed, its model reachable,
and `notes.enabled: true`, `notes.summary: true`; check 59 also without them):

59. With `notes.summary: false` the "Last meeting" dialog has no summary row and
    `curl -s 127.0.0.1:8080/internal/notes/summaries` on the host prints `[]`.
60. Take notes in a meeting, then everyone leaves: the dialog shows "Summary · being prepared"
    under the notes; within about two minutes (reopen the dialog) it reads "written automatically",
    and "Download" saves `<room>-<date>-<time>-summary.md` with the title, the meeting's times and the
    note that it was written automatically.
61. Switch notes on again in the same room before the summary is ready, then leave: the old summary
    never appears; a new one is prepared for the longer notes.
62. Delete the notes in the dialog: the summary row disappears with them. Stop the model: the next
    meeting's summary reads "could not be written" after the add-on's three attempts.
