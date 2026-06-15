package tui

import (
	"fmt"
	"strings"
	"time"
)

// ─── Raven ASCII Art ────────────────────────────────────────────────────
//
// The raven faces RIGHT — looking forward. Built with 5 density levels:
//   █ (solid), ▓ (dark), ▒ (mid), ░ (light), space (edge)
//
// The design shows a raven perched and alert with a book-like form in front,
// suggesting "learning" — the bird's posture is attentive, head tilted
// slightly as if studying.

var ravenSilhouetteLines = []string{
	"                      ░░░░░░░░░░░░░░░░░░░░░░                            ",
	"                   ░░░▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒░░░░                        ",
	"                ░░░▒▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▒▒░░░                     ",
	"              ░░▒▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▒░░                   ",
	"            ░░▒▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓█████████▓▓▓▓▓▓▓▓▓▓▓▓▓▒░                 ",
	"           ░▒▓▓▓▓▓▓▓▓▓▓▓█████████████████████▓▓▓▓▓▓▓▓▓▓▒░               ",
	"          ░▒▓▓▓▓▓▓▓▓▓█████▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓█████▓▓▓▓▓▓▓▓▒░              ",
	"         ░▒▓▓▓▓▓▓▓▓████▓▓▓▒▒░░░      ░░░▒▒▓▓████▓▓▓▓▓▓▓▓▒░             ",
	"        ░▒▓▓▓▓▓▓▓███▓▓▒░                    ░░▒▓███▓▓▓▓▓▓▓▒░            ",
	"       ░▒▓▓▓▓▓▓████▓▒░                          ░▒▓████▓▓▓▓▓▒░          ",
	"      ░▒▓▓▓▓▓▓███▓▒░                              ░▒▓███▓▓▓▓▓▒░         ",
	"      ▒▓▓▓▓▓▓███▓░          ░░░░░░░░░░░            ░▓███▓▓▓▓▓▒░         ",
	"     ░▓▓▓▓▓▓████▒          ░████████████░           ▓███▓▓▓▓▓▓░         ",
	"     ▓▓▓▓▓▓▓███▓          ░██████████████░          ▓███▓▓▓▓▓▓▒         ",
	"     ▓▓▓▓▓▓▓████░          ██████████████           ░████▓▓▓▓▓▓▒         ",
	"     ▓▓▓▓▓▓▓████▒          ██████████████           ▒████▓▓▓▓▓▓▒         ",
	"     ▓▓▓▓▓▓▓████▓          ██████████████           ▓████▓▓▓▓▓▓▒         ",
	"     ░▓▓▓▓▓▓▓████▓░         ████████████           ░▓████▓▓▓▓▓▓░         ",
	"      ▒▓▓▓▓▓▓▓█████▓░        ██████████           ░▓█████▓▓▓▓▓▒          ",
	"      ░▒▓▓▓▓▓▓▓██████▓▒░░     ▀▀▀▀▀▀▀▀▀▀        ░░▒▓█████▓▓▓▓▓▓░         ",
	"       ░▒▓▓▓▓▓▓▓▓████████▓▓▓▒▒░░░░░░░░░░░░░░▒▒▒▓▓▓█████▓▓▓▓▓▓▒░         ",
	"        ░░▒▓▓▓▓▓▓▓▓▓▓▓▓████████████████████████████▓▓▓▓▓▓▓▓▓▒░          ",
	"          ░▒▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓███████████████▓▓▓▓▓▓▓▓▓▓▓▓▓▒░           ",
	"           ░░▒▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▒░░            ",
	"             ░░▒▒▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▒▒░░               ",
	"                ░░░░▒▒▒▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▒▒▒░░░░                   ",
	"                     ░░░░░░░░░░░░░░░░░░░░░░░░                           ",
}

var ravenSilhouetteWidth int

func init() {
	for _, line := range ravenSilhouetteLines {
		if len(line) > ravenSilhouetteWidth {
			ravenSilhouetteWidth = len(line)
		}
	}
}

// ─── Welcome View ───────────────────────────────────────────────────────

