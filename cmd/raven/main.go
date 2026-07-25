// Raven CLI — a premium AI coding assistant terminal UI.
//
// Usage:
//
//	raven                          # Interactive REPL with welcome screen
//	raven --prompt "Fix the bug"   # Single-shot non-interactive run
//	raven --session <id>           # Resume a specific session
//	raven --config path/to.json    # Use custom config file
//
// Raven consumes the skawld SDK event stream and renders it with a
// thoughtfully designed terminal UX. It supports streaming text,
// interactive permission prompts, tool execution display, and
// session management.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go"
	"github.com/ZekromNguyen/skawld-sdk-go/cmd/raven/internal/tui"
	"github.com/ZekromNguyen/skawld-sdk-go/config"
	"github.com/ZekromNguyen/skawld-sdk-go/providers"
	"github.com/ZekromNguyen/skawld-sdk-go/tools"
)

var (
	flagPrompt  = flag.String("prompt", "", "Single-shot prompt (non-interactive)")
	flagSession = flag.String("session", "", "Resume a specific session ID")
	flagConfig  = flag.String("config", "", "Path to config JSON file")
	flagModel   = flag.String("model", "", "Override model (e.g., claude-sonnet-4-6)")
	flagCWD     = flag.String("cwd", "", "Override working directory")
	flagHelp    = flag.Bool("help", false, "Show help")
)

// REPL mode constants.
const (
	modeInput      = iota // normal text input
	modePalette           // command palette overlay
	modePermission        // waiting for permission decision
	modeModal             // interactive modal overlay
)

// ModalType identifies which interactive modal is active.
type ModalType int

const (
	modalNone ModalType = iota
	modalModelPicker
	modalSessionBrowser
	modalSettings
	modalMemoryBrowser
	modalExport
	modalCost
	modalTheme
	modalCompact
)

// modalState holds the transient state of an interactive modal.
type modalState struct {
	typ              ModalType
	selected         int
	selectedSection  int
	selectedField    int
	query            string
	exportFormat     string
	exporting        bool
	exportProgress   int
	exportPath       string
	compactConfirmed bool
	themeList        []tui.AvailableTheme
	currentTheme     string
	modelEntries     []tui.ModelInfo
	currentModel     string
	sessions         []tui.SessionInfo
	memories         []tui.MemoryEntry
	settingsSections []tui.SettingsSection
	costData         tui.CostData
}

// modalAction is returned by handleModalKey to signal what to do next.
type modalAction int

const (
	modalActionContinue modalAction = iota // modal still active
	modalActionDismiss                     // dismiss modal, return to input
	modalActionExecute                     // execute the modal's action
)

