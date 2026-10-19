package main

import (
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// press is the key press for a key named as Bubble Tea names it: "enter",
// "esc", "ctrl+c", "shift+tab", "f2", "pgup", or one character.
func press(name string) tea.KeyPressMsg {
	var mod tea.KeyMod
	for {
		switch {
		case strings.HasPrefix(name, "ctrl+") && len(name) > len("ctrl+"):
			mod |= tea.ModCtrl
			name = strings.TrimPrefix(name, "ctrl+")
			continue
		case strings.HasPrefix(name, "alt+") && len(name) > len("alt+"):
			mod |= tea.ModAlt
			name = strings.TrimPrefix(name, "alt+")
			continue
		case strings.HasPrefix(name, "shift+") && len(name) > len("shift+"):
			mod |= tea.ModShift
			name = strings.TrimPrefix(name, "shift+")
			continue
		}
		break
	}
	named := map[string]rune{
		"enter": tea.KeyEnter, "esc": tea.KeyEscape, "tab": tea.KeyTab, "backspace": tea.KeyBackspace,
		"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
		"home": tea.KeyHome, "end": tea.KeyEnd, "pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown,
		"delete": tea.KeyDelete, "insert": tea.KeyInsert,
		"f1": tea.KeyF1, "f2": tea.KeyF2, "f3": tea.KeyF3, "f4": tea.KeyF4,
	}
	if code, ok := named[name]; ok {
		return tea.KeyPressMsg{Code: code, Mod: mod}
	}
	if name == "space" {
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " ", Mod: mod}
	}
	if r, size := utf8.DecodeRuneInString(name); size == len(name) && mod != 0 {
		return tea.KeyPressMsg{Code: r, Mod: mod}
	}
	return tea.KeyPressMsg{Text: name, Mod: mod}
}

// asUIKey is a key press or paste message as the pane handlers take it.
func asUIKey(msg tea.Msg) uiKey {
	switch msg := msg.(type) {
	case tea.PasteMsg:
		return uiKey{paste: true, text: msg.Content}
	case tea.KeyPressMsg:
		return uiKey{key: msg}
	}
	panic("not a key press or paste")
}