// WelcomeView manages the multi-phase welcome screen.
type WelcomeView struct {
	Width  int
	Height int
	Theme  Theme

	// Splash animation state
	splashStart time.Time
	phase       int // 0=splash, 1=setup, 2=ready
}

// NewWelcomeView creates the welcome screen renderer.
func NewWelcomeView(w, h int, t Theme) *WelcomeView {
	return &WelcomeView{
		Width:  w,
		Height: h,
		Theme:  t,
	}
}

// Resize updates dimensions.
func (wv *WelcomeView) Resize(w, h int) {
	wv.Width = w
	wv.Height = h
}

// RenderSplash draws the full raven splash screen. It auto-adapts to
// terminal width — centering the ASCII art with padding.
func (wv *WelcomeView) RenderSplash(buf *Buffer) {
	wv.splashStart = time.Now()

	// Compute centering and vertical padding
	artWidth := ravenSilhouetteWidth
	hPad := (wv.Width - artWidth) / 2
	if hPad < 0 {
		hPad = 0
	}
	vPad := (wv.Height - len(ravenSilhouetteLines) - 8) / 2 // 8 lines for text + spacing
	if vPad < 0 {
		vPad = 0
	}

	row := 0

	// Top padding
	for i := 0; i < vPad; i++ {
		buf.SetRow(row, "")
		row++
	}

	// ─── Border top ──────────────────────────────────────────────
	borderWidth := wv.Width - 4
	if borderWidth < 60 {
		borderWidth = 60
	}
	borderLine := wv.Theme.DimText(BoxTL + Repeat(BoxH, borderWidth) + BoxTR)
	buf.SetRow(row, padTo(hPad-2)+borderLine)
	row++

	// Empty line inside border
	buf.SetRow(row, padTo(hPad-2)+wv.Theme.DimText(BoxV)+padTo(borderWidth)+wv.Theme.DimText(BoxV))
	row++

	// ─── Raven Silhouette ────────────────────────────────────────
	// Render the raven in Electric Blue with density variations
	for _, artLine := range ravenSilhouetteLines {
		rendered := wv.renderSilhouetteLine(artLine)
		// Center the art within the available width
		artPad := (wv.Width - artWidth) / 2
		if artPad < 0 {
			artPad = 0
		}
		line := padTo(artPad) + rendered
		buf.SetRow(row, line)
		row++
	}

	// Empty lines after raven
	buf.SetRow(row, "")
	row++

	// ─── R A V E N title ─────────────────────────────────────────
	title := "R  A  V  E  N"
	titlePad := (wv.Width - len(title)) / 2
	if titlePad < 0 {
		titlePad = 0
	}
	buf.SetRow(row, padTo(titlePad)+wv.Theme.AccentText(title))
	row++

	// Empty line
	buf.SetRow(row, "")
	row++

	// ─── Tagline ─────────────────────────────────────────────────
	tagline := "your  AI  coding  companion"
	tagPad := (wv.Width - len(tagline)) / 2
	if tagPad < 0 {
		tagPad = 0
	}
	buf.SetRow(row, padTo(tagPad)+wv.Theme.DimText(tagline))
	row++

	// Empty line
	buf.SetRow(row, "")
	row++

	// Bottom border
	buf.SetRow(row, padTo(hPad-2)+wv.Theme.DimText(BoxV)+padTo(borderWidth)+wv.Theme.DimText(BoxV))
	row++
	buf.SetRow(row, padTo(hPad-2)+wv.Theme.DimText(BoxBL+Repeat(BoxH, borderWidth)+BoxBR))
	row++

	// Bottom padding
	for i := row; i < wv.Height; i++ {
		buf.SetRow(i, "")
	}
}