func main() {
	flag.Parse()

	if *flagHelp {
		printHelp()
		return
	}

	// ── Agent Construction ──────────────────────────────────────

	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	opts, err := buildAgentOptions(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent: %v\n", err)
		os.Exit(1)
	}

	agent, err := skawld.NewAgent(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent: %v\n", err)
		os.Exit(1)
	}
	defer agent.Close()

	// ── Terminal Setup ──────────────────────────────────────────

	screen, err := tui.NewScreen()
	if err != nil {
		fmt.Fprintf(os.Stderr, "terminal: %v\n", err)
		os.Exit(1)
	}
	defer screen.Reset()

	renderer := tui.NewRenderer(screen)

	// ── Single-Shot Mode ────────────────────────────────────────

	if *flagPrompt != "" {
		runSingleShot(agent, screen, renderer, *flagPrompt)
		return
	}

	// ── Interactive REPL ────────────────────────────────────────

	if err := screen.EnterRawMode(); err != nil {
		fmt.Fprintf(os.Stderr, "raw mode: %v\n", err)
		os.Exit(1)
	}
	screen.EnterAltScreen()

	sigCh := screen.HandleSignals()

	// Show welcome
	renderer.ShowWelcome()
	time.Sleep(1500 * time.Millisecond) // Brief splash display

	// Clear and prepare for chat
	renderer.ClearAndReset()
	renderer.Views.Status.SetModel(string(opts.Model))
	renderer.Views.Status.SetMode("chat")

	// Create session
	sessOpts := skawld.SessionOptions{}
	if *flagSession != "" {
		sessOpts.ID = *flagSession
	}
	session, err := agent.Session(context.Background(), sessOpts)
	if err != nil {
		screen.Reset()
		fmt.Fprintf(os.Stderr, "session: %v\n", err)
		os.Exit(1)
	}

	renderer.Views.Status.SessionID = session.ID
	renderer.PrintStatusBar()

	// Build palette entries once
	paletteEntries := buildPaletteEntries()

	// ── Input Loop ──────────────────────────────────────────────

	inputReader := tui.NewInputReader()
	replMode := modeInput
	editor := tui.NewLineEditor("> ")
	editor.History = loadHistory()

	// Palette state
	var paletteQuery string
	var paletteFiltered []tui.PaletteEntry
	paletteSelected := 0
	cmdPalette := tui.CommandPalette{Width: screen.Width, Height: screen.Height, Theme: renderer.Theme}

	// Permission state
	var pendingPermission *tui.PermissionRequest
	var permDialog tui.PermissionDialog

	// Modal state
	var ms modalState

loop:
	for {
		select {
		case sig := <-sigCh:
			if sig == os.Interrupt {
				break loop
			}
		default:
		}

		// Handle terminal resize events (non-blocking).
		select {
		case <-screen.Resize:
			renderer.Resize()
			renderer.Render()
			renderer.PrintStatusBar()
		default:
		}

		switch replMode {
		case modeInput:
			// Show prompt
			promptRow := screen.Height - 1
			screen.WriteAt(promptRow, 1, tui.ClearToEndOfLine())
			screen.WriteAt(promptRow, 1, renderer.Theme.AccentText("> "))

			// Use LineEditor for readline-style input
			editor.PaletteTriggered = false
			input, err := editor.ReadLine(inputReader, screen)
			if err != nil {
				if err == io.EOF {
					break loop
				}
				continue
			}

			// Check for palette trigger (Ctrl+P)
			if editor.PaletteTriggered {
				replMode = modePalette
				paletteQuery = ""
				paletteFiltered = paletteEntries
				paletteSelected = 0
				renderPalette(screen, &cmdPalette, paletteQuery, paletteFiltered, paletteSelected)
				continue
			}

			if input == "" {
				continue
			}

			// Handle slash commands
			if strings.HasPrefix(input, "/") {
				modalType, openModal := handleCommand(input, screen, renderer, agent, session)
				if openModal {
					initModal(modalType, &ms, renderer, agent, session, screen)
					renderModal(modalType, &ms, renderer, screen)
					replMode = modeModal
					continue
				}
				renderer.PrintStatusBar()
				continue
			}

			// Handle shell escape
			if strings.HasPrefix(input, "!") {
				handleShellEscape(screen, strings.TrimSpace(input[1:]))
				renderer.Render()
				renderer.PrintStatusBar()
				continue
			}

			if input == "exit" || input == "quit" {
				break loop
			}

			// Start a run — wrap in permission-aware handler
			runPromptInteractive(agent, session, input, screen, renderer, &permDialog, inputReader)

		case modePalette:
			key, err := inputReader.ReadKey()
			if err != nil {
				replMode = modeInput
				continue
			}

			switch {
			case key.KeyCode == tui.KeyEscape:
				// Dismiss palette
				replMode = modeInput
				renderer.Render()
				renderer.PrintStatusBar()

			case key.KeyCode == tui.KeyEnter:
				// Execute selected entry
				if paletteSelected >= 0 && paletteSelected < len(paletteFiltered) {
					action := paletteFiltered[paletteSelected].Action
					replMode = modeInput
					renderer.Render()
					renderer.PrintStatusBar()
					modalType, openModal := executePaletteAction(action, screen, renderer, agent, session)
					if openModal {
						initModal(modalType, &ms, renderer, agent, session, screen)
						renderModal(modalType, &ms, renderer, screen)
						replMode = modeModal
					}
				}

			case key.KeyCode == tui.KeyUp:
				if paletteSelected > 0 {
					paletteSelected--
				}
				renderPalette(screen, &cmdPalette, paletteQuery, paletteFiltered, paletteSelected)

			case key.KeyCode == tui.KeyDown:
				if paletteSelected < len(paletteFiltered)-1 {
					paletteSelected++
				}
				renderPalette(screen, &cmdPalette, paletteQuery, paletteFiltered, paletteSelected)

			case key.KeyCode == tui.KeyBackspace:
				if len(paletteQuery) > 0 {
					paletteQuery = paletteQuery[:len(paletteQuery)-1]
				}
				paletteFiltered = filterEntries(paletteEntries, paletteQuery)
				if paletteSelected >= len(paletteFiltered) {
					paletteSelected = len(paletteFiltered) - 1
				}
				if paletteSelected < 0 {
					paletteSelected = 0
				}
				renderPalette(screen, &cmdPalette, paletteQuery, paletteFiltered, paletteSelected)

			case key.Rune > 0 && !key.Ctrl:
				paletteQuery += string(key.Rune)
				paletteFiltered = filterEntries(paletteEntries, paletteQuery)
				paletteSelected = 0
				renderPalette(screen, &cmdPalette, paletteQuery, paletteFiltered, paletteSelected)
			}

		case modePermission:
			// Wait for a valid permission key
			rawKey := readKeyString(inputReader)
			choice, ok := tui.ParsePermissionChoice(rawKey)
			if ok {
				replMode = modeInput
				renderer.Render()
				renderer.PrintStatusBar()

				if pendingPermission != nil {
					handlePermissionChoice(choice, pendingPermission, renderer)
				}
				pendingPermission = nil
			}

		case modeModal:
			key, err := inputReader.ReadKey()
			if err != nil {
				replMode = modeInput
				continue
			}
			action := handleModalKey(key, &ms, renderer, agent, session, screen)
			switch action {
			case modalActionDismiss:
				replMode = modeInput
				renderer.Render()
				renderer.PrintStatusBar()
			case modalActionExecute:
				replMode = modeInput
				executeModalAction(ms.typ, &ms, screen, renderer, agent, session)
				renderer.Render()
				renderer.PrintStatusBar()
			case modalActionContinue:
			}
		}
	}

	// Clean exit
	screen.Reset()
	fmt.Println("Raven out. ◤")
}

