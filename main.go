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

  install     Register as a Windows service (autostart) and start it
              (with -debug: verbose log next to the exe)
  uninstall   Stop and remove the service
  status      Show service and Instant Replay status
  run         Monitor in the foreground (for testing, Ctrl+C exits)
  press       Send the Instant Replay hotkey once (helper command of the service)

Option -debug (with install, run): writes verbose info to
shadowplay-observer.log next to the exe.
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
			fmt.Println("Administrator rights required, restarting via UAC ...")
			if err := relaunchElevated(append([]string{cmd}, svcArgs(debugOn)...)); err != nil {
				fmt.Println("Error:", err)
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
			fmt.Println("Error:", err)
		} else if cmd == "install" {
			fmt.Printf("Service %q installed and started. Log: %s\\observer.log\n", svcName, dataDir())
		} else {
			fmt.Printf("Service %q removed.\n", svcName)
		}
		if elevated {
			fmt.Print("Press Enter to close ...")
			bufio.NewReader(os.Stdin).ReadString('\n')
		}
		if err != nil {
			os.Exit(1)
		}
	case "status":
		fmt.Println("Service:       ", serviceStatus())
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
