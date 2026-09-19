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

const usage = `Shadowplay Observer - keeps NVIDIA Instant Replay switched on.

  install service      Register as a Windows service (runs as SYSTEM, needs admin/UAC)
  install autostart    Start at logon of the current user (no admin needed), starts it now
  uninstall service    Stop and remove the service
  uninstall autostart  Remove the autostart entry and stop the running instance
  status               Show service, autostart and Instant Replay status
  run                  Monitor in the foreground (for testing, Ctrl+C exits)
  press                Send the Instant Replay hotkey once (helper command of the service)

Option -debug (with install, run): writes verbose info to
shadowplay-observer.log next to the exe.

Use either the service or autostart. If both are installed, only one of them
is active at a time.
`

var backgroundMode bool // started from autostart, no console output

func main() {
	var args []string
	elevated := false
	for _, a := range os.Args[1:] {
		switch strings.ToLower(a) {
		case "-debug", "--debug":
			debugOn = true
		case "--background":
			backgroundMode = true
		case "--elevated":
			elevated = true
		default:
			args = append(args, a)
		}
	}
	if ok, _ := svc.IsWindowsService(); ok {
		runService()
		return
	}
	cmd, target := "help", ""
	if len(args) > 0 {
		cmd = strings.ToLower(args[0])
	}
	if len(args) > 1 {
		target = strings.ToLower(args[1])
	}

	switch cmd {
	case "install", "uninstall":
		switch target {
		case "service":
			if cmd == "install" {
				adminAction(cmd, target, elevated, func() error { return installService(debugOn) },
					fmt.Sprintf("Service %q installed and started. Log: %s\\observer.log", svcName, programDataDir()))
				warnIfOther(autostartRegistered(), "autostart is also registered; only one of them will be active")
			} else {
				adminAction(cmd, target, elevated, uninstallService, fmt.Sprintf("Service %q removed.", svcName))
			}
		case "autostart":
			var err error
			if cmd == "install" {
				if err = installAutostart(debugOn); err == nil {
					fmt.Printf("Autostart registered and started. Log: %s\\observer.log\n", dataDir())
					warnIfOther(serviceStatus() != "not installed", "the service is also installed; only one of them will be active")
				}
			} else {
				if err = uninstallAutostart(); err == nil {
					fmt.Println("Autostart removed and running instance stopped.")
				}
			}
			if err != nil {
				fmt.Println("Error:", err)
				os.Exit(1)
			}
		default:
			fmt.Printf("Usage: %s %s <service|autostart> [-debug]\n", os.Args[0], cmd)
			os.Exit(2)
		}
	case "status":
		fmt.Println("Service:       ", serviceStatus())
		fmt.Println("Autostart:     ", autostartStatus())
		fmt.Println("Instant Replay:", lastStateInFile(captureLogPath()), "(per CaptureCore.log)")
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

func warnIfOther(other bool, msg string) {
	if other {
		fmt.Println("Note:", msg)
	}
}

// adminAction runs fn with admin rights, restarting the exe via UAC if needed.
func adminAction(cmd, target string, elevated bool, fn func() error, okMsg string) {
	if !isElevated() {
		fmt.Println("Administrator rights required, restarting via UAC ...")
		if err := relaunchElevated(append([]string{cmd, target}, svcArgs(debugOn)...)); err != nil {
			fmt.Println("Error:", err)
			os.Exit(1)
		}
		return
	}
	err := fn()
	if err != nil {
		fmt.Println("Error:", err)
	} else {
		fmt.Println(okMsg)
	}
	if elevated {
		fmt.Print("Press Enter to close ...")
		bufio.NewReader(os.Stdin).ReadString('\n')
	}
	if err != nil {
		os.Exit(1)
	}
}
