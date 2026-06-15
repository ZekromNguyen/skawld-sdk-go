package main

import (
	"context"
	"fmt"
	"time"

	"github.com/skawld/skawld-sdk-go"
	"github.com/skawld/skawld-sdk-go/cmd/raven/internal/tui"
)

// ─── Modal Dispatch ─────────────────────────────────────────────────────────

// renderModal renders the current modal overlay to the screen.
func renderModal(typ ModalType, ms *modalState, renderer *tui.Renderer, screen *tui.Screen) {
	switch typ {
	case modalModelPicker:
		picker := tui.ModelPicker{Width: screen.Width, Height: screen.Height, Theme: renderer.Theme}
		renderModelPicker(screen, &picker, ms)
	case modalSessionBrowser:
		browser := tui.SessionBrowser{Width: screen.Width, Height: screen.Height, Theme: renderer.Theme}
		renderSessionBrowser(screen, &browser, ms)
	case modalSettings:
		sp := tui.SettingsPage{Width: screen.Width, Height: screen.Height, Theme: renderer.Theme}
		renderSettings(screen, &sp, ms)
	case modalMemoryBrowser:
		mb := tui.MemoryBrowser{Width: screen.Width, Height: screen.Height, Theme: renderer.Theme}
		renderMemories(screen, &mb, ms)
	case modalExport:
		ed := tui.ExportDialog{Width: screen.Width, Height: screen.Height, Theme: renderer.Theme}
		renderExportDialog(screen, &ed, ms)
	case modalCost:
		cd := tui.CostDialog{Width: screen.Width, Height: screen.Height, Theme: renderer.Theme}
		ms.costData = tui.CostData{
			Model:        renderer.Views.Status.Model,
			InputTokens:  renderer.Views.Status.InputTokens,
			OutputTokens: renderer.Views.Status.OutputTokens,
			InputCost:    renderer.Views.Status.Cost * 0.4,
			OutputCost:   renderer.Views.Status.Cost * 0.6,
			TotalCost:    renderer.Views.Status.Cost,
		}
		renderCostDialog(screen, &cd, ms)
	case modalTheme:
		ts := tui.ThemeSwitcher{Width: screen.Width, Height: screen.Height, Theme: renderer.Theme}
		renderThemePicker(screen, &ts, ms)
	case modalCompact:
		renderCompactDialog(screen, renderer)
	}
}

// handleModalKey dispatches a key event to the appropriate modal handler and
// returns the action to take.
func handleModalKey(key tui.Key, ms *modalState, renderer *tui.Renderer, agent *skawld.Agent, session *skawld.Session, screen *tui.Screen) modalAction {
	switch ms.typ {
	case modalModelPicker:
		return handleModelPickerKey(key, ms, renderer, screen)
	case modalSessionBrowser:
		return handleSessionBrowserKey(key, ms, renderer, screen, agent)
	case modalSettings:
		return handleSettingsKey(key, ms, renderer, screen)
	case modalMemoryBrowser:
		return handleMemoryBrowserKey(key, ms, renderer, screen)
	case modalExport:
		return handleExportKey(key, ms, renderer, screen, agent, session)
	case modalCost:
		return handleCostKey(key, ms, renderer, screen, agent, session)
	case modalTheme:
		return handleThemeKey(key, ms, renderer, screen)
	case modalCompact:
		return handleCompactKey(key, ms, renderer, screen, agent, session)
	default:
		return modalActionDismiss
	}
}

// initModal sets up modal state for a given modal type.
func initModal(typ ModalType, ms *modalState, renderer *tui.Renderer, agent *skawld.Agent, session *skawld.Session, screen *tui.Screen) {
	ms.typ = typ
	ms.selected = 0
	ms.selectedSection = 0
	ms.selectedField = 0
	ms.query = ""

	switch typ {
	case modalModelPicker:
		ms.modelEntries = tui.AvailableModels
		ms.currentModel = renderer.Views.Status.Model

	case modalSessionBrowser:
		ms.sessions = loadSessions(agent, session)

	case modalSettings:
		ms.settingsSections = tui.BuildSettings(ms.currentModel, "default")

	case modalMemoryBrowser:
		ms.memories = loadMemories()

	case modalExport:
		ms.exportFormat = "md"

	case modalTheme:
		ms.themeList = tui.AvailableThemes
		ms.currentTheme = "default"
	}
}

// executeModalAction executes the confirmed action of a modal.
func executeModalAction(typ ModalType, ms *modalState, screen *tui.Screen, renderer *tui.Renderer, agent *skawld.Agent, session *skawld.Session) {
	switch typ {
	case modalModelPicker:
		if ms.selected >= 0 && ms.selected < len(ms.modelEntries) {
			renderer.Views.Status.SetModel(ms.modelEntries[ms.selected].ID)
		}

	case modalSessionBrowser:
		if ms.selected >= 0 && ms.selected < len(ms.sessions) {
			resumeSession(ms.sessions[ms.selected].ID, screen, renderer, agent, session)
		}

	case modalCompact:
		triggerCompact(screen, renderer, agent, session)
	}
}

