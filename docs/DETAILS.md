# Shadowplay Observer: details

This document explains everything the [README](../README.md) leaves out: every command, how the two
run modes differ, how the tool knows whether Instant Replay is on, how it switches it back on, and what
to look at when something does not work.

Contents

1. [What problem it solves](#1-what-problem-it-solves)
2. [Commands](#2-commands)
3. [Run modes: autostart or service](#3-run-modes-autostart-or-service)
4. [Tray icon and pausing](#4-tray-icon-and-pausing)
5. [How it detects the state](#5-how-it-detects-the-state)
6. [How it switches Instant Replay back on](#6-how-it-switches-instant-replay-back-on) (incl. readiness check)
7. [The hotkey, also when you changed it](#7-the-hotkey-also-when-you-changed-it)
8. [Reboots and app restarts](#8-reboots-and-app-restarts)
9. [Logs and `-debug`](#9-logs-and--debug)
10. [Code layout](#10-code-layout)
11. [Building and testing](#11-building-and-testing)
12. [Limitations](#12-limitations)
13. [Troubleshooting](#13-troubleshooting)
14. [Assets and licensing](#14-assets-and-licensing)

---

## 1. What problem it solves

In the NVIDIA app, Instant Replay (German UI: "Videobeweis") records the last minutes of gameplay into a
buffer so you can save a clip afterwards. It has a switch, and that switch tends to end up off: after a
reboot, a driver or app update, or a crash. You notice only when you want the clip and there is none.

Shadowplay Observer watches that switch and turns it back on. It does this the same way you would: it
presses the Instant Replay toggle hotkey. There is no supported API for it, so the hotkey is the most
robust way that does not touch NVIDIA's internals.

## 2. Commands

| Command | Effect |
|---|---|
| `install autostart` | Adds the exe to the current user's autostart (registry key `HKCU\...\Run`) and starts it right away. No admin rights needed. |
| `install service` | Registers a Windows service (automatic, delayed start, runs as SYSTEM) and starts it. Asks for admin rights via UAC. |
| `uninstall autostart` | Removes the autostart entry and stops the running instance (the tray icon is closed cleanly). |
| `uninstall service` | Stops and removes the service. |
| `pause [duration]` | Pauses monitoring. `pause 2h`, `pause 30m`; without a duration until `resume`. |
| `resume` | Resumes monitoring. |
| `status` | Shows service, autostart, pause state, whether NVIDIA ShadowPlay is ready, and the Instant Replay state. |
| `run` | Runs in the foreground with console output. Ctrl+C exits. |
| `run -tray` | Same, plus the tray icon (Exit in its menu also ends it). For starting from a shell without installing anything. |
| `hotkey` | Prints the Instant Replay toggle hotkey it would press and where it read it from. Presses nothing. |
| `press` | Presses the hotkey once. Internal helper of the service. |
| `-debug` | Option for `install` and `run`: writes a verbose log next to the exe (see [9](#9-logs-and--debug)). |

`install`/`uninstall` need the target (`service` or `autostart`), otherwise a usage line is shown.
`-debug` given to `install` is stored in the autostart entry or the service's command line, so it also
applies after every reboot.

## 3. Run modes: autostart or service

|  | Autostart | Service |
|---|---|---|
| Starts | when you log on | at boot, before anyone logs on |
| Admin rights | not needed | needed to install (UAC) |
| Runs as | you, in your session | SYSTEM, in session 0 |
| Tray icon | yes | no (services cannot have one) |
| Sends the hotkey | itself | through a helper process in your session |
| Log | `%LOCALAPPDATA%\ShadowplayObserver\observer.log` | `%ProgramData%\ShadowplayObserver\observer.log` |

**Recommendation:** use autostart. It is simpler, needs no admin rights and has the tray icon. Instant
Replay only matters while you are logged in and playing anyway.

**Autostart entry.** The registry value (named `ShadowplayObserver`) runs
`conhost.exe --headless "<exe>" run --background`. `conhost --headless` starts the console program
without any window, so nothing flashes at logon.

**Why the service needs a helper.** Windows runs services in a separate session (0) that has no access to
your desktop, so a service cannot send keystrokes to it. The service therefore starts the same exe with
your user token in your session (`<exe> press`, on desktop `winsta0\default`), which presses the keys and
exits. It uses the API calls `WTSQueryUserToken`, `DuplicateTokenEx` and `CreateProcessAsUser`.

**Service details.** Name `ShadowplayObserver`, display name "Shadowplay Observer". If it crashes it is
restarted after 5 s, 30 s and 60 s (the counter resets after a day).

**Only one instance at a time.** If the service and an autostart instance both ran, each would press the
toggle hotkey, and two presses cancel each other out. A named mutex (`Global\ShadowplayObserverInstance`)
prevents this:

- a second `run` / autostart instance says "already running" and exits;
- the service stays idle and checks every 30 s, so it takes over when the autostart instance ends.

You can install both, but only one is active at any time. Note that with the service active, `run -tray`
exits immediately for the same reason.

## 4. Tray icon and pausing

Shown in autostart mode and with `run -tray`. Windows 11 may put a new icon into the overflow flyout
behind the "^" arrow first; drag it out to keep it visible.

| Icon | Meaning |
|---|---|
| Green logo (`#76B900`) on a dark grey tile (`#1D1E1E`) | monitoring active |
| Red struck-through logo (`#A31621`) | paused |

- **Left click** toggles pause. **Right click** opens a menu: Pause/Resume, Exit.
- The tooltip shows the state and whether Instant Replay is on.
- The grey tile colour was measured from NVIDIA's own tray icon.

**How pausing works.** `pause`, the tray icon and the monitor share one small file:

```
%LOCALAPPDATA%\ShadowplayObserver\paused
```

It contains an expiry time (RFC 3339) or the word `indefinite`. The autostart instance and the service
both read it (the service, running as SYSTEM, looks in every profile under `C:\Users`), so `pause 2h`
works with either mode and the tray icon follows it within a second. An expired file is deleted.

While paused, Instant Replay is left alone, even if it is off. On resume it is switched back on if it is
off. The pause survives restarts of the tool.

## 5. How it detects the state

Two sources are combined. The file wins when they disagree.

**1. `ShareSettings.json`** (primary). Location:
`%LOCALAPPDATA%\NVIDIA Corporation\NVIDIA Overlay\ShareSettings.json`, field `video.irEnabled`.
The NVIDIA overlay writes this file immediately on every toggle, and reads the value again when it
starts, which is why Instant Replay comes back as you left it after a reboot. It is a persistent switch,
not just something in memory.

**2. `CaptureCore.log`.** Location: `%ProgramData%\NVIDIA Corporation\ShadowPlay\CaptureCore.log`.
The capture engine logs each change, for example:

```
CCaptureSession::EvaluateDVRStop: IR(0) HL(0) DVR(0)               -> off
CCaptureSession::EvaluateAutoDVRStart: Starting DVR capture. IR(1) -> on
DVR Active = true                                                  -> on
```

The log is read incrementally (every 2 s) and rotation is detected. It reacts quickly, but the file can
be stale after a restart, so the tool does not rely on it alone.

**Not used: the registry.** The registry (`HKCU\Software\NVIDIA Corporation\Global\ShadowPlay\NVSPCAPS`)
holds settings only. It does not change when you toggle Instant Replay, so it cannot tell on from off.

**Safety check.** A log line such as `IR(0)` alone does not make the tool press anything. Before it does,
it confirms that `irEnabled` really is `false` (or, if the file is missing, that the log state is off).

## 6. How it switches Instant Replay back on

When the state changes to off (and the tool is not paused):

1. **Debounce 2 s.** The state flips several times when a game starts. If it is back on by itself the tool
   does nothing.
2. **Wait until NVIDIA is ready** (see [6a](#6a-is-nvidia-shadowplay-ready)). Up to 3 minutes.
3. **Press the hotkey** (see [7](#7-the-hotkey-also-when-you-changed-it)). The keys go down modifiers
   first, and are released in reverse order.
4. **Confirm up to 15 s.** Success is `irEnabled: true` in the file or an "on" line in the log.
5. **Retry.** Up to 3 attempts. If the hotkey could not be sent, it waits 20 s before the next try; if it
   was sent but not confirmed, 5 s. After 3 failed attempts it stops and waits for the next event.

The hotkey only toggles. That is why the checks above matter: pressing it when Instant Replay is already
on would switch it off.

### 6a. Is NVIDIA ShadowPlay ready?

The hotkey only does something when all three parts of Instant Replay are running. Pressing it earlier
goes nowhere, or lands in whatever window has focus. So the tool checks first:

| Part | How it is checked |
|---|---|
| Service `NvContainerLocalSystem` | Windows service state is "running" |
| ShadowPlay capture container | a `nvcontainer.exe` outside session 0 whose command line contains `SPUser` (`...NvContainerUser%dSPUser.log`). It hosts the capture and encode threads. The command line is read with `NtQueryInformationProcess`. There are other `nvcontainer` processes too, so the marker matters. |
| `NVIDIA Overlay.exe` | the process exists. It registers and receives the hotkey. |

If something is missing, the log says what (`NVIDIA ShadowPlay not ready, waiting. Missing: ...`) and the
tool re-checks every 3 s for up to 3 minutes. Once everything is up it continues. If the time runs out it
gives up for now and remembers that it still has to switch Instant Replay on: as soon as everything is
ready, it does. `status` shows the current result in its `NVIDIA:` line, and `-debug` logs it at start.

This is separate from the startup status of the app after a reboot ([8](#8-reboots-and-app-restarts)):
that one waits for the app to report its state, this one makes sure the parts exist at all.

## 7. The hotkey, also when you changed it

You can change the hotkey in the NVIDIA app. The tool looks it up again on every press, in this order:

1. **`ShareSettings.json`**, `settings.shortcuts.DVRToggle` (a list of Windows virtual-key codes such as
   `[18,16,121]` = Alt+Shift+F10). The overlay writes it on every change and registers the hotkey from it,
   so this is what actually works.
2. **Registry**, `NVSPCAPS\IRToggleHKeyCount` and `IRToggleHKey0..`. The overlay copies the file's values
   there only when it starts (visible as `SetProperty: IN(IRToggleHKey0)` in `CaptureCore.log`), so it can
   be out of date until the app restarts. Hence only a fallback.
3. **NVIDIA default**, Alt+Shift+F10.

If you remove the shortcut in the app (`DVRToggle` is an empty list), there is nothing to press. The tool
logs `no hotkey is assigned ...` and does not retry until you assign one again.

Keys such as arrows, Home/End, Insert/Delete and Win need the "extended key" flag, otherwise Windows
sends the numpad variant. The tool sets it. Codes outside 1..255 are rejected and never pressed.

`shadowplay-observer.exe hotkey` shows what would be pressed and the source:

```
Instant Replay hotkey: Alt+Shift+F10 (from ShareSettings.json)
```

## 8. Reboots and app restarts

After a reboot the NVIDIA app restarts and may not yet be ready to receive the hotkey. The tool therefore
detects a new `NVIDIA Overlay.exe` by the **process start time**, not by the system uptime. (Uptime is
unreliable here: with Windows Fast Startup it does not reset on a reboot.)

For an app instance younger than 15 minutes, it waits up to 3 minutes for the app's own startup status
before acting. The app logs it in its `console.log`:

```
ShadowPlayService  GetInstantReplayEnableStatus returns:  { "status": true, ...
```

This also covers an app restart during a session (for example after a driver update): a new app instance
triggers the same procedure. If no startup status shows up, the tool falls back to `ShareSettings.json`
and, if that is unavailable too, does nothing rather than guess.

The service starts delayed after boot. As long as nobody is logged in there is no user session for the
helper, so an attempt fails and is retried. The NVIDIA overlay only starts after logon anyway, and the
tool waits for it as described above.

## 9. Logs and `-debug`

| File | Contents |
|---|---|
| `%ProgramData%\ShadowplayObserver\observer.log` | regular log of the service |
| `%LOCALAPPDATA%\ShadowplayObserver\observer.log` | regular log of autostart and `run` |
| `shadowplay-observer.log` next to the exe | only with `-debug`: verbose |

All files rotate at about 1 MB (the old one becomes `.old`).

A normal run logs only the interesting events, for example:

```
Monitoring started (...CaptureCore.log), Instant Replay per log: on
Instant Replay: on -> off
Attempt 1: hotkey sent
Instant Replay switched back on
```

With `-debug` the log also contains the environment at startup (exe path, user, session ids, system
uptime, the NVIDIA log paths, since when the overlay runs, the status per file and the resolved hotkey),
every recognised line of `CaptureCore.log`, each step of waiting for the app's startup status, and the
helper process' start and exit code. Run `install autostart -debug` or `install service -debug`, restart
Windows once, and send `shadowplay-observer.log` if something looks off.

## 10. Code layout

| File | Contents |
|---|---|
| `main.go` | Commands, argument parsing (`-debug`, `-tray`, `--background`, `--elevated`), UAC re-launch |
| `service.go` | Windows service, install/uninstall, `run`, log files with rotation |
| `autostart.go` | Autostart via the HKCU Run key, single-instance mutex, stopping instances |
| `monitor.go` | The monitor loop: state, log tailing, debounce, retries, pause handling |
| `nvapp.go` | NVIDIA app process start time, `console.log`, `ShareSettings.json` (`irEnabled`) |
| `hotkey.go` | Finds the current toggle hotkey; key names |
| `ready.go` | Checks that NVIDIA ShadowPlay (service, capture container, overlay) is up before pressing |
| `session.go` | Helper process in the user session, sending keys with `SendInput` |
| `pause.go` | The pause file (`pause`, `resume`) |
| `tray.go` | Tray icon, message loop, menu (plain Win32 through `syscall`) |
| `icon.go` | Small SVG path renderer that draws the tray icons |
| `debug.go` | `-debug` output |
| `assets/` | `logo.png` (README), `nvidia/*.svg` (embedded), `icon.svg` (own, unused) |

Only one external module is used: `golang.org/x/sys/windows`. The tray icon is plain Win32 calls, and
`icon.go` renders the icon SVGs itself (paths with lines, curves, arcs and both fill rules), so the exe
needs no SVG or GUI library and no files next to it.

## 11. Building and testing

```powershell
go build -ldflags "-s -w" -o shadowplay-observer.exe .    # needs Go 1.26+
go vet ./...
go test ./...
```

`golang.org/x/sys` v0.48 requires Go 1.26, so an older Go switches its toolchain automatically.

The tests cover the icon renderer and the hotkey parsing. The monitor talks to the real NVIDIA files
and processes, so it is tested by hand: run `shadowplay-observer.exe run -tray -debug`, switch Instant
Replay off with its hotkey, and watch the tool switch it back on.

`assets/logo.png` (the README logo) is generated from the same renderer:

```powershell
$env:WRITE_LOGO = "1"; go test -run TestWriteLogo
```

## 12. Limitations

- **Depends on NVIDIA's files.** `ShareSettings.json` and the log lines are not a public interface. An app
  update may change them; the patterns live in `monitor.go` and `nvapp.go`.
- **Only the global switch.** It checks that Instant Replay is enabled, not whether a specific game is
  being recorded at that moment.
- **Turning it off on purpose does not stick.** It is switched back on. Use `pause` or the tray icon.
- **Hotkey based.** The tool presses keys. If another program has grabbed the same hotkey, or the game
  blocks injected input, it cannot switch on. It then logs the failure.
- **Tested with the NVIDIA app only**, not with GeForce Experience.
- **One user.** Built for a single-user PC. With several profiles, the service takes the most recently
  written NVIDIA files.
- **Hard kill leaves a ghost icon.** If the process is killed hard (Task Manager), Windows keeps its tray
  icon until the mouse passes over it. `uninstall autostart`, Exit and Ctrl+C remove it cleanly.

## 13. Troubleshooting

| Symptom | Check |
|---|---|
| Nothing happens when Instant Replay goes off | `status`: is it paused? Is an instance running? Then read the log. |
| `run -tray` says "already running" | The service or autostart instance is active. `status` shows which. |
| Log says `no hotkey is assigned ...` | Assign a hotkey for the Instant Replay toggle in the NVIDIA app. |
| Log says `NVIDIA ShadowPlay not ready, waiting` | The named part of NVIDIA is not running (yet). `status` shows the same. Start or repair the NVIDIA app. The tool continues by itself. |
| Hotkey pressed but not confirmed | `hotkey` shows what is pressed. Does that match the app's setting? Another program may have taken the hotkey. |
| Log says `no active user session` or `WTSQueryUserToken: ...` | Service only: nobody is logged in (yet). It retries. |
| No tray icon | Look behind the "^" arrow in the notification area. Service mode has none. |
| After an update it stopped working | Compare `ShareSettings.json` (`video.irEnabled`) and `CaptureCore.log` with the patterns above. |

## 14. Assets and licensing

`assets/nvidia/replay.svg` and `replay_disabled.svg` are the Instant Replay icons of the NVIDIA app. They
were extracted from its overlay (the SVG sprite symbols `replay` and `replay_disabled` in
`osc/main.*.js`) and are embedded into the exe for the tray icon, so it matches the NVIDIA app visually.

They belong to NVIDIA, not to this project: they are excluded from this repository's MIT license (see
the [README](../README.md#license)), and are used here only for that visual match, for a tool that exists
to interoperate with the NVIDIA app. If you prefer not to ship them, swap them for your own drawing, for
example `assets/icon.svg`, and point `icon.go`'s `//go:embed` and `glyphActive`/`glyphPaused` at it.
`icon.go` only needs the SVG file to contain plain `<path d="...">` elements (no `<use>`/`<defs>`), each
optionally with `fill-rule="evenodd"`.
