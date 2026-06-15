// Package tui implements the Raven terminal UI rendering layer.
//
// Design follows the Raven CLI UX Design System:
//   - Primary palette: ANSI-256 safe with 16-color fallbacks
//   - Usage rules: never more than 3 colors in a single view
//   - Background: always terminal default (respects user theme)
//   - Accent: Electric Blue reserved for interactive elements and key info
package tui

// Color represents a terminal color using ANSI 256-color codes.
type Color struct {
	Code256  int    // ANSI 256-color code (0-255)
	Fallback string // ANSI 16-color fallback name (e.g., "cyan", "red")
}

// Foreground ANSI code for 256-color terminals.
func (c Color) Fg() string {
	return ansiFg256(c.Code256)
}

// Background ANSI code for 256-color terminals.
func (c Color) Bg() string {
	return ansiBg256(c.Code256)
}

// BoldForeground ANSI code for bold text.
func (c Color) BoldFg() string {
	return ansiBoldFg256(c.Code256)
}

// DimForeground ANSI code for dim text.
func (c Color) DimFg() string {
	return ansiDimFg256(c.Code256)
}

// Primary Palette — ANSI-safe, works on light + dark terminals
var (
	RavenBlack   = Color{Code256: 232, Fallback: "black"}     // #1a1a2e — deepest bg
	DeepGray     = Color{Code256: 236, Fallback: "dark gray"} // #2d2d44 — panels, borders
	DarkSurface  = Color{Code256: 233, Fallback: "black"}     // #16162a — deepest bg
	ElectricBlue = Color{Code256: 81, Fallback: "cyan"}       // #4fc3f7 — primary accent
	Silver       = Color{Code256: 249, Fallback: "dark gray"} // #b0bec5 — secondary text
	SoftPurple   = Color{Code256: 99, Fallback: "magenta"}    // #7c4dff — thinking/planning
	SuccessGreen = Color{Code256: 71, Fallback: "green"}      // #66bb6a — success, done
	WarningAmber = Color{Code256: 214, Fallback: "yellow"}    // #ffa726 — warnings, attention
	ErrorRed     = Color{Code256: 203, Fallback: "red"}       // #ef5350 — errors, failures
	MutedTeal    = Color{Code256: 73, Fallback: "cyan"}       // #4db6ac — info, tool names
)

// Opacity multipliers for the raven silhouette density levels.
const (
	DensitySolid = 1.0  // █
	DensityDark  = 0.7  // ▓
	DensityMid   = 0.45 // ▒
	DensityLight = 0.25 // ░
	DensityEdge  = 0.10 // (space only)
)

// Theme holds the active color scheme. Respects NO_COLOR and TERM=dumb.
type Theme struct {
	Accent   Color
	Success  Color
	Error    Color
	Warning  Color
	Dim      Color
	Thinking Color
	Muted    Color
	Border   Color
	Text     Color // always terminal default — we tint via context

	UseANSI256 bool
}

// DefaultTheme returns the standard Raven theme with ANSI-256 colors.
func DefaultTheme() Theme {
	return Theme{
		Accent:     ElectricBlue,
		Success:    SuccessGreen,
		Error:      ErrorRed,
		Warning:    WarningAmber,
		Dim:        Silver,
		Thinking:   SoftPurple,
		Muted:      MutedTeal,
		Border:     DeepGray,
		UseANSI256: true,
	}
}

// NoColorTheme returns a theme stripped of color escapes.
func NoColorTheme() Theme {
	return Theme{UseANSI256: false}
}

// AnsiStart returns the ANSI escape to begin a styled span.
func (t Theme) AnsiStart(c Color, bold, dim bool) string {
	if !t.UseANSI256 {
		return ""
	}
	if bold {
		return c.BoldFg()
	}
	if dim {
		return c.DimFg()
	}
	return c.Fg()
}

// AnsiReset returns the ANSI reset sequence.
func (t Theme) AnsiReset() string {
	if !t.UseANSI256 {
		return ""
	}
	return ansiReset
}

// Styled applies color and style to text with the given attributes.
func (t Theme) Styled(s string, c Color, bold, dim bool) string {
	if !t.UseANSI256 {
		return s
	}
	return t.AnsiStart(c, bold, dim) + s + t.AnsiReset()
}

// AccentText returns text styled as accent (Electric Blue).
func (t Theme) AccentText(s string) string { return t.Styled(s, t.Accent, false, false) }

// SuccessText returns text styled as success (green).
func (t Theme) SuccessText(s string) string { return t.Styled(s, t.Success, false, false) }

// ErrorText returns text styled as error (red).
func (t Theme) ErrorText(s string) string { return t.Styled(s, t.Error, false, false) }

// WarningText returns text styled as warning (amber).
func (t Theme) WarningText(s string) string { return t.Styled(s, t.Warning, false, false) }

// DimText returns dim secondary text.
func (t Theme) DimText(s string) string { return t.Styled(s, t.Dim, false, true) }

// ThinkingText returns text styled for thinking states.
func (t Theme) ThinkingText(s string) string { return t.Styled(s, t.Thinking, false, true) }

// MutedText returns muted teal text for tool names.
func (t Theme) MutedText(s string) string { return t.Styled(s, t.Muted, false, false) }

// Bold returns bold text.
func (t Theme) Bold(s string) string {
	if !t.UseANSI256 {
		return s
	}
	return ansiBold + s + ansiReset
}