// ─── Palette Helpers ──────────────────────────────────────────────────────

func buildPaletteEntries() []tui.PaletteEntry {
	return tui.BuiltinCommands
}

func filterEntries(entries []tui.PaletteEntry, query string) []tui.PaletteEntry {
	if query == "" {
		return entries
	}
	lower := strings.ToLower(query)
	var filtered []tui.PaletteEntry
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e.Label), lower) ||
			strings.Contains(strings.ToLower(e.Subtitle), lower) {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

func renderPalette(screen *tui.Screen, cp *tui.CommandPalette, query string, entries []tui.PaletteEntry, selected int) {
	buf := tui.NewBuffer(cp.Width, cp.Height)
	cp.Render(buf, query, entries, selected)
	buf.FullRender(screen)
}

func executePaletteAction(action string, screen *tui.Screen, renderer *tui.Renderer, agent *skawld.Agent, session *skawld.Session) (ModalType, bool) {
	switch action {
	case "/clear":
		renderer.ClearAndReset()
	case "/status":
		showStatus(screen, renderer, session)
	case "/help":
		showHelp(screen, renderer)
	case "/model":
		return modalModelPicker, true
	case "/sessions":
		return modalSessionBrowser, true
	case "/memory":
		return modalMemoryBrowser, true
	case "/settings":
		return modalSettings, true
	case "/export md", "/export json":
		return modalExport, true
	case "/cost":
		return modalCost, true
	case "/compact":
		return modalCompact, true
	case "/quit", "/exit":
		screen.Reset()
		os.Exit(0)
	default:
		screen.WriteAt(screen.Height-1, 1, tui.ClearToEndOfLine())
		screen.WriteAt(screen.Height-1, 1, renderer.Theme.DimText(action))
		time.Sleep(500 * time.Millisecond)
	}
	return modalNone, false
}

func readKeyString(reader *tui.InputReader) string {
	key, err := reader.ReadKey()
	if err != nil {
		return ""
	}
	return key.KeyEvent()
}

// ─── Permission Handling ──────────────────────────────────────────────────

// runPromptInteractive runs a prompt with inline permission dialog support.
// When the SDK requests permissions mid-stream, it pauses to show the dialog.
func runPromptInteractive(agent *skawld.Agent, session *skawld.Session, prompt string, screen *tui.Screen, renderer *tui.Renderer, permDialog *tui.PermissionDialog, inputReader *tui.InputReader) {
	ctx := context.Background()
	handle := session.StartRun(ctx, prompt, skawld.RunOptions{})
	defer handle.Close()

	for ev := range handle.Events() {
		// Check if this is a permission request — intercept for interactive dialog
		if ev.Type == skawld.EventPermissionRequest && len(ev.Requests) > 0 {
			req := ev.Requests[0]
			permReq := &tui.PermissionRequest{
				ToolName: req.ToolName,
				Summary:  req.Summary,
				FilePath: extractFilePath(req.ToolName, req.Input),
				Command:  extractCommand(req.ToolName, req.Input),
			}

			// Try to compute a diff preview for Edit/Write
			if req.ToolName == "Edit" {
				if oldStr, ok := req.Input["old_string"].(string); ok {
					newStr, _ := req.Input["new_string"].(string)
					permReq.Edits = tui.ComputeSimpleDiff(oldStr, newStr)
				}
			} else if req.ToolName == "Write" {
				if content, ok := req.Input["content"].(string); ok {
					permReq.Edits = contentPreview(content)
				}
			}

			// Render permission dialog
			permDialog.Width = screen.Width
			permDialog.Height = screen.Height
			permDialog.Theme = renderer.Theme

			buf := tui.NewBuffer(screen.Width, screen.Height)
			permDialog.RenderPermission(buf, *permReq)
			buf.FullRender(screen)

			// Wait for user decision
			for {
				rawKey := readKeyString(inputReader)
				choice, ok := tui.ParsePermissionChoice(rawKey)
				if !ok {
					continue
				}
				switch choice {
				case tui.PermAllowOnce:
					// Allow this one — send event back through renderer and continue
					renderer.HandleEvent(ev)
					goto donePermission
				case tui.PermAllowAll:
					// Allow all: render once, then let subsequent pass through
					renderer.HandleEvent(ev)
					goto donePermission
				case tui.PermDeny:
					// Skip this event, don't render
					goto donePermission
				case tui.PermShowDiff:
					// Show diff and re-prompt
					if permReq.Edits == "" {
						oldStr, _ := req.Input["old_string"].(string)
						newStr, _ := req.Input["new_string"].(string)
						if req.ToolName == "Edit" {
							permReq.Edits = tui.ComputeSimpleDiff(oldStr, newStr)
						} else if req.ToolName == "Write" {
							if content, ok := req.Input["content"].(string); ok {
								permReq.Edits = contentPreview(content)
							}
						} else if req.ToolName == "Bash" {
							if cmd, ok := req.Input["command"].(string); ok {
								permReq.Edits = "Command: " + cmd
							}
						}
					}
					buf := tui.NewBuffer(screen.Width, screen.Height)
					permDialog.RenderPermission(buf, *permReq)
					buf.FullRender(screen)
					// Loop back to wait for decision
				}
			}
		donePermission:
			continue
		}
		renderer.HandleEvent(ev)
	}
}

func handlePermissionChoice(choice tui.PermissionChoice, req *tui.PermissionRequest, renderer *tui.Renderer) {
	// Permission choices are handled inline during the run loop.
	// This function handles the modePermission case if needed.
	_ = choice
	_ = req
}

func extractFilePath(toolName string, input map[string]interface{}) string {
	if fp, ok := input["file_path"].(string); ok {
		return fp
	}
	return ""
}

func extractCommand(toolName string, input map[string]interface{}) string {
	if cmd, ok := input["command"].(string); ok {
		return cmd
	}
	return ""
}

func contentPreview(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) <= 6 {
		return content
	}
	return strings.Join(lines[:6], "\n") + "\n  ..."
}