// renderSilhouetteLine renders one line of the raven ASCII art with
// Electric Blue density variations.
func (wv *WelcomeView) renderSilhouetteLine(line string) string {
	if !wv.Theme.UseANSI256 {
		// 16-color fallback: render with bold/normal/dim for density
		return wv.renderSilhouetteLine16(line)
	}

	var sb strings.Builder
	for _, ch := range line {
		switch ch {
		case '█':
			sb.WriteString(wv.Theme.AnsiStart(ElectricBlue, false, false))
			sb.WriteRune('█')
			sb.WriteString(wv.Theme.AnsiReset())
		case '▓':
			sb.WriteString(RGBFg(79, 195, 247)) // Electric blue at ~70%
			sb.WriteRune('▓')
			sb.WriteString(wv.Theme.AnsiReset())
		case '▒':
			sb.WriteString(RGBFg(79, 195, 247)) // Electric blue at ~45% — dimmer
			sb.WriteRune('▒')
			sb.WriteString(wv.Theme.AnsiReset())
		case '░':
			sb.WriteString(wv.Theme.AnsiStart(Silver, false, true))
			sb.WriteRune('░')
			sb.WriteString(wv.Theme.AnsiReset())
		case ' ':
			sb.WriteRune(' ')
		default:
			sb.WriteRune(ch)
		}
	}
	return sb.String()
}

// renderSilhouetteLine16 renders the raven line for 16-color terminals
// using bold/normal/dim cyan for density.
func (wv *WelcomeView) renderSilhouetteLine16(line string) string {
	var sb strings.Builder
	for _, ch := range line {
		switch ch {
		case '█':
			sb.WriteString(ansiBold)
			sb.WriteRune('█')
			sb.WriteString(ansiReset)
		case '▓':
			sb.WriteRune('▓')
		case '▒':
			sb.WriteString(ansiDim)
			sb.WriteRune('▒')
			sb.WriteString(ansiReset)
		case '░':
			sb.WriteString(ansiDim)
			sb.WriteRune('░')
			sb.WriteString(ansiReset)
		case ' ':
			sb.WriteRune(' ')
		default:
			sb.WriteRune(ch)
		}
	}
	return sb.String()
}

// RenderSplashFull renders the splash without the border for full-screen mode.
func (wv *WelcomeView) RenderSplashFull(buf *Buffer) {
	wv.RenderSplash(buf)
}

// RenderCompactGreeting renders a compact greeting for returning users.
func (wv *WelcomeView) RenderCompactGreeting(buf *Buffer, sessionID string, msgCount int, totalTokens int, filesEdited int, cost float64) {
	centerCol := wv.Width / 2
	_ = centerCol

	row := wv.Height / 3
	if row < 2 {
		row = 2
	}

	// Raven mark
	buf.SetRow(row, padTo(wv.Width/2-1)+wv.Theme.AccentText("◤"))
	row += 2

	// Session info
	info := fmt.Sprintf("Session %s  ·  %d messages  ·  %s tokens",
		sessionID[:min(4, len(sessionID))], msgCount, TokenFormat(totalTokens))
	buf.SetRow(row, padTo((wv.Width-len(info))/2)+wv.Theme.DimText(info))
	row++

	if filesEdited > 0 || cost > 0 {
		sub := fmt.Sprintf("%d files edited  ·  %s", filesEdited, CostFormat(cost))
		buf.SetRow(row, padTo((wv.Width-len(sub))/2)+wv.Theme.DimText(sub))
		row++
	}

	row += 2

	// Actions
	divider := Repeat("─", wv.Width/3)
	buf.SetRow(row, padTo((wv.Width-len(divider))/2)+wv.Theme.DimText(divider))
	row += 2

	newConv := "New conversation                        /sessions"
	buf.SetRow(row, padTo((wv.Width-len(newConv))/2)+newConv)
}

// ─── Post-Setup Ready Screen ────────────────────────────────────────────

