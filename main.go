package main

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"golang.org/x/sys/windows/svc"
)

func notifyInterrupt(c chan os.Signal) { signal.Notify(c, os.Interrupt) }

const usage = `Shadowplay Observer - hält den NVIDIA-Videobeweis (Instant Replay) eingeschaltet.

  install     Als Windows-Dienst registrieren (Autostart) und starten
              (mit -debug: ausführliches Log neben der exe)
  uninstall   Dienst stoppen und entfernen
  status      Dienst- und Videobeweis-Status anzeigen
  run         Überwachung im Vordergrund (zum Testen, Strg+C beendet)
  press       Videobeweis-Hotkey einmal senden (Hilfsbefehl des Dienstes)

Option -debug (bei install, run): schreibt ausführliche Infos nach
shadowplay-observer.log neben der exe.
`

func main() {
	var args []string
	for _, a := range os.Args[1:] {
		if strings.EqualFold(a, "-debug") || strings.EqualFold(a, "--debug") {
			debugOn = true
		} else {
			args = append(args, a)
		}
	}
	if ok, _ := svc.IsWindowsService(); ok {
		runService()
		return
	}
	cmd := "help"
	if len(args) > 0 {
		cmd = strings.ToLower(args[0])
	}
	elevated := len(args) > 1 && args[len(args)-1] == "--elevated"

	switch cmd {
	case "install", "uninstall":
		if !isElevated() {
			fmt.Println("Administratorrechte nötig, starte neu mit UAC ...")
			if err := relaunchElevated(append([]string{cmd}, svcArgs(debugOn)...)); err != nil {
				fmt.Println("Fehler:", err)
				os.Exit(1)
			}
			return
		}
		var err error
		if cmd == "install" {
			err = installService(debugOn)
		} else {
			err = uninstallService()
		}
		if err != nil {
			fmt.Println("Fehler:", err)
		} else if cmd == "install" {
			fmt.Printf("Dienst %q installiert und gestartet. Log: %s\\observer.log\n", svcName, dataDir())
		} else {
			fmt.Printf("Dienst %q entfernt.\n", svcName)
		}
		if elevated {
			fmt.Print("Enter zum Schließen ...")
			bufio.NewReader(os.Stdin).ReadString('\n')
		}
		if err != nil {
			os.Exit(1)
		}
	case "status":
		fmt.Println("Dienst:      ", serviceStatus())
		fmt.Println("Videobeweis: ", lastStateInFile(captureLogPath()), "(laut CaptureCore.log)")
	case "run":
		runConsole()
	case "press":
		if err := pressHotkey(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		fmt.Print(usage)
	}
}