func loadHistory() []string {
	// For now, start with empty history. In the future, load from disk.
	return nil
}

// ── Run Prompt (non-interactive fallback) ─────────────────────────────────

func runPrompt(agent *skawld.Agent, session *skawld.Session, prompt string, screen *tui.Screen, renderer *tui.Renderer) {
	ctx := context.Background()
	handle := session.StartRun(ctx, prompt, skawld.RunOptions{})
	defer handle.Close()

	for ev := range handle.Events() {
		renderer.HandleEvent(ev)
	}
}

// ── Slash Commands ────────────────────────────────────────────────────────

func handleCommand(cmd string, screen *tui.Screen, renderer *tui.Renderer, agent *skawld.Agent, session *skawld.Session) (openModalType ModalType, shouldOpenModal bool) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return modalNone, false
	}

	switch parts[0] {
	case "/help":
		showHelp(screen, renderer)
	case "/model":
		if len(parts) > 1 {
			renderer.Views.Status.SetModel(parts[1])
			renderer.PrintStatusBar()
		} else {
			return modalModelPicker, true
		}
	case "/clear":
		renderer.ClearAndReset()
	case "/status":
		showStatus(screen, renderer, session)
	case "/sessions":
		return modalSessionBrowser, true
	case "/memory":
		return modalMemoryBrowser, true
	case "/settings":
		return modalSettings, true
	case "/export":
		return modalExport, true
	case "/cost":
		return modalCost, true
	case "/theme":
		return modalTheme, true
	case "/compact":
		return modalCompact, true
	case "/quit", "/exit":
		screen.Reset()
		os.Exit(0)
	default:
		msg := fmt.Sprintf("Unknown command: %s. Try /help", cmd)
		screen.WriteAt(screen.Height-1, 1, tui.ClearLine())
		screen.WriteAt(screen.Height-1, 1, renderer.Theme.DimText(msg))
		time.Sleep(1500 * time.Millisecond)
	}
	return modalNone, false
}

