<p align="center">
  <img src="assets/logo.png" alt="Shadowplay Observer" width="160">
</p>

# Shadowplay Observer

A small Windows service written in Go that keeps **Instant Replay** of the NVIDIA app switched on
permanently. If it gets turned off, for example after a reboot or an app update, the service turns
it back on.

A single `exe` that can register itself as a service. No dependencies besides Windows.

## Installation

Requirements: NVIDIA app (with "NVIDIA Overlay"), Windows 10/11, Go 1.26+ to build.

```powershell
go build -ldflags "-s -w" -o shadowplay-observer.exe .
shadowplay-observer.exe install autostart     # start at logon, no admin needed
# or
shadowplay-observer.exe install service       # Windows service, asks for admin rights via UAC
```

Put the exe in its final location first, both variants point to its path.

| Command | Effect |
|---|---|
| `install autostart` | Add to the current user's autostart (HKCU `Run`) and start it now. No admin needed. |
| `install service` | Register a Windows service (delayed autostart, runs as SYSTEM) and start it. Needs admin (UAC). |
| `uninstall autostart` | Remove the autostart entry and stop the running instance |
| `uninstall service` | Stop and remove the service |
| `pause [duration]` | Pause monitoring, e.g. `pause 2h`, `pause 30m`; without duration until `resume` |
| `resume` | Resume monitoring |
| `status` | Show service, autostart, pause and Instant Replay status |
| `run` | Run in the foreground with console output (Ctrl+C exits) |
| `run -tray` | Same, plus the tray icon (pause toggle, Exit). Handy to start from a shell without installing anything. |
| `press` | Send the hotkey once (helper command of the service) |
| `-debug` | With `install`/`run`: verbose log next to the exe |

**Service or autostart?** Autostart is the simplest: it runs in your session, needs no admin rights and
sends the hotkey itself. It starts when you log on. The service starts at boot, before anyone is
logged in, and launches a helper in the user session to send the hotkey. Use one of them. If both are
installed, a single-instance lock ensures only one is active at a time (the service takes over when the
autostart instance ends), otherwise both would toggle the hotkey and cancel each other out.

**Tray icon.** In autostart mode and with `run -tray` a small icon appears in the notification area (Windows 11 may put it
in the overflow flyout first: drag it out to keep it visible). Green logo (`#76B900`) on a dark grey tile (`#1D1E1E`, like NVIDIA's own tray icon) = monitoring active, red struck-through logo (`#A31621`) =
paused. Left click toggles pause, right click opens a menu (Pause/Resume, Exit). The tooltip shows the
state of Instant Replay.

**Pausing.** `pause` and the tray icon share one small file,
`%LOCALAPPDATA%\ShadowplayObserver\paused` (an expiry time, or `indefinite`), which both the autostart
instance and the service look at. So `pause 2h` works with either mode, and the tray icon reflects it.
While paused, Instant Replay is left alone; on resume it is switched back on if it is off.

The autostart entry runs `conhost.exe --headless "<exe>" run --background`, so no console window shows up.

## Logs

- Service: `%ProgramData%\ShadowplayObserver\observer.log`
- Autostart / `run`: `%LOCALAPPDATA%\ShadowplayObserver\observer.log`
- `shadowplay-observer.log` next to the exe: only with `-debug` (also for the service/autostart when
  installed with `-debug`). Contains the environment, recognized log lines, app status and helper process.

All files rotate at about 1 MB (`.old`).

## How it works

**Detecting the state.** Two sources, the file takes precedence:

1. `%LOCALAPPDATA%\NVIDIA Corporation\NVIDIA Overlay\ShareSettings.json`, field `video.irEnabled`.
   NVIDIA writes it immediately on every toggle, and on startup the app reads exactly this value.
   The service runs as SYSTEM and finds the file via `C:\Users\*`.
2. `%ProgramData%\NVIDIA Corporation\ShadowPlay\CaptureCore.log`, lines such as
   `EvaluateDVRStop: IR(0)` and `DVR Active = true`.

The state is not stored in the registry, which only holds settings.

**Turning it back on.** The service sends the Instant Replay hotkey stored in the registry
(`IRToggleHKey*`, default Alt+Shift+F10). A service runs in session 0 and cannot send keystrokes into
the user session. So it launches the same exe as a helper (`press`) with the logged-in user's token on
`winsta0\default`. Afterwards it waits for confirmation (`irEnabled: true` or the log). Up to 3
attempts, then it waits for the next event.

**After a reboot or app restart.** The service detects a new `NVIDIA Overlay.exe` instance by its
process start time, not by system uptime (which is unreliable because of Fast Startup). It waits for
the app's startup status (`GetInstantReplayEnableStatus` in `console.log`) before sending the hotkey.

**Safety.** The hotkey only toggles. To make sure it never turns Instant Replay off by accident, the
service only sends it when `irEnabled` is actually `false`, or, if the file is missing, when the log
state is reliably "off".

## Limitations

- Depends on file formats of the NVIDIA app. An app update may change them, the patterns are in
  `monitor.go` and `nvapp.go`.
- Only the global switch is checked, not whether a specific game is currently being recorded.
- If you turn Instant Replay off on purpose, it gets turned back on. Use `pause` or the tray icon to pause.
- Tested with the NVIDIA app only (not GeForce Experience).

## Files

| File | Contents |
|---|---|
| `main.go` | Commands and arguments |
| `service.go` | Service, installation, logging |
| `autostart.go` | Autostart (HKCU Run key), single-instance lock |
| `pause.go` | Pause state file (`pause` / `resume`) |
| `tray.go` | Tray icon (plain Win32) |
| `icon.go` | Tiny SVG path renderer that draws the tray icons from the embedded `assets/nvidia/*.svg` |
| `monitor.go` | Monitoring, switching back on |
| `nvapp.go` | NVIDIA app process, `console.log`, `ShareSettings.json` |
| `session.go` | Helper process in the user session, sending the hotkey |
| `debug.go` | `-debug` output |

## Assets and licensing

`assets/nvidia/replay.svg` and `replay_disabled.svg` are the Instant Replay icons of the NVIDIA app
(extracted from its overlay, sprite symbols `replay` and `replay_disabled`), embedded into the exe for
the tray icon. They belong to NVIDIA. Keep the repository private, or replace them (for example with
`assets/icon.svg`, a similar own drawing) before publishing.
