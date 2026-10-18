package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"github.com/getlantern/systray"
	wr "github.com/wailsapp/wails/v2/pkg/runtime"

	"tartarus/engine"
	"tartarus/solver"
)

const windowTitle = "Tartarus-Go"
const solverRollouts = 10000

var gameTitle = regexp.MustCompile(`^Torchlight: Infinite\s*$`)

// Settings persist between runs.
type Settings struct {
	OverlayOn       bool   `json:"overlayOn"`
	OverlayX        int    `json:"overlayX"` // physical pixels; -1 places it by the game window
	OverlayY        int    `json:"overlayY"`
	OverlayWidth    int    `json:"overlayWidth"` // DIPs
	OverlayOpacity  int    `json:"overlayOpacity"`
	HideFromCapture bool   `json:"hideFromCapture"`
	ClickThrough    bool   `json:"clickThrough"`
	Collapsed       bool   `json:"collapsed"`
	CPU             string `json:"cpu"` // most, half, quarter, one
	AutoScan        bool   `json:"autoScan"`
	LogPath         string `json:"logPath"`
	GameFirst       bool   `json:"gameFirst"`
	Mode            string `json:"mode"` // analyzer or overlay
}

func defaultSettings() Settings {
	return Settings{OverlayOn: true, OverlayX: -1, OverlayY: -1, OverlayWidth: 380, OverlayOpacity: 92, HideFromCapture: true, CPU: "game", AutoScan: true, GameFirst: true, Mode: "analyzer"}
}

// GameStatus is what the app knows about the Torchlight: Infinite window.
type GameStatus struct {
	Running    bool `json:"running"`
	Foreground bool `json:"foreground"`
}

type Advice struct {
	Error  string        `json:"error"`
	JobID  int64         `json:"jobId"`
	Label  string        `json:"label"`
	Why    string        `json:"why"`
	Action solver.Action `json:"action"`
}

type SolveEvent struct {
	JobID   int64          `json:"jobId"`
	Summary solver.Summary `json:"summary"`
}

type LiveFrame struct {
	ID   uint64 `json:"id"`
	JPG  string `json:"jpg"`
	Kind string `json:"kind"`
}

type mouseUp struct {
	hwnd uintptr
	x, y int32
}

type App struct {
	ctx context.Context

	mu                     sync.Mutex
	settings               Settings
	cancel                 context.CancelFunc
	jobID                  int64
	hwnd                   uintptr
	mode                   string
	quitting               bool
	normal                 rect
	game                   GameStatus
	gameHWND               uintptr
	placed                 bool
	scanBusy               bool
	frameID                uint64
	lastHash               [32]byte
	captureRequested       bool
	captureReason          string
	restoreAfterManualScan bool
	logStatus              LogStatus
}

func NewApp() *App { return &App{settings: defaultSettings(), mode: "analyzer"} }

func settingsPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "Tartarus", "settings.json")
}

func (a *App) load() {
	if raw, err := os.ReadFile(settingsPath()); err == nil {
		s := a.settings
		if json.Unmarshal(raw, &s) == nil {
			a.settings = s
		}
	}
}

func (a *App) save() {
	path := settingsPath()
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	raw, _ := json.MarshalIndent(a.settings, "", "  ")
	os.WriteFile(path, raw, 0o644)
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.load()
	// Playouts allocate small short-lived objects: trade memory for fewer collections.
	debug.SetGCPercent(400)
	lowerPriority(a.settings.GameFirst)
	mouseUps := make(chan mouseUp, 8)
	go a.watchGame()
	go a.watchLiveScan()
	go a.watchLog()
	go a.watchMouseUps(mouseUps)
	go systray.Run(a.trayReady, func() {})
	go runHotkeys(map[int]hotkeySpec{
		1: {modifiers: modControl | modAlt, key: 'O'},
		2: {modifiers: modControl | modAlt, key: 'A'},
		3: {modifiers: modControl | modAlt, key: 'S'},
		4: {modifiers: modControl | modAlt, key: 'L'},
		5: {modifiers: modControl | modAlt, key: ' '},
	}, a.onHotkey, func(hwnd uintptr, x, y int32) {
		select {
		case mouseUps <- mouseUp{hwnd: hwnd, x: x, y: y}:
		default:
		}
	})
}

