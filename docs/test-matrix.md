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
9. Disable the network for 5 s: the status shows reconnecting, then connected, and audio resumes.
10. The 16th participant gets "The room is full" (use `tools/loadgen -n 15`).
11. 15 participants (14 from loadgen plus one browser) for 10 minutes: no audio dropouts; note the
    server's CPU.
12. "Copy link" in the lobby (room chosen, PIN entered); open the link in another browser: the room
    is chosen, the PIN filled in, the mic check shown; Join enters the room.
13. "Invite" inside a room; open the link in a private window: as in 12; after joining the address
    bar shows `/`.
14. A link to a deleted room opens the lobby with "The room in the link no longer exists" and the
    room list.
