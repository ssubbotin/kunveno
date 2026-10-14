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