// ─── Model Picker ───────────────────────────────────────────────────────────

func handleModelPickerKey(key tui.Key, ms *modalState, renderer *tui.Renderer, screen *tui.Screen) modalAction {
	picker := tui.ModelPicker{Width: screen.Width, Height: screen.Height, Theme: renderer.Theme}

	switch {
	case key.KeyCode == tui.KeyEscape:
		return modalActionDismiss

	case key.KeyCode == tui.KeyEnter:
		return modalActionExecute

	case key.KeyCode == tui.KeyUp:
		if ms.selected > 0 {
			ms.selected--
		}
		renderModelPicker(screen, &picker, ms)
		return modalActionContinue

	case key.KeyCode == tui.KeyDown:
		if ms.selected < len(ms.modelEntries)-1 {
			ms.selected++
		}
		renderModelPicker(screen, &picker, ms)
		return modalActionContinue
	}
	return modalActionContinue
}

func renderModelPicker(screen *tui.Screen, picker *tui.ModelPicker, ms *modalState) {
	buf := tui.NewBuffer(screen.Width, screen.Height)
	picker.RenderModelPicker(buf, ms.currentModel, ms.modelEntries, ms.selected)
	buf.FullRender(screen)
}

// ─── Session Browser ────────────────────────────────────────────────────────

func handleSessionBrowserKey(key tui.Key, ms *modalState, renderer *tui.Renderer, screen *tui.Screen, agent *skawld.Agent) modalAction {
	browser := tui.SessionBrowser{Width: screen.Width, Height: screen.Height, Theme: renderer.Theme}

	switch {
	case key.KeyCode == tui.KeyEscape:
		return modalActionDismiss

	case key.KeyCode == tui.KeyEnter:
		return modalActionExecute

	case key.KeyCode == tui.KeyUp:
		if ms.selected > 0 {
			ms.selected--
		}
		renderSessionBrowser(screen, &browser, ms)
		return modalActionContinue

	case key.KeyCode == tui.KeyDown:
		if ms.selected < len(ms.sessions)-1 {
			ms.selected++
		}
		renderSessionBrowser(screen, &browser, ms)
		return modalActionContinue

	case key.Rune == 'd' || key.Rune == 'D':
		// Delete session
		if ms.selected >= 0 && ms.selected < len(ms.sessions) {
			deleteSession(ms.sessions[ms.selected].ID, agent)
			ms.sessions = loadSessions(agent, nil)
			if ms.selected >= len(ms.sessions) {
				ms.selected = len(ms.sessions) - 1
			}
			if ms.selected < 0 {
				ms.selected = 0
			}
		}
		renderSessionBrowser(screen, &browser, ms)
		return modalActionContinue

	case key.Rune == 'n' || key.Rune == 'N':
		// New session — just dismiss
		return modalActionDismiss
	}
	return modalActionContinue
}

func renderSessionBrowser(screen *tui.Screen, browser *tui.SessionBrowser, ms *modalState) {
	buf := tui.NewBuffer(screen.Width, screen.Height)
	browser.RenderSessions(buf, ms.sessions, ms.selected)
	buf.FullRender(screen)
}

func loadSessions(agent *skawld.Agent, current *skawld.Session) []tui.SessionInfo {
	ctx := context.Background()
	records, err := agent.Store().List(ctx, 50, 0)
	if err != nil {
		return nil
	}

	var sessions []tui.SessionInfo
	currentID := ""
	if current != nil {
		currentID = current.ID
	}

	for _, rec := range records {
		topic := ""
		if rec.Meta != nil {
			if t, ok := rec.Meta["topic"].(string); ok {
				topic = t
			}
		}

		name := rec.ID
		if rec.Meta != nil {
			if n, ok := rec.Meta["name"].(string); ok {
				name = n
			}
		}
		if len(name) > 8 {
			name = name[:8]
		}

		sessions = append(sessions, tui.SessionInfo{
			ID:       rec.ID,
			Name:     name,
			Topic:    topic,
			Active:   rec.ID == currentID,
			TimeAgo:  formatTimeAgo(rec.UpdatedAt),
		})
	}
	return sessions
}

func deleteSession(id string, agent *skawld.Agent) {
	ctx := context.Background()
	_ = agent.Store().Delete(ctx, id)
}

