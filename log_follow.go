package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	logPollPeriod = 200 * time.Millisecond
	logMaxRead    = 1024 * 1024
	logMaxLine    = 8192
)

type LogStatus struct {
	Path        string `json:"path"`
	Source      string `json:"source"`
	State       string `json:"state"`
	Message     string `json:"message"`
	Bytes       int64  `json:"bytes"`
	Lines       uint64 `json:"lines"`
	Triggers    uint64 `json:"triggers"`
	LastTrigger string `json:"lastTrigger"`
}

type logRule struct {
	kind    string
	pattern *regexp.Regexp
	settle  time.Duration
	rank    int
}

var defaultLogRules = []logRule{
	{kind: "objective", pattern: regexp.MustCompile(`S16BoardTaskPop Run`), settle: 700 * time.Millisecond, rank: 3},
	{kind: "instruments", pattern: regexp.MustCompile(`S16BoardRelicPop Run`), settle: 700 * time.Millisecond, rank: 2},
	{kind: "instruments", pattern: regexp.MustCompile(`Btn_S16Board_RelicPop_Reroll`), settle: 700 * time.Millisecond, rank: 2},
	{kind: "board", pattern: regexp.MustCompile(`\[S16LastDay\] check cur=\d+ max=[1-9]`), settle: 700 * time.Millisecond, rank: 1},
	{kind: "board", pattern: regexp.MustCompile(`UI_S16_Card_Up`), settle: 500 * time.Millisecond, rank: 1},
	{kind: "board", pattern: regexp.MustCompile(`Btn_S16Board_ActionCell`), settle: 500 * time.Millisecond, rank: 1},
	{kind: "board", pattern: regexp.MustCompile(`UI_S16_Click_Eat`), settle: 600 * time.Millisecond, rank: 1},
	{kind: "run-end", pattern: regexp.MustCompile(`S16GamePlayCheckOut Run|UI_S16_Riot_Interface|Btn_S16GamePlay_CheckOut_AbandonChallenge`), settle: 250 * time.Millisecond, rank: 4},
	{kind: "skip", pattern: regexp.MustCompile(`S16BoardNewDayPop Run`), rank: 0},
	{kind: "treasure", pattern: regexp.MustCompile(`S16BoardColoredPop Run`), settle: 600 * time.Millisecond, rank: 3},
	{kind: "board", pattern: regexp.MustCompile(`S16BoardColoredPop Destory`), settle: 350 * time.Millisecond, rank: 1},
	{kind: "speed", pattern: regexp.MustCompile(`Btn_S16Board_Top_Speed`), rank: 0},
	{kind: "board", pattern: regexp.MustCompile(`UI_S16_Click_Cancel`), settle: 500 * time.Millisecond, rank: 1},
	{kind: "board", pattern: regexp.MustCompile(`Btn_S16Board_EventPop_Confirm`), settle: 500 * time.Millisecond, rank: 1},
}

var detailLogLine = regexp.MustCompile(`\b(TLLua|TLGame):`)

func (a *App) GetLogStatus() LogStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.logStatus
}

func (a *App) SetLogPath(path string) Settings {
	a.mu.Lock()
	if strings.TrimSpace(path) == "" {
		a.settings.LogPath = ""
	} else {
		a.settings.LogPath = filepath.Clean(strings.TrimSpace(path))
	}
	settings := a.settings
	a.mu.Unlock()
	a.save()
	a.publishLogStatus(LogStatus{Path: settings.LogPath, State: "searching", Message: "Checking UE_game.log path."})
	return settings
}

func (a *App) BrowseLog() string {
	a.mu.Lock()
	current := a.settings.LogPath
	a.mu.Unlock()
	options := wr.OpenDialogOptions{Title: "Choose UE_game.log", Filters: []wr.FileFilter{{DisplayName: "Torchlight: Infinite log", Pattern: "*.log"}}}
	if current != "" {
		options.DefaultDirectory = filepath.Dir(current)
		options.DefaultFilename = filepath.Base(current)
	}
	path, err := wr.OpenFileDialog(a.ctx, options)
	if err != nil || path == "" {
		return ""
	}
	a.SetLogPath(path)
	return path
}

func (a *App) publishLogStatus(status LogStatus) {
	a.mu.Lock()
	a.logStatus = status
	a.mu.Unlock()
	wr.EventsEmit(a.ctx, "log-status", status)
}

