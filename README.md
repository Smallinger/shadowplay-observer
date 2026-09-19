# Shadowplay Observer

Kleiner Windows-Dienst in Go, der dafür sorgt, dass der **Videobeweis** (NVIDIA „Instant Replay“)
der NVIDIA App **dauerhaft eingeschaltet** bleibt. Schaltet er sich aus, etwa nach einem Neustart
oder App-Update, schaltet der Dienst ihn wieder ein.

Eine einzelne `exe`, die sich selbst als Dienst registrieren kann. Keine Abhängigkeiten außer Windows.

## Installation

Voraussetzung: NVIDIA App (mit „NVIDIA Overlay“), Windows 10/11, Go 1.26+ zum Bauen.

```powershell
go build -ldflags "-s -w" -o shadowplay-observer.exe .
shadowplay-observer.exe install          # fragt per UAC nach Adminrechten
```

Die exe vorher an ihren endgültigen Ort legen, der Dienst zeigt auf ihren Pfad.

| Befehl | Wirkung |
|---|---|
| `install` | Als Dienst registrieren (Autostart, verzögert) und starten |
| `uninstall` | Dienst stoppen und entfernen |
| `status` | Dienst- und Videobeweis-Status anzeigen |
| `run` | Im Vordergrund ausführen (Test, Strg+C beendet) |
| `press` | Hotkey einmal senden (Hilfsbefehl des Dienstes) |
| `-debug` | Bei `install`/`run`: ausführliches Log neben der exe |

## Logs

- `%ProgramData%\ShadowplayObserver\observer.log`: normales Log des Dienstes
- `shadowplay-observer.log` neben der exe: nur mit `-debug` (auch als Dienst, wenn mit `install -debug`
  installiert). Enthält Umgebung, erkannte Log-Zeilen, App-Status und Helferprozess.

Beide Dateien rotieren bei etwa 1 MB (`.old`).

## Wie es funktioniert

**Zustand erkennen.** Zwei Quellen, die Datei hat Vorrang:

1. `%LOCALAPPDATA%\NVIDIA Corporation\NVIDIA Overlay\ShareSettings.json`, Feld `video.irEnabled`.
   NVIDIA schreibt es bei jedem Umschalten sofort, und beim Start liest die App genau diesen Wert.
   Der Dienst läuft als SYSTEM und findet die Datei über `C:\Users\*`.
2. `%ProgramData%\NVIDIA Corporation\ShadowPlay\CaptureCore.log`, Zeilen wie
   `EvaluateDVRStop: IR(0)` und `DVR Active = true`.

In der Registry liegt der Zustand nicht, dort stehen nur Einstellungen.

**Wieder einschalten.** Der Dienst sendet den in der Registry (`IRToggleHKey*`) hinterlegten
Videobeweis-Hotkey (Standard Alt+Shift+F10). Ein Dienst läuft in Session 0 und kann keine Tasten in die
Benutzersitzung senden. Deshalb startet er dieselbe exe als Helfer (`press`) mit dem Token des
angemeldeten Benutzers auf `winsta0\default`. Danach wartet er auf die Bestätigung
(`irEnabled: true` oder Log). Bis zu 3 Versuche, dann wartet er auf das nächste Ereignis.

**Nach Neustart oder App-Neustart.** Der Dienst erkennt eine neue Instanz von `NVIDIA Overlay.exe`
über deren Startzeit, nicht über die Systemlaufzeit (die ist wegen Schnellstart unzuverlässig). Er
wartet auf den Start-Status der App (`GetInstantReplayEnableStatus` in `console.log`), bevor er den
Hotkey sendet.

**Absicherung.** Der Hotkey schaltet nur um. Damit er nie versehentlich ausschaltet, sendet der Dienst
ihn nur, wenn `irEnabled` tatsächlich `false` ist bzw. bei fehlender Datei der Zustand aus den Logs
sicher „aus“ ist.

## Grenzen

- Abhängig von Dateiformaten der NVIDIA App. Ein App-Update kann sie ändern, die Muster stehen in
  `monitor.go` und `nvapp.go`.
- Der Dienst prüft nur den globalen Schalter, nicht, ob in einem bestimmten Spiel gerade aufgezeichnet wird.
- Wer den Videobeweis absichtlich ausschaltet, bekommt ihn wieder eingeschaltet. Zum Pausieren `uninstall`.
- Nur mit der NVIDIA App getestet (nicht GeForce Experience).

## Dateien

| Datei | Inhalt |
|---|---|
| `main.go` | Befehle und Argumente |
| `service.go` | Dienst, Installation, Logging |
| `monitor.go` | Überwachung, Wiedereinschalten |
| `nvapp.go` | NVIDIA-App-Prozess, `console.log`, `ShareSettings.json` |
| `session.go` | Helferprozess in der Benutzersitzung, Hotkey senden |
| `debug.go` | `-debug`-Ausgaben |