func resumeSession(id string, screen *tui.Screen, renderer *tui.Renderer, agent *skawld.Agent, session *skawld.Session) {
	// For now, just show status
	renderer.Views.Status.SessionID = id
	screen.WriteAt(screen.Height-1, 1, tui.ClearLine())
	screen.WriteAt(screen.Height-1, 1, renderer.Theme.AccentText(fmt.Sprintf("Resumed session %s", id[:8])))
	time.Sleep(500 * time.Millisecond)
}

// ─── Settings Page ──────────────────────────────────────────────────────────

func handleSettingsKey(key tui.Key, ms *modalState, renderer *tui.Renderer, screen *tui.Screen) modalAction {
	sp := tui.SettingsPage{Width: screen.Width, Height: screen.Height, Theme: renderer.Theme}

	switch {
	case key.KeyCode == tui.KeyEscape:
		return modalActionDismiss

	case key.KeyCode == tui.KeyUp:
		if ms.selectedField > 0 {
			ms.selectedField--
		}
		renderSettings(screen, &sp, ms)
		return modalActionContinue

	case key.KeyCode == tui.KeyDown:
		totalFields := countFields(ms.settingsSections)
		if ms.selectedField < totalFields-1 {
			ms.selectedField++
		}
		renderSettings(screen, &sp, ms)
		return modalActionContinue
	}
	return modalActionContinue
}

func renderSettings(screen *tui.Screen, sp *tui.SettingsPage, ms *modalState) {
	buf := tui.NewBuffer(screen.Width, screen.Height)
	sp.RenderSettings(buf, ms.settingsSections, ms.selectedSection, ms.selectedField)
	buf.FullRender(screen)
}

func countFields(sections []tui.SettingsSection) int {
	n := 0
	for _, s := range sections {
		n += len(s.Fields)
	}
	return n
}

// ─── Memory Browser ─────────────────────────────────────────────────────────

func handleMemoryBrowserKey(key tui.Key, ms *modalState, renderer *tui.Renderer, screen *tui.Screen) modalAction {
	mb := tui.MemoryBrowser{Width: screen.Width, Height: screen.Height, Theme: renderer.Theme}

	switch {
	case key.KeyCode == tui.KeyEscape:
		return modalActionDismiss

	case key.KeyCode == tui.KeyUp:
		if ms.selected > 0 {
			ms.selected--
		}
		renderMemories(screen, &mb, ms)
		return modalActionContinue

	case key.KeyCode == tui.KeyDown:
		if ms.selected < len(ms.memories) {
			ms.selected++
		}
		renderMemories(screen, &mb, ms)
		return modalActionContinue
	}
	return modalActionContinue
}

func renderMemories(screen *tui.Screen, mb *tui.MemoryBrowser, ms *modalState) {
	buf := tui.NewBuffer(screen.Width, screen.Height)
	mb.RenderMemories(buf, ms.memories, ms.selected)
	buf.FullRender(screen)
}

func loadMemories() []tui.MemoryEntry {
	// TODO: implement actual memory loading from disk
	return nil
}

// ─── Export Dialog ──────────────────────────────────────────────────────────

func handleExportKey(key tui.Key, ms *modalState, renderer *tui.Renderer, screen *tui.Screen, agent *skawld.Agent, session *skawld.Session) modalAction {
	ed := tui.ExportDialog{Width: screen.Width, Height: screen.Height, Theme: renderer.Theme}

	switch {
	case key.KeyCode == tui.KeyEscape:
		return modalActionDismiss

	case key.KeyCode == tui.KeyEnter:
		if !ms.exporting {
			// Start export
			ms.exporting = true
			ms.exportProgress = 50
			ms.exportPath = fmt.Sprintf("raven-export-%s.%s", session.ID[:8], ms.exportFormat)
			renderExportDialog(screen, &ed, ms)
			return modalActionContinue
		}
		return modalActionDismiss

	case key.Rune == 'm' || key.Rune == 'M':
		ms.exportFormat = "md"
		renderExportDialog(screen, &ed, ms)
		return modalActionContinue

	case key.Rune == 'j' || key.Rune == 'J':
		ms.exportFormat = "json"
		renderExportDialog(screen, &ed, ms)
		return modalActionContinue

	case key.Rune == 't' || key.Rune == 'T':
		ms.exportFormat = "txt"
		renderExportDialog(screen, &ed, ms)
		return modalActionContinue
	}
	return modalActionContinue
}

func renderExportDialog(screen *tui.Screen, ed *tui.ExportDialog, ms *modalState) {
	buf := tui.NewBuffer(screen.Width, screen.Height)
	ed.RenderExport(buf, ms.exportFormat, ms.exporting, ms.exportProgress, ms.exportPath)
	buf.FullRender(screen)
}

// ─── Cost Breakdown ─────────────────────────────────────────────────────────

func handleCostKey(key tui.Key, ms *modalState, renderer *tui.Renderer, screen *tui.Screen, agent *skawld.Agent, session *skawld.Session) modalAction {
	switch {
	case key.KeyCode == tui.KeyEscape || key.KeyCode == tui.KeyEnter:
		return modalActionDismiss
	}
	return modalActionContinue
}