func handleShellEscape(screen *tui.Screen, command string) {
	if strings.TrimSpace(command) == "" {
		return
	}
	if screen != nil {
		screen.ExitAltScreen()
		_ = screen.ExitRawMode()
	}
	fmt.Printf("!%s\n", command)
	name := "sh"
	args := []string{"-c", command}
	if runtime.GOOS == "windows" {
		name = "cmd.exe"
		args = []string{"/c", command}
	}
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "shell escape failed: %v\n", err)
	}
	fmt.Fprint(os.Stdout, "\nPress Enter to return to Raven...")
	_, _ = fmt.Fscanln(os.Stdin)
	if screen != nil {
		_ = screen.EnterRawMode()
		screen.EnterAltScreen()
		screen.Clear()
	}
}

func showHelp(screen *tui.Screen, renderer *tui.Renderer) {
	renderer.ClearAndReset()
	lines := []string{
		renderer.Theme.Bold("  Raven Commands:"),
		"",
		renderer.Theme.AccentText("  /help") + "        Show this help",
		renderer.Theme.AccentText("  /model") + "        Switch model (interactive picker)",
		renderer.Theme.AccentText("  /sessions") + "     Browse and resume sessions",
		renderer.Theme.AccentText("  /memory") + "       Browse memories",
		renderer.Theme.AccentText("  /settings") + "     Open settings",
		renderer.Theme.AccentText("  /clear") + "        Clear screen",
		renderer.Theme.AccentText("  /compact") + "      Compact context to free space",
		renderer.Theme.AccentText("  /export") + "       Export conversation (md/json/txt)",
		renderer.Theme.AccentText("  /cost") + "         Show cost breakdown",
		renderer.Theme.AccentText("  /theme") + "        Switch terminal theme",
		renderer.Theme.AccentText("  /status") + "       Show session status",
		renderer.Theme.AccentText("  /quit, /exit") + "  Exit Raven",
		"",
		renderer.Theme.AccentText("  Ctrl+P") + "       Command palette",
		renderer.Theme.AccentText("  Ctrl+C") + "       Cancel / interrupt",
		renderer.Theme.AccentText("  Ctrl+D") + "       Exit Raven",
		"",
		renderer.Theme.DimText("  Enter to continue..."),
	}

	buf := renderer.Buffer
	buf.Reset()
	for i, line := range lines {
		buf.SetRow(2+i, line)
	}
	buf.FullRender(screen)

	reader := tui.NewInputReader()
	for {
		key, err := reader.ReadKey()
		if err != nil {
			break
		}
		if key.KeyCode == tui.KeyEnter || key.KeyCode == tui.KeyEscape {
			break
		}
	}
	renderer.ClearAndReset()
}