func (a *App) trayReady() {
	systray.SetIcon(trayIcon)
	systray.SetTitle("Tartarus")
	systray.SetTooltip("Tartarus - Ctrl+Alt+Space to show or hide")
	showHide := systray.AddMenuItem("Show / Hide", "Toggle the Tartarus window")
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit", "Exit Tartarus")
	go func() {
		for {
			select {
			case <-showHide.ClickedCh:
				a.toggleHidden()
			case <-quit.ClickedCh:
				a.Quit()
			case <-a.ctx.Done():
				systray.Quit()
				return
			}
		}
	}()
}

func (a *App) beforeClose(context.Context) bool {
	a.mu.Lock()
	quitting := a.quitting
	a.mu.Unlock()
	if quitting {
		return false
	}
	a.toggleHidden()
	return true
}

func (a *App) domReady(ctx context.Context) {
	a.hwnd = findWindow(windowTitle)
	a.normal, _ = windowRect(a.hwnd)
}

// RestoreStartupMode applies persisted native window styling after the frontend is ready.
func (a *App) RestoreStartupMode() string {
	a.mu.Lock()
	mode := a.settings.Mode
	a.mu.Unlock()
	if mode != "overlay" {
		mode = "analyzer"
	}
	a.SetMode(mode)
	return mode
}

func (a *App) shutdown(ctx context.Context) {
	a.mu.Lock()
	if a.cancel != nil {
		a.cancel()
	}
	a.mu.Unlock()
	systray.Quit()
	a.save()
}

func (a *App) onHotkey(id int) {
	switch id {
	case 1: // Ctrl+Alt+O: show or hide the overlay
		a.mu.Lock()
		mode := a.mode
		a.mu.Unlock()
		if mode != "overlay" {
			a.SetMode("overlay")
			return
		}
		a.mu.Lock()
		a.settings.OverlayOn = !a.settings.OverlayOn
		on := a.settings.OverlayOn
		a.mu.Unlock()
		if on {
			showWindow(a.hwnd, swShowNoActivate)
		} else {
			showWindow(a.hwnd, swHide)
		}
	case 2: // Ctrl+Alt+A: the analyzer
		a.SetMode("analyzer")
	case 3: // Ctrl+Alt+S: solve again
		wr.EventsEmit(a.ctx, "solve-now")
	case 4: // Ctrl+Alt+L: lock or unlock click-through
		s := a.GetSettings()
		s.ClickThrough = !s.ClickThrough
		a.SaveSettings(s)
		wr.EventsEmit(a.ctx, "settings", a.GetSettings())
	case 5: // Ctrl+Alt+Space: hide or show the app
		a.toggleHidden()
	}
}

func (a *App) toggleHidden() {
	a.mu.Lock()
	hwnd := a.hwnd
	a.mu.Unlock()
	if hwnd == 0 {
		hwnd = findWindow(windowTitle)
		a.mu.Lock()
		a.hwnd = hwnd
		a.mu.Unlock()
	}
	if windowVisible(hwnd) {
		showWindow(hwnd, swHide)
	} else {
		showWindow(hwnd, swShowNoActivate)
	}
}

// ---------- The game window ----------