func (a *App) watchLog() {
	ticker := time.NewTicker(logPollPeriod)
	defer ticker.Stop()
	var currentPath string
	var source string
	var offset int64
	var rest string
	var currentSize int64
	var lastInfo os.FileInfo
	var lines, triggers uint64
	var detailSeen uint64
	var lastTrigger string
	var pendingKind string
	var pendingRank int
	var pendingSettle time.Duration
	var pendingDue time.Time
	needsBaseline := true
	missingWhileWaiting := false
	lastDiscovery := time.Time{}
	var configured string

	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
		}

		a.mu.Lock()
		wanted := a.settings.LogPath
		a.mu.Unlock()
		if wanted != configured {
			configured = wanted
			lastDiscovery = time.Time{}
			currentPath = ""
			offset, rest, currentSize = 0, "", 0
			lastInfo = nil
			pendingKind, pendingRank = "", 0
			pendingSettle, pendingDue = 0, time.Time{}
			needsBaseline, missingWhileWaiting = true, false
		}
		if lastDiscovery.IsZero() || time.Since(lastDiscovery) >= 10*time.Second {
			found, foundBy := findUELog(configured)
			lastDiscovery = time.Now()
			if found != currentPath {
				currentPath, source = found, foundBy
				offset, rest, currentSize = 0, "", 0
				lastInfo = nil
				pendingKind, pendingRank = "", 0
				pendingSettle, pendingDue = 0, time.Time{}
				needsBaseline, missingWhileWaiting = true, false
				lines, triggers, detailSeen = 0, 0, 0
				lastTrigger = ""
			}
			if found == "" {
				a.publishLogStatus(LogStatus{State: "not-found", Message: "UE_game.log not found. Set a path or install Torchlight: Infinite through Steam."})
				continue
			}
		}

		info, err := os.Stat(currentPath)
		if err != nil || !info.Mode().IsRegular() {
			if os.IsNotExist(err) {
				missingWhileWaiting = true
			}
			state, message := "waiting", "Waiting for the game to create UE_game.log."
			if !os.IsNotExist(err) && err != nil {
				state, message = "error", err.Error()
			}
			a.publishLogStatus(LogStatus{Path: currentPath, Source: source, State: state, Message: message, Lines: lines, Triggers: triggers, LastTrigger: lastTrigger})
			continue
		}
		if needsBaseline {
			if !missingWhileWaiting {
				offset = info.Size()
			}
			currentSize = info.Size()
			rest = ""
			needsBaseline = false
			missingWhileWaiting = false
		}
		if info.Size() < offset || (lastInfo != nil && !os.SameFile(lastInfo, info)) {
			offset, rest = 0, ""
			pendingKind, pendingRank = "", 0
		}
		lastInfo = info
		currentSize = info.Size()
		if currentSize-offset > logMaxRead {
			offset = currentSize - logMaxRead
			rest = ""
		}
		if currentSize > offset {
			f, openErr := os.Open(currentPath)
			if openErr == nil {
				buf := make([]byte, currentSize-offset)
				n, readErr := f.ReadAt(buf, offset)
				f.Close()
				if readErr == nil || n > 0 {
					offset += int64(n)
					parts := strings.Split(rest+string(buf[:n]), "\n")
					rest = parts[len(parts)-1]
					if len(rest) > logMaxLine {
						rest = rest[len(rest)-logMaxLine:]
					}
					for _, line := range parts[:len(parts)-1] {
						line = strings.TrimSuffix(line, "\r")
						if len(line) > logMaxLine {
							line = line[len(line)-logMaxLine:]
						}
						lines++
						if detailLogLine.MatchString(line) {
							detailSeen++
						}
						for _, rule := range defaultLogRules {
							if !rule.pattern.MatchString(line) {
								continue
							}
							triggers++
							lastTrigger = rule.kind
							switch rule.kind {
							case "skip":
								pendingKind, pendingRank = "", 0
								pendingSettle = 0
								pendingDue = time.Time{}
							case "speed":
							case "board":
								if pendingKind == "treasure" {
									a.requestCapture("board")
									pendingKind, pendingRank, pendingSettle = "", 0, 0
									pendingDue = time.Time{}
									break
								}
								fallthrough
							default:
								if rule.rank > pendingRank || pendingKind == "" {
									pendingKind, pendingRank = rule.kind, rule.rank
								}
								if rule.settle > pendingSettle {
									pendingSettle = rule.settle
								}
								pendingDue = time.Now().Add(pendingSettle)
							}
							break
						}
					}
				}
			}
		}
		if pendingKind != "" && !pendingDue.IsZero() && !time.Now().Before(pendingDue) {
			a.mu.Lock()
			a.captureRequested = true
			a.captureReason = pendingKind
			a.mu.Unlock()
			pendingKind, pendingRank, pendingSettle = "", 0, 0
			pendingDue = time.Time{}
		}
		message := "Following the UE game log. Enable the game's log in Account Related if it stays quiet."
		if detailSeen == 0 && lines > 0 {
			message = "The UE log is readable, but no TLLua/TLGame detail lines have appeared yet."
		}
		a.publishLogStatus(LogStatus{Path: currentPath, Source: source, State: "following", Message: message, Bytes: currentSize, Lines: lines, Triggers: triggers, LastTrigger: lastTrigger})
	}
}
