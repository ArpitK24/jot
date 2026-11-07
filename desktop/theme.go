package main

import "fmt"

// SetTheme saves the appearance: "system", "light" or "dark".
func (a *App) SetTheme(theme string) error {
	switch theme {
	case "system":
		theme = ""
	case "light", "dark":
	default:
		return fmt.Errorf("unknown theme %q", theme)
	}
	a.prefs.Theme = theme
	return a.prefs.save()
}

// themeName is the saved appearance, with "" reported as "system".
func (a *App) themeName() string {
	if a.prefs.Theme == "" {
		return "system"
	}
	return a.prefs.Theme
}