func showStatus(screen *tui.Screen, renderer *tui.Renderer, session *skawld.Session) {
	renderer.ClearAndReset()
	buf := renderer.Buffer
	lines := []string{
		renderer.Theme.Bold("  Session Status:"),
		"",
		fmt.Sprintf("  Session ID:  %s", renderer.Theme.AccentText(session.ID)),
		fmt.Sprintf("  Messages:    %d", session.MessageCount()),
		fmt.Sprintf("  Created:     %s", session.CreatedAt.Format("2006-01-02 15:04:05")),
		"",
		renderer.Theme.DimText("  Enter to continue..."),
	}
	for i, line := range lines {
		buf.SetRow(2+i, line)
	}
	buf.FullRender(screen)

	reader := tui.NewInputReader()
	for {
		key, err := reader.ReadKey()
		if err != nil {
			break
		}
		if key.KeyCode == tui.KeyEnter || key.KeyCode == tui.KeyEscape {
			break
		}
	}
	renderer.ClearAndReset()
}

// ── Single-Shot Mode ──────────────────────────────────────────────────────

func runSingleShot(agent *skawld.Agent, screen *tui.Screen, renderer *tui.Renderer, prompt string) {
	session, err := agent.Session(context.Background(), skawld.SessionOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "session: %v\n", err)
		os.Exit(1)
	}

	renderer.Views.Status.SetModel(string(agent.Options().Model))
	renderer.Views.Status.SetMode("chat")

	ctx := context.Background()
	handle := session.StartRun(ctx, prompt, skawld.RunOptions{})
	defer handle.Close()

	for ev := range handle.Events() {
		switch ev.Type {
		case skawld.EventAssistant:
			for _, block := range ev.Message.Content {
				if block.Type == skawld.BlockText && block.Text != "" {
					fmt.Print(block.Text)
				}
			}
		case skawld.EventResult:
			fmt.Println()
			if ev.TotalUsage.InputTokens > 0 || ev.TotalUsage.OutputTokens > 0 {
				fmt.Printf("─ %s tokens · %s\n",
					tui.TokenFormat(ev.TotalUsage.InputTokens+ev.TotalUsage.OutputTokens),
					tui.DurationFormat(ev.DurationMS))
			}
		}
	}
}

// ── Config / Agent Setup ──────────────────────────────────────────────────

