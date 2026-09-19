package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// The Instant Replay toggle hotkey can be changed by the user in the NVIDIA app. Where it is
// stored, in order of trust:
//
//  1. ShareSettings.json  settings.shortcuts.DVRToggle, list of virtual-key codes. The overlay
//     writes it on every change and registers the hotkey from it, so it is what really works.
//  2. Registry            NVSPCAPS\IRToggleHKey*, which the overlay only copies from (1) at start
//     (visible as "SetProperty: IN(IRToggleHKey0)" in CaptureCore.log), so it can be stale.
//  3. Alt+Shift+F10       the NVIDIA default.
//
// The hotkey is resolved every time it is pressed, so a change needs no restart.

const (
	vkShift = 0x10
	vkCtrl  = 0x11
	vkAlt   = 0x12
	vkF10   = 0x79
)

var defaultHotkey = []uint16{vkAlt, vkShift, vkF10}

var errNoHotkey = errors.New("no hotkey is assigned to the Instant Replay toggle in the NVIDIA app")

// hotkeyFromShareSettings extracts DVRToggle. found is false if the key is absent; an empty
// list with found == true means the user removed the shortcut.
func hotkeyFromShareSettings(data []byte) (keys []uint16, found bool, err error) {
	var s struct {
		Settings struct {
			Shortcuts map[string][]int `json:"shortcuts"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, false, err
	}
	v, ok := s.Settings.Shortcuts["DVRToggle"]
	if !ok {
		return nil, false, nil
	}
	for _, k := range v {
		if k < 1 || k > 255 {
			return nil, true, fmt.Errorf("invalid key code %d in DVRToggle", k)
		}
		keys = append(keys, uint16(k))
	}
	return keys, true, nil
}

func hotkeyFromRegistry() []uint16 {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\NVIDIA Corporation\Global\ShadowPlay\NVSPCAPS`, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()
	get := func(name string) (uint32, bool) {
		b, _, err := k.GetBinaryValue(name)
		if err != nil || len(b) < 4 {
			return 0, false
		}
		return binary.LittleEndian.Uint32(b), true
	}
	n, ok := get("IRToggleHKeyCount")
	if !ok || n == 0 || n > 6 {
		return nil
	}
	var keys []uint16
	for i := range n {
		v, ok := get(fmt.Sprintf("IRToggleHKey%d", i))
		if !ok || v == 0 || v > 0xFF {
			return nil
		}
		keys = append(keys, uint16(v))
	}
	return keys
}

// resolveHotkey returns the current toggle hotkey and where it came from. It returns
// errNoHotkey if the user removed the shortcut in the NVIDIA app (nothing we could press).
func resolveHotkey() (keys []uint16, source string, err error) {
	if p := overlayFile("ShareSettings.json"); p != "" {
		if b, e := os.ReadFile(p); e == nil {
			if k, found, e2 := hotkeyFromShareSettings(b); e2 == nil && found {
				if len(k) == 0 {
					return nil, "ShareSettings.json", errNoHotkey
				}
				return k, "ShareSettings.json", nil
			}
		}
	}
	if k := hotkeyFromRegistry(); k != nil {
		return k, "registry", nil
	}
	return defaultHotkey, "default", nil
}

// vkName gives a readable name for the virtual-key codes hotkeys are made of.
func vkName(vk uint16) string {
	switch {
	case vk == vkShift:
		return "Shift"
	case vk == vkCtrl:
		return "Ctrl"
	case vk == vkAlt:
		return "Alt"
	case vk == 0x5B || vk == 0x5C:
		return "Win"
	case vk >= 0x70 && vk <= 0x87:
		return fmt.Sprintf("F%d", vk-0x6F)
	case vk >= '0' && vk <= '9', vk >= 'A' && vk <= 'Z':
		return string(rune(vk))
	case vk >= 0x60 && vk <= 0x69:
		return fmt.Sprintf("Num%d", vk-0x60)
	}
	names := map[uint16]string{
		0x08: "Backspace", 0x09: "Tab", 0x0D: "Enter", 0x1B: "Esc", 0x20: "Space",
		0x21: "PageUp", 0x22: "PageDown", 0x23: "End", 0x24: "Home",
		0x25: "Left", 0x26: "Up", 0x27: "Right", 0x28: "Down",
		0x2D: "Insert", 0x2E: "Delete", 0xC0: "`", 0xBD: "-", 0xBB: "=",
	}
	if n, ok := names[vk]; ok {
		return n
	}
	return fmt.Sprintf("VK_0x%02X", vk)
}

func formatHotkey(keys []uint16) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = vkName(k)
	}
	return strings.Join(parts, "+")
}

// isExtendedKey: keys that need KEYEVENTF_EXTENDEDKEY, otherwise Windows sends the numpad variant.
func isExtendedKey(vk uint16) bool {
	switch {
	case vk >= 0x21 && vk <= 0x28, vk == 0x2D, vk == 0x2E, vk == 0x5B, vk == 0x5C, vk == 0x5D,
		vk == 0x6F, vk == 0x90, vk == 0xA3, vk == 0xA5:
		return true
	}
	return false
}