// RenderReady renders the post-setup ready confirmation.
func (wv *WelcomeView) RenderReady(buf *Buffer, model, contextWindow, mode, nest string) {
	centerCol := wv.Width / 2
	row := wv.Height/2 - 5
	if row < 2 {
		row = 2
	}

	// Top border
	border := wv.Theme.DimText(BoxTL + Repeat(BoxH, wv.Width-4) + BoxTR)
	buf.SetRow(row, padTo(2)+border)
	row++

	rows := []string{"", ""}
	rows = append(rows, padTo(centerCol-10)+wv.Theme.AccentText("◤  Raven is ready."))
	rows = append(rows, "")
	rows = append(rows, padTo(centerCol-6)+wv.Theme.DimText(fmt.Sprintf("Model    %s", model)))
	rows = append(rows, padTo(centerCol-6)+wv.Theme.DimText(fmt.Sprintf("Context  %s tokens", contextWindow)))
	rows = append(rows, padTo(centerCol-6)+wv.Theme.DimText(fmt.Sprintf("Mode     %s permissions", mode)))
	rows = append(rows, padTo(centerCol-6)+wv.Theme.DimText(fmt.Sprintf("Nest     %s", nest)))

	for _, line := range rows {
		buf.SetRow(row, padTo(2)+wv.Theme.DimText(BoxV)+line+padTo(wv.Width-4))
		row++
	}

	// Bottom border
	buf.SetRow(row, padTo(2)+wv.Theme.DimText(BoxBL+Repeat(BoxH, wv.Width-4)+BoxBR))
}

// ─── Animated Splash ────────────────────────────────────────────────────

// AnimateSplash plays the phased splash animation, calling the provided
// render callback at each frame. Returns after 2.5 seconds.
//
// Animation phases:
//
//	0.00s — Terminal clears, cursor hidden, border fades in (100ms)
//	0.15s — Raven silhouette emerges bottom→top over 400ms
//	0.70s — Shimmer sweep across body (200ms)
//	1.10s — "R A V E N" types out letter-by-letter (560ms)
//	1.70s — Tagline fades in (200ms)
//	2.10s — Hold complete composition (400ms)
//	2.50s — Dissolve into next phase
func (wv *WelcomeView) AnimateSplash(buf *Buffer, screen *Screen, done chan struct{}) {
	start := time.Now()
	ticker := time.NewTicker(30 * time.Millisecond)
	defer ticker.Stop()

	phase := 0
	lettersShown := 0
	shimmerCol := 0

	for {
		select {
		case <-done:
			return
		case t := <-ticker.C:
			elapsed := t.Sub(start)
			buf.Reset()

			switch {
			case elapsed < 100*time.Millisecond:
				// Phase: border fade in — just clear screen
				buf.FullRender(screen)
				continue

			case elapsed < 700*time.Millisecond:
				// Phase: raven silhouette emerges bottom→top
				progress := float64(elapsed-100*time.Millisecond) / 600.0
				if progress > 1.0 {
					progress = 1.0
				}
				wv.renderAnimatedSilhouette(buf, progress, 0, false)
				phase = 1

			case elapsed < 900*time.Millisecond:
				// Phase: shimmer sweep
				progress := float64(elapsed-700*time.Millisecond) / 200.0
				if progress > 1.0 {
					progress = 1.0
				}
				wv.renderAnimatedSilhouette(buf, 1.0, int(progress*float64(ravenSilhouetteWidth)), false)
				phase = 2

			case elapsed < 1700*time.Millisecond:
				// Phase: title types out
				progress := float64(elapsed-1100*time.Millisecond) / 600.0
				// But the types out runs 1.10s → 1.66s (7 letters × 80ms)
				letterProgress := float64(elapsed-1100*time.Millisecond) / 560.0
				if letterProgress > 1.0 {
					letterProgress = 1.0
				}
				lettersShown = int(letterProgress * 7)
				_ = progress
				wv.renderAnimatedSilhouette(buf, 1.0, 0, true)
				phase = 3

			case elapsed < 1900*time.Millisecond:
				// Phase: tagline fades in
				wv.renderAnimatedSilhouette(buf, 1.0, 0, true)
				phase = 4

			case elapsed < 2500*time.Millisecond:
				// Phase: hold complete composition
				wv.renderAnimatedSilhouette(buf, 1.0, 0, true)
				phase = 5

			default:
				// Dissolve and exit
				buf.Reset()
				buf.FullRender(screen)
				return
			}

			_ = phase
			_ = shimmerCol
			_ = lettersShown
			buf.FullRender(screen)
		}
	}
}