func (a *App) watchGame() {
	missing := 0
	for {
		g, ok := findGame(gameTitle)
		a.mu.Lock()
		prev := a.game
		if ok {
			missing = 0
			a.game = GameStatus{Running: true, Foreground: g.Foreground}
			a.gameHWND = g.HWND
		} else if missing++; missing >= 3 {
			a.game = GameStatus{}
			a.gameHWND = 0
		}
		cur, mode, placed := a.game, a.mode, a.placed
		a.mu.Unlock()
		if cur != prev {
			wr.EventsEmit(a.ctx, "game", cur)
		}
		if ok && mode == "overlay" && !placed && a.GetSettings().OverlayX < 0 {
			a.placeOverlay(&g)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (a *App) watchMouseUps(events <-chan mouseUp) {
	var timer *time.Timer
	var timerC <-chan time.Time
	var last mouseUp
	for {
		select {
		case <-a.ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case event := <-events:
			a.mu.Lock()
			gameHWND, appHWND := a.gameHWND, a.hwnd
			isOverlay := a.mode == "overlay"
			eligible := a.settings.AutoScan && event.hwnd != 0 && event.hwnd == gameHWND
			a.mu.Unlock()
			if !eligible || !pointInClient(event.hwnd, event.x, event.y) || isOverlay && pointInClient(appHWND, event.x, event.y) {
				continue
			}
			last = event
			if timer == nil {
				timer = time.NewTimer(300 * time.Millisecond)
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(300 * time.Millisecond)
			}
			timerC = timer.C
		case <-timerC:
			timerC = nil
			a.mu.Lock()
			eligible := a.settings.AutoScan && a.gameHWND == last.hwnd
			a.mu.Unlock()
			if eligible && foregroundWindow() == last.hwnd && pointInClient(last.hwnd, last.x, last.y) {
				a.requestCapture("click")
			}
		}
	}
}

// watchLiveScan captures only fresh frames while the game is foreground.
func (a *App) watchLiveScan() {
	for t := time.NewTicker(350 * time.Millisecond); ; <-t.C {
		a.mu.Lock()
		if !a.settings.AutoScan && a.captureRequested && a.captureReason != "manual" {
			a.captureRequested = false
			a.captureReason = ""
		}
		if (!a.settings.AutoScan && !a.captureRequested) || !a.game.Foreground || a.gameHWND == 0 || a.scanBusy {
			a.mu.Unlock()
			continue
		}
		hwnd := a.gameHWND
		force := a.captureRequested
		kind := "foreground"
		if force {
			a.captureRequested = false
			kind = a.captureReason
			a.captureReason = ""
		}
		a.scanBusy = true
		a.mu.Unlock()

		frame, err := captureGameFrame(hwnd)
		if err != nil {
			if force {
				a.requestCapture("log trigger")
			}
			a.finishCapture()
			continue
		}
		var encoded bytes.Buffer
		if err = jpeg.Encode(&encoded, frame, &jpeg.Options{Quality: 78}); err != nil {
			if force {
				a.requestCapture("log trigger")
			}
			a.finishCapture()
			continue
		}
		hash := sha256.Sum256(encoded.Bytes())
		a.mu.Lock()
		if hash == a.lastHash && !force {
			a.scanBusy = false
			a.mu.Unlock()
			continue
		}
		a.lastHash = hash
		a.frameID++
		id := a.frameID
		restore := a.restoreAfterManualScan && kind == "manual"
		restoreHwnd := a.hwnd
		if restore {
			a.restoreAfterManualScan = false
		}
		a.mu.Unlock()
		wr.EventsEmit(a.ctx, "scan-frame", LiveFrame{ID: id, JPG: base64.StdEncoding.EncodeToString(encoded.Bytes()), Kind: kind})
		if restore && foregroundWindow() == hwnd {
			activateWindow(restoreHwnd)
		}
	}
}

func (a *App) requestCapture(reason string) {
	a.mu.Lock()
	a.captureRequested = true
	a.captureReason = reason
	a.mu.Unlock()
}

// ScanNow requests one capture through the normal foreground/backpressure pipeline.
func (a *App) ScanNow() bool {
	a.mu.Lock()
	hwnd := a.gameHWND
	ready := hwnd != 0 && a.game.Running
	needFocus := ready && !a.game.Foreground && a.mode != "overlay"
	a.mu.Unlock()
	if !ready {
		return false
	}
	if needFocus && !activateWindow(hwnd) {
		return false
	}
	a.mu.Lock()
	a.restoreAfterManualScan = needFocus
	a.mu.Unlock()
	a.requestCapture("manual")
	return true
}

func (a *App) finishCapture() {
	a.mu.Lock()
	a.scanBusy = false
	a.mu.Unlock()
}

// ScanComplete releases the single-frame backpressure after the reader worker finishes.
func (a *App) ScanComplete(id uint64) {
	a.mu.Lock()
	if id == a.frameID {
		a.scanBusy = false
	}
	a.mu.Unlock()
}

func (a *App) GameStatus() GameStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.game
}

// ---------- Window modes ----------

func (a *App) overlaySize(h uintptr, heightDIP int) (int, int) {
	s := dpiScale(h)
	return int(float64(a.settings.OverlayWidth) * s), int(float64(heightDIP) * s)
}

// placeOverlay puts the card at the game window's top-right corner.
func (a *App) placeOverlay(g *gameWindow) {
	if a.hwnd == 0 {
		return
	}
	cur, _ := windowRect(a.hwnd)
	h, _ := screenSize()
	x, y := int(g.Rect.Left)+16, int(g.Rect.Bottom)-int(cur.Bottom-cur.Top)-16
	if g.Minimized || y < 0 {
		x, y = 16, h-int(cur.Bottom-cur.Top)-16
	}
	setPos(a.hwnd, hwndTopmost, x, y, 0, 0, swpNoSize|swpNoActivate)
	a.mu.Lock()
	a.placed = true
	a.mu.Unlock()
}

// SetMode switches the one window between the analyzer and the overlay card.
func (a *App) SetMode(mode string) {
	if a.hwnd == 0 {
		a.hwnd = findWindow(windowTitle)
	}
	h := a.hwnd
	if h == 0 {
		return
	}
	a.mu.Lock()
	prev := a.mode
	a.mode = mode
	a.settings.Mode = mode
	s := a.settings
	a.mu.Unlock()
	if mode == "overlay" {
		if prev != "overlay" {
			a.normal, _ = windowRect(h)
		}
		styleOverlay(h, s.HideFromCapture, s.OverlayOpacity)
		setClickThrough(h, s.ClickThrough)
		w, hgt := a.overlaySize(h, 220)
		x, y := s.OverlayX, s.OverlayY
		if x < 0 {
			_, sh := screenSize()
			x, y = 16, sh-hgt-16
		}
		setPos(h, hwndTopmost, x, y, w, hgt, swpNoActivate|swpShowWindow)
		if g, ok := findGame(gameTitle); ok && s.OverlayX < 0 {
			a.placeOverlay(&g)
		}
		if !s.OverlayOn {
			showWindow(h, swHide)
		}
	} else {
		styleNormal(h)
		r := a.normal
		if r.Right-r.Left < 200 {
			sw, sh := screenSize()
			r = rect{Left: int32(sw)/2 - 560, Top: int32(sh)/2 - 380, Right: int32(sw)/2 + 560, Bottom: int32(sh)/2 + 380}
		}
		setPos(h, hwndNoTopmost, int(r.Left), int(r.Top), int(r.Right-r.Left), int(r.Bottom-r.Top), swpShowWindow)
		showWindow(h, swShow)
	}
	wr.EventsEmit(a.ctx, "mode", mode)
	a.save()
}

// OverlayHeight resizes the overlay card to its content (CSS pixels).
func (a *App) OverlayHeight(heightDIP int) {
	a.mu.Lock()
	mode := a.mode
	a.mu.Unlock()
	if mode != "overlay" || a.hwnd == 0 {
		return
	}
	cur, ok := windowRect(a.hwnd)
	if !ok {
		return
	}
	_, h := a.overlaySize(a.hwnd, max(40, min(900, heightDIP)))
	w := int(cur.Right - cur.Left)
	currentHeight := int(cur.Bottom - cur.Top)
	if currentHeight >= h-1 && currentHeight <= h+1 {
		return
	}
	setPos(a.hwnd, hwndTopmost, 0, 0, w, h, swpNoMove|swpNoActivate)
}

// OverlayMoved remembers where the player dragged the card.
func (a *App) OverlayMoved() {
	if r, ok := windowRect(a.hwnd); ok {
		a.mu.Lock()
		a.settings.OverlayX, a.settings.OverlayY = int(r.Left), int(r.Top)
		a.mu.Unlock()
		a.save()
	}
}

func (a *App) GetSettings() Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings
}

func (a *App) SaveSettings(s Settings) {
	a.mu.Lock()
	old := a.settings
	s.Mode = old.Mode
	s.OverlayOpacity = max(30, min(100, s.OverlayOpacity))
	s.OverlayWidth = max(280, min(700, s.OverlayWidth))
	a.settings = s
	mode := a.mode
	a.mu.Unlock()
	a.save()
	lowerPriority(s.GameFirst)
	if mode == "overlay" && a.hwnd != 0 {
		setOpacity(a.hwnd, s.OverlayOpacity)
		setClickThrough(a.hwnd, s.ClickThrough)
		setHideFromCapture(a.hwnd, s.HideFromCapture)
	}
}

func (a *App) workers() int {
	return workersFor(a.GetSettings().CPU, runtime.NumCPU())
}

func workersFor(mode string, n int) int {
	if n < 1 {
		n = 1
	}
	available := n - 1
	if n >= 12 {
		available = n - 2
	}
	if available < 1 {
		available = 1
	}
	switch mode {
	case "most":
		return available
	case "quarter":
		return max(1, available/4)
	case "one":
		return 1
	case "half":
		return max(1, available/2)
	default: // game: favor the game, reserving at least one logical processor
		return 1
	}
}

// Quit closes the app.
func (a *App) Quit() {
	a.mu.Lock()
	a.quitting = true
	a.mu.Unlock()
	wr.Quit(a.ctx)
}

// ---------- Solving ----------

// Solve ranks the moves for a board: the strategy's pick comes back at once,
// and the Monte Carlo check streams "solve" events until it is done.
func (a *App) Solve(b engine.BoardInput) (adv Advice) {
	defer func() {
		if r := recover(); r != nil {
			adv = Advice{Error: fmt.Sprintf("That board could not be read: %v", r)}
		}
	}()
	st, msg := engine.FromBoard(b)
	if st == nil {
		return Advice{Error: msg}
	}
	action, label, why, ok := solver.Instant(st)
	if !ok {
		return Advice{Error: "The run is over."}
	}
	ctx, cancel := context.WithCancel(a.ctx)
	job := solver.Start(ctx, st, solver.Options{Rollouts: solverRollouts, Workers: a.workers(), StopAt: 4})
	a.mu.Lock()
	if a.cancel != nil {
		a.cancel()
	}
	a.cancel = cancel
	a.jobID++
	id := a.jobID
	a.mu.Unlock()
	go a.stream(id, job)
	return Advice{JobID: id, Label: label, Why: why, Action: action}
}

func (a *App) stream(id int64, job *solver.Job) {
	for t := time.NewTicker(120 * time.Millisecond); ; <-t.C {
		a.mu.Lock()
		current := a.jobID == id
		a.mu.Unlock()
		if !current {
			t.Stop()
			job.Stop()
			return
		}
		s := job.Snapshot()
		wr.EventsEmit(a.ctx, "solve", SolveEvent{JobID: id, Summary: s})
		if s.Done {
			t.Stop()
			return
		}
	}
}

// ---------- Static data for the UI ----------

type Named struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Meta struct {
	Tools      []Named  `json:"tools"`
	Events     []Named  `json:"events"`
	Gifts      []Named  `json:"gifts"`
	Tiers      []string `json:"tiers"`
	Beasts     []Named  `json:"beasts"`
	Objectives []Named  `json:"objectives"`
	StartCards []Named  `json:"startCards"`
}

func (a *App) Meta() Meta {
	var m Meta
	for i, id := range engine.ToolIDs {
		m.Tools = append(m.Tools, Named{id, engine.ToolNames[i]})
	}
	for i := 1; i < int(engine.EvCount); i++ {
		m.Events = append(m.Events, Named{engine.EventIDs[i], engine.EventNames[i]})
	}
	for i, id := range engine.GiftIDs {
		m.Gifts = append(m.Gifts, Named{id, engine.GiftNames[i]})
	}
	m.Tiers = engine.TierNames[:]
	for _, n := range engine.BeastNames {
		m.Beasts = append(m.Beasts, Named{n, n})
	}
	m.Objectives = []Named{{"king", "King of the Caged"}, {"walk", "Prison Walk"}, {"consume", "Survival of the Fittest"}, {"incident", "Undercurrents"}, {"doors", "Behind Cell Doors"}, {"other", "Another objective"}}
	m.StartCards = []Named{{"", "None"}, {"cagefight", "Cage Fight"}, {"ironlaw", "Iron Law"}, {"tyranny", "Tyranny"}, {"opulence", "Opulence"}}
	return m
}
