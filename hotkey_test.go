package main

import "testing"

func TestHotkeyFromShareSettings(t *testing.T) {
	const withKey = "{\"settings\":{\"shortcuts\":{\"DVRToggle\":[17,16,74],\"DVRSave\":[18,121]}}}"
	keys, found, err := hotkeyFromShareSettings([]byte(withKey))
	if err != nil || !found || formatHotkey(keys) != "Ctrl+Shift+J" {
		t.Errorf("changed hotkey: got %v found=%v err=%v (%s)", keys, found, err, formatHotkey(keys))
	}

	// The user removed the shortcut: found, but empty.
	keys, found, err = hotkeyFromShareSettings([]byte(`{"settings":{"shortcuts":{"DVRToggle":[]}}}`))
	if err != nil || !found || len(keys) != 0 {
		t.Errorf("removed hotkey: got %v found=%v err=%v", keys, found, err)
	}

	// Key absent: caller falls back to the registry.
	if _, found, _ := hotkeyFromShareSettings([]byte(`{"settings":{"shortcuts":{}}}`)); found {
		t.Error("absent key reported as found")
	}

	// Garbage and out-of-range codes are errors, never pressed.
	if _, _, err := hotkeyFromShareSettings([]byte(`not json`)); err == nil {
		t.Error("invalid json accepted")
	}
	if _, _, err := hotkeyFromShareSettings([]byte(`{"settings":{"shortcuts":{"DVRToggle":[18,999]}}}`)); err == nil {
		t.Error("out-of-range key code accepted")
	}
}

func TestFormatHotkey(t *testing.T) {
	for want, keys := range map[string][]uint16{
		"Alt+Shift+F10": {vkAlt, vkShift, vkF10},
		"Ctrl+Alt+M":    {vkCtrl, vkAlt, 'M'},
		"Alt+Home":      {vkAlt, 0x24},
		"Ctrl+Shift+F1": {vkCtrl, vkShift, 0x70},
		"Alt+VK_0xFF":   {vkAlt, 0xFF},
		"Alt+Num5":      {vkAlt, 0x65},
		"Shift+Delete":  {vkShift, 0x2E},
	} {
		if got := formatHotkey(keys); got != want {
			t.Errorf("formatHotkey(%v) = %q, want %q", keys, got, want)
		}
	}
	if !isExtendedKey(0x25) || isExtendedKey(vkF10) {
		t.Error("extended key detection wrong")
	}
}