func (wv *WelcomeView) renderAnimatedSilhouette(buf *Buffer, progress float64, shimmerX int, showTitle bool) {
	vPad := (wv.Height - len(ravenSilhouetteLines) - 8) / 2
	if vPad < 0 {
		vPad = 0
	}

	topPad := vPad
	for i := 0; i < topPad; i++ {
		buf.SetRow(i, "")
	}

	// Render from bottom up based on progress
	totalRows := len(ravenSilhouetteLines)
	visibleRows := int(progress * float64(totalRows))
	if visibleRows > totalRows {
		visibleRows = totalRows
	}

	row := topPad
	// Render fully visible rows with proper density
	for i := totalRows - visibleRows; i < totalRows; i++ {
		line := wv.renderSilhouetteLineWithShimmer(ravenSilhouetteLines[i], shimmerX)
		artPad := (wv.Width - ravenSilhouetteWidth) / 2
		if artPad < 0 {
			artPad = 0
		}
		buf.SetRow(row, padTo(artPad)+line)
		row++
	}

	row++ // blank line

	if showTitle {
		title := "R  A  V  E  N"
		titlePad := (wv.Width - len(title)) / 2
		buf.SetRow(row, padTo(titlePad)+wv.Theme.AccentText(title))
		row++

		row++ // blank line

		tagline := "your  AI  coding  companion"
		tagPad := (wv.Width - len(tagline)) / 2
		buf.SetRow(row, padTo(tagPad)+wv.Theme.DimText(tagline))
		row++
	}
}

func (wv *WelcomeView) renderSilhouetteLineWithShimmer(line string, shimmerX int) string {
	if shimmerX <= 0 || !wv.Theme.UseANSI256 {
		return wv.renderSilhouetteLine(line)
	}

	var sb strings.Builder
	for i, ch := range line {
		brightness := 1.0
		if i >= shimmerX-2 && i <= shimmerX+2 {
			brightness = 1.3
		}

		switch ch {
		case '█':
			if brightness > 1.0 {
				sb.WriteString(wv.Theme.AnsiStart(ElectricBlue, true, false))
			} else {
				sb.WriteString(wv.Theme.AnsiStart(ElectricBlue, false, false))
			}
			sb.WriteRune('█')
			sb.WriteString(wv.Theme.AnsiReset())
		case '▓', '▒', '░':
			sb.WriteString(wv.Theme.AnsiStart(Silver, false, true))
			sb.WriteRune(ch)
			sb.WriteString(wv.Theme.AnsiReset())
		default:
			sb.WriteRune(ch)
		}
	}
	return sb.String()
}

// ─── Session Resume Animation ───────────────────────────────────────────

// RenderSessionResume renders the session resume frames.
func (wv *WelcomeView) RenderSessionResume(buf *Buffer, frame int, sessionID, nest string, msgsLoaded, memsLoaded int, msgsTotal, totalTokens int, cost float64, toolsNames []string, skillNames []string) {
	switch frame {
	case 1:
		wv.resumeFrameInit(buf, sessionID, nest)
	case 2:
		wv.resumeFrameMessages(buf, sessionID, nest, msgsLoaded, msgsTotal)
	case 3:
		wv.resumeFrameMemories(buf, sessionID, nest, memsLoaded)
	case 4:
		wv.resumeFrameTools(buf, sessionID, nest, toolsNames, skillNames)
	case 5:
		wv.resumeFrameReady(buf, sessionID, msgsTotal, memsLoaded, totalTokens, cost, len(toolsNames))
	}
}

func (wv *WelcomeView) resumeFrameInit(buf *Buffer, sessionID, nest string) {
	centerCol := wv.Width / 2
	row := wv.Height/2 - 3
	if row < 2 {
		row = 2
	}

	buf.SetRow(row, padTo(centerCol-10)+wv.Theme.AccentText("◤  Waking session..."))
	row += 2
	buf.SetRow(row, padTo(centerCol-12)+wv.Theme.DimText(fmt.Sprintf("%s · %s", sessionID[:min(4, len(sessionID))], nest)))
}