func renderCostDialog(screen *tui.Screen, cd *tui.CostDialog, ms *modalState) {
	buf := tui.NewBuffer(screen.Width, screen.Height)
	cd.RenderCost(buf, ms.costData)
	buf.FullRender(screen)
}

// ─── Theme Switcher ─────────────────────────────────────────────────────────

func handleThemeKey(key tui.Key, ms *modalState, renderer *tui.Renderer, screen *tui.Screen) modalAction {
	ts := tui.ThemeSwitcher{Width: screen.Width, Height: screen.Height, Theme: renderer.Theme}

	switch {
	case key.KeyCode == tui.KeyEscape:
		return modalActionDismiss

	case key.KeyCode == tui.KeyEnter:
		if ms.selected >= 0 && ms.selected < len(ms.themeList) {
			// Theme switching — currently no SetTheme method on Screen.
			// The user can see the selection, full theme switch is a future enhancement.
			ms.currentTheme = ms.themeList[ms.selected].ID
		}
		return modalActionExecute

	case key.KeyCode == tui.KeyUp:
		if ms.selected > 0 {
			ms.selected--
		}
		renderThemePicker(screen, &ts, ms)
		return modalActionContinue

	case key.KeyCode == tui.KeyDown:
		if ms.selected < len(ms.themeList)-1 {
			ms.selected++
		}
		renderThemePicker(screen, &ts, ms)
		return modalActionContinue
	}
	return modalActionContinue
}

func renderThemePicker(screen *tui.Screen, ts *tui.ThemeSwitcher, ms *modalState) {
	buf := tui.NewBuffer(screen.Width, screen.Height)
	ts.RenderTheme(buf, ms.currentTheme, ms.themeList, ms.selected)
	buf.FullRender(screen)
}

// ─── Compact ────────────────────────────────────────────────────────────────

func handleCompactKey(key tui.Key, ms *modalState, renderer *tui.Renderer, screen *tui.Screen, agent *skawld.Agent, session *skawld.Session) modalAction {
	switch {
	case key.KeyCode == tui.KeyEscape || key.Rune == 'n' || key.Rune == 'N':
		return modalActionDismiss
	case key.KeyCode == tui.KeyEnter || key.Rune == 'y' || key.Rune == 'Y':
		return modalActionExecute
	}
	return modalActionContinue
}

func renderCompactDialog(screen *tui.Screen, renderer *tui.Renderer) {
	buf := tui.NewBuffer(screen.Width, screen.Height)
	width := screen.Width - 8
	if width > 60 {
		width = 60
	}
	if width < 36 {
		width = 36
	}
	leftPad := (screen.Width - width) / 2
	topPad := screen.Height/2 - 3

	title := " Compact Context "
	buf.SetRow(topPad, tui.PadTo(leftPad)+renderer.Theme.DimText(tui.BoxTL+title+tui.Repeat(tui.BoxH, width-len(title)-1)+tui.BoxTR))
	buf.SetRow(topPad+1, tui.PadTo(leftPad)+renderer.Theme.DimText(tui.BoxV+" ")+"Compact context? This will summarize earlier messages to free space."+tui.PadTo(width-len("Compact context? This will summarize earlier messages to free space.")-2)+renderer.Theme.DimText(tui.BoxV))
	buf.SetRow(topPad+2, tui.PadTo(leftPad)+renderer.Theme.DimText(tui.BoxV+" ")+renderer.Theme.Bold("[Y] Confirm  [N] Cancel")+tui.PadTo(width-len("[Y] Confirm  [N] Cancel")-3)+renderer.Theme.DimText(tui.BoxV))
	buf.SetRow(topPad+3, tui.PadTo(leftPad)+renderer.Theme.DimText(tui.BoxBL+tui.Repeat(tui.BoxH, width-1)+tui.BoxBR))
	buf.FullRender(screen)
}

func triggerCompact(screen *tui.Screen, renderer *tui.Renderer, agent *skawld.Agent, session *skawld.Session) {
	// Show feedback — actual compaction is triggered by SDK
	screen.WriteAt(screen.Height-1, 1, tui.ClearLine())
	screen.WriteAt(screen.Height-1, 1, renderer.Theme.AccentText("Compacting context..."))
	time.Sleep(500 * time.Millisecond)
}

// ─── Time Formatting ────────────────────────────────────────────────────────

func formatTimeAgo(timestamp string) string {
	t, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return "unknown"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1m ago"
		}
		return fmt.Sprintf("%dm ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1h ago"
		}
		return fmt.Sprintf("%dh ago", h)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1d ago"
		}
		return fmt.Sprintf("%dd ago", days)
	}
}