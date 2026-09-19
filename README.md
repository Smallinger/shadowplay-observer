<p align="center">
  <img src="assets/logo.png" alt="Shadowplay Observer" width="160">
</p>

# Shadowplay Observer

A small Windows tool written in Go that keeps **Instant Replay** of the NVIDIA app switched on. If it
gets turned off, for example after a reboot or an app update, it switches it back on.

- One `exe`, no dependencies besides Windows.
- Runs as autostart at logon (no admin needed) or as a Windows service.
- Tray icon with a pause toggle: green = active, red = paused.
- Follows a hotkey you changed in the NVIDIA app.

## Build

Requires Go 1.26+ and Windows 10/11 with the NVIDIA app installed.

```powershell
go build -ldflags "-s -w" -o shadowplay-observer.exe .
go test ./...
```

## Use

Put the exe where it should stay, then:

```powershell
shadowplay-observer.exe install autostart    # start at logon, with tray icon
shadowplay-observer.exe status               # what is running, is it paused, is Instant Replay on
shadowplay-observer.exe pause 2h             # pause for two hours (resume: `resume`)
shadowplay-observer.exe uninstall autostart
```

Other commands: `install service` / `uninstall service` (Windows service, needs admin), `run -tray`
(run from a shell with tray icon), `hotkey`, and `-debug` for a verbose log next to the exe.

Everything else, how it works, logs and limitations: **[docs/DETAILS.md](docs/DETAILS.md)**.

## Note on assets

The tray icons are NVIDIA's own Instant Replay icons (`assets/nvidia/`). They belong to NVIDIA, so keep
this repository private or replace them before publishing.