func loadConfig() (config.File, error) {
	cfgPath := *flagConfig
	if cfgPath == "" {
		if _, err := os.Stat("skawld.json"); err == nil {
			cfgPath = "skawld.json"
		}
	}

	var cfg config.File
	if cfgPath != "" {
		var err error
		cfg, _, err = config.Load(config.LoadOptions{Path: cfgPath})
		if err != nil {
			return cfg, err
		}
	}

	return cfg, nil
}

func buildAgentOptions(cfg config.File) (skawld.AgentOptions, error) {
	opts := skawld.AgentOptions{
		Tools:                  tools.DefaultTools(),
		IncludePartialMessages: true,
	}

	cwd := *flagCWD
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	opts.CWD = cwd

	providerID := cfg.Provider
	if providerID == "" {
		if os.Getenv("ANTHROPIC_API_KEY") != "" {
			providerID = "anthropic"
		} else if os.Getenv("OPENAI_API_KEY") != "" {
			providerID = "openai-responses"
		}
	}

	switch providerID {
	case "anthropic":
		opts.Provider = providers.NewAnthropicProvider(providers.AnthropicOptions{
			APIKey: firstNonEmpty(os.Getenv("ANTHROPIC_API_KEY"), cfg.Anthropic.APIKey),
		})
	case "openai-responses":
		opts.Provider = providers.NewOpenAIResponsesProvider(providers.OpenAIOptions{
			APIKey: firstNonEmpty(os.Getenv("OPENAI_API_KEY"), cfg.OpenAI.APIKey),
		})
	case "openai-chat":
		opts.Provider = providers.NewOpenAIChatCompletionsProvider(providers.OpenAIOptions{
			APIKey: firstNonEmpty(os.Getenv("OPENAI_API_KEY"), cfg.OpenAI.APIKey),
		})
	default:
		if os.Getenv("ANTHROPIC_API_KEY") != "" {
			opts.Provider = providers.NewAnthropicProvider(providers.AnthropicOptions{
				APIKey: os.Getenv("ANTHROPIC_API_KEY"),
			})
		} else if os.Getenv("OPENAI_API_KEY") != "" {
			opts.Provider = providers.NewOpenAIResponsesProvider(providers.OpenAIOptions{
				APIKey: os.Getenv("OPENAI_API_KEY"),
			})
		}
	}

	if opts.Provider == nil {
		return opts, fmt.Errorf("no provider configured — set ANTHROPIC_API_KEY or OPENAI_API_KEY")
	}

	model := *flagModel
	if model == "" {
		model = string(cfg.Model)
	}
	if model == "" {
		if providerID == "anthropic" {
			model = "claude-sonnet-4-6"
		} else {
			model = "gpt-5"
		}
	}
	opts.Model = skawld.ModelID(model)

	mode := cfg.PermissionMode
	if mode == "" {
		mode = skawld.PermissionModeDefault
	}
	opts.Permissions = skawld.PermissionOptions{Mode: skawld.PermissionMode(mode)}

	return opts, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func printHelp() {
	fmt.Println(`Raven — AI coding companion terminal UI.

Usage:
  raven [flags]

Flags:
  --prompt <text>   Single-shot prompt (non-interactive)
  --session <id>    Resume a specific session
  --config <path>   Path to config JSON file (default: skawld.json)
  --model <name>    Override model (e.g., claude-sonnet-4-6)
  --cwd <path>      Override working directory
  --help            Show this help

Examples:
  raven                                          # Interactive REPL
  raven --prompt "Fix the auth bug"              # Single shot
  raven --session abc123 --prompt "Continue"     # Resume session
  raven --model claude-haiku-4-5                  # Specific model

Configuration:
  Set ANTHROPIC_API_KEY or OPENAI_API_KEY environment variable.
  Or create a skawld.json config file.

Keyboard shortcuts (interactive mode):
  Ctrl+C    Cancel current operation
  Ctrl+D    Exit Raven
  Ctrl+P    Command palette
  /help     Show help
  /model    Switch model
  /clear    Clear screen
  /status   Show session status`)
}
