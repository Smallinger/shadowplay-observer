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
| `status` | Show service, autostart and Instant Replay status |
| `run` | Run in the foreground (testing, Ctrl+C exits) |
| `press` | Send the hotkey once (helper command of the service) |
| `-debug` | With `install`/`run`: verbose log next to the exe |

**Service or autostart?** Autostart is the simplest: it runs in your session, needs no admin rights and
sends the hotkey itself. It starts when you log on. The service starts at boot, before anyone is
logged in, and launches a helper in the user session to send the hotkey. Use one of them. If both are
installed, a single-instance lock ensures only one is active at a time (the service takes over when the
autostart instance ends), otherwise both would toggle the hotkey and cancel each other out.

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
- If you turn Instant Replay off on purpose, it gets turned back on. Use `uninstall` to pause.
- Tested with the NVIDIA app only (not GeForce Experience).

## Files

| File | Contents |
|---|---|
| `main.go` | Commands and arguments |
| `service.go` | Service, installation, logging |
| `autostart.go` | Autostart (HKCU Run key), single-instance lock |
| `monitor.go` | Monitoring, switching back on |
| `nvapp.go` | NVIDIA app process, `console.log`, `ShareSettings.json` |
| `session.go` | Helper process in the user session, sending the hotkey |
| `debug.go` | `-debug` output |