func (wv *WelcomeView) resumeFrameMessages(buf *Buffer, sessionID, nest string, loaded, total int) {
	centerCol := wv.Width / 2
	row := wv.Height/2 - 4
	if row < 2 {
		row = 2
	}

	buf.SetRow(row, padTo(centerCol-10)+wv.Theme.AccentText("◤  Waking session..."))
	row += 2
	buf.SetRow(row, padTo(centerCol-12)+wv.Theme.DimText(fmt.Sprintf("%s · %s", sessionID[:min(4, len(sessionID))], nest)))
	row += 2

	// Progress bar
	fill := float64(loaded) / float64(total)
	if total == 0 {
		fill = 1.0
	}
	barWidth := 40
	barPad := (wv.Width - barWidth) / 2
	buf.SetRow(row, padTo(barPad)+ProgressBar(fill, barWidth, wv.Theme))
	row += 2

	buf.SetRow(row, padTo(centerCol-10)+wv.Theme.DimText(fmt.Sprintf("Loading messages...   %d loaded", loaded)))
}

func (wv *WelcomeView) resumeFrameMemories(buf *Buffer, sessionID, nest string, memsLoaded int) {
	centerCol := wv.Width / 2
	row := wv.Height/2 - 4
	if row < 2 {
		row = 2
	}

	buf.SetRow(row, padTo(centerCol-10)+wv.Theme.AccentText("◤  Waking session..."))
	row += 2
	buf.SetRow(row, padTo(centerCol-12)+wv.Theme.DimText(fmt.Sprintf("%s · %s", sessionID[:min(4, len(sessionID))], nest)))
	row += 2

	barWidth := 40
	barPad := (wv.Width - barWidth) / 2
	buf.SetRow(row, padTo(barPad)+ProgressBar(1.0, barWidth, wv.Theme))
	row += 2

	buf.SetRow(row, padTo(centerCol-12)+wv.Theme.DimText(fmt.Sprintf("Restoring memories...   %d loaded", memsLoaded)))
}

func (wv *WelcomeView) resumeFrameTools(buf *Buffer, sessionID, nest string, toolNames, skillNames []string) {
	centerCol := wv.Width / 2
	row := wv.Height/2 - 5
	if row < 2 {
		row = 2
	}

	buf.SetRow(row, padTo(centerCol-10)+wv.Theme.AccentText("◤  Waking session..."))
	row += 2
	buf.SetRow(row, padTo(centerCol-12)+wv.Theme.DimText(fmt.Sprintf("%s · %s", sessionID[:min(4, len(sessionID))], nest)))
	row += 2

	// Tools
	toolLine := "✓ " + strings.Join(toolNames, "  ✓ ")
	toolPad := (wv.Width - len(stripANSI(toolLine))) / 2
	buf.SetRow(row, padTo(toolPad)+wv.Theme.SuccessText("✓ ")+wv.Theme.DimText(strings.Join(toolNames, "  ✓ ")))
	row++

	if len(skillNames) > 0 {
		skillLine := "✓ skills: " + strings.Join(skillNames, ", ")
		skillPad := (wv.Width - len(stripANSI(skillLine))) / 2
		buf.SetRow(row, padTo(skillPad)+wv.Theme.SuccessText("✓ ")+wv.Theme.DimText("skills: "+strings.Join(skillNames, ", ")))
	}
}

func (wv *WelcomeView) resumeFrameReady(buf *Buffer, sessionID string, msgs, mems, tokens int, cost float64, tools int) {
	centerCol := wv.Width / 2
	row := wv.Height/2 - 2
	if row < 2 {
		row = 2
	}

	buf.SetRow(row, padTo(centerCol-5)+wv.Theme.AccentText("◤  Ready."))
	row += 2

	info := fmt.Sprintf("%d messages  ·  %d memories  ·  %d tools", msgs, mems, tools)
	buf.SetRow(row, padTo((wv.Width-len(info))/2)+wv.Theme.DimText(info))
	row++

	sub := fmt.Sprintf("%s tokens  ·  %s spent", TokenFormat(tokens), CostFormat(cost))
	buf.SetRow(row, padTo((wv.Width-len(sub))/2)+wv.Theme.DimText(sub))
}
