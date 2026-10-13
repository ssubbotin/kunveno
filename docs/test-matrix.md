# Release test matrix

Run before every release. Record date, versions and results in the table.

| # | Case | Chrome Ubuntu | Firefox Ubuntu | Chrome Windows | Firefox Windows |
|---|------|---------------|----------------|----------------|-----------------|
| 1 | Join `ops` with wrong PIN shows "Wrong PIN" and stays in the lobby | | | | |
| 2 | Join with correct PIN; name appears for everyone within 1 s | | | | |
| 3 | Speak; the speaking highlight appears for others | | | | |
| 4 | Mute; `muted` badge appears for others; unmute clears it | | | | |
| 5 | Share screen (full screen and a single window); others see it within 2 s | | | | |
| 6 | While sharing, another participant's Share button is disabled | | | | |
| 7 | Stop sharing from the app button and from the browser's own bar; the view clears for others | | | | |
| 8 | Close the tab while sharing; others see the sharer gone and the share cleared within 2 s | | | | |
| 9 | Disable the network for 5 s; status shows reconnecting, then connected, audio resumes | | | | |
| 10 | 16th participant gets "The room is full" (use `tools/loadgen -n 15`) | | | | |
| 11 | 15 participants (14 loadgen + 1 browser) for 10 minutes: no audio dropouts, server CPU noted | | | | |
| 12 | "Copy link" in the lobby (room chosen, PIN entered); open the link in another browser: the room is chosen, the PIN filled in, the mic check shown; Join enters the room | | | | |
| 13 | "Invite" inside a room; open the link in a private window: as in 12; after joining the address bar shows `/` | | | | |
| 14 | A link to a deleted room opens the lobby with "The room in the link no longer exists" and the room list | | | | |
