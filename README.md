# Tartarus (Go + Wails)

A Go port of the Tartarus Simulator's solver and overlay, for Windows. The rules engine and the Monte Carlo solver run natively in Go on every core instead of in the browser's JS sandbox.

## Support

Issues and pull requests are welcome. You can also support development on Ko-fi.

<a href="https://ko-fi.com/vali98"><img src="https://storage.ko-fi.com/cdn/kofi5.png?v=6" alt="Support me on Ko-fi" height="36"></a>

## Layout

| Path | What |
| --- | --- |
| `engine/` | Rules (port of `js/engine.js`): cells, inmates, 31 tools, 14 events, objectives, start cards, gifts. Fixed-size state, cheap `Clone`, logging off in playouts. `FromBoard` builds a state from a typed board. |
| `solver/` | Strategy and chain math (port of `js/solver.js`), tuned profiles, measured tool-set data (`solver/data/tool-combos.json`), and a parallel Monte Carlo solver with common random numbers and early stop. |
| `app.go`, `main.go` | Wails app: foreground-only live scan loop, solve streaming (`solve` events), window modes and adaptive CPU budgeting. |
| `win_windows.go` | Win32: game-window capture, frameless topmost no-activate overlay, capture exclusion (`WDA_EXCLUDEFROMCAPTURE`), click-through, game window lookup and hotkeys. |
| `frontend/` | Plain HTML/JS/CSS (no build step): screenshot input and review, board editor and overlay. `scan.js` and `scan-data.js` are the simulator's trained pixel reader and references; it runs in a Web Worker, entirely on-device. |

## Run

```
go install github.com/wailsapp/wails/v2/cmd/wails@latest
wails build            # build\bin\tartarus.exe
wails dev              # live reload
go test ./solver -v    # strategy average and solve timing
```

## Overlay

One window switches between the analyzer and a compact card (Wails v2 has a single window). The card is always on top, does not take focus, stays out of screenshots and recordings (setting), and can be made click-through. With live scanning enabled, the app captures the game client area only while the game is foreground, handles at most one frame at a time, and sends changed boards through the Go solver. Use borderless or windowed mode; exclusive fullscreen cannot be captured.

## Screenshot Reader

The **Follow foreground game** toggle captures and reads the game automatically. The same local reader accepts an image file, clipboard image or dropped screenshot. It reads cells, warden, today's cards, riot countdown, instruments, start-card badge, Chromatic vault count and consume-screen meals. Instrument offers use their dedicated trained card reader; a log-confirmed Chromatic Treasure opens a pending gift choice, including Opulence's extra picks. Solver advice waits for visible patrol/decision controls or an explicit offer/consume screen, rather than recalculating on every ordinary board frame. Findings below 60% confidence are called out; clicking a cell finding selects it for correction. On a consume-selection screen, the solver only offers the highlighted meals for the current bite, then resumes chain simulation. The objective still needs to be set by hand, Tusk marks are retained from the previous board when possible, and other cells on the darkened consume screen need a manual review.

UE log following discovers the latest `UE_game.log` in the game's Steam libraries or known standalone install locations. In the analyzer's Screenshot Reader panel, use **Browse** to select a log or enter a path and press **Use**; leave it blank to return to auto-discovery. The follower reads new lines only, handles truncation/replacement and limits each poll to 1 MiB. Its built-in S16 triggers request a scan after the game settles; the foreground sampler remains as a fallback. For trigger-driven scans, enable the game's detailed log from Esc → Account Related → Enable Log, then restart the game session. Log contents stay local.

The simulator's labeled screenshot corpus is not present in this workspace, so the reader was validated for script loading, Wails asset embedding and build, but not rescored against the full screenshot set here.

| Hotkey | Action |
| --- | --- |
| Ctrl+Alt+O | Overlay mode, then show/hide the card |
| Ctrl+Alt+A | Analyzer |
| Ctrl+Alt+S | Re-solve |
| Ctrl+Alt+L | Lock/unlock click-through |

Run the game borderless or windowed: nothing can draw over exclusive fullscreen.

## Performance

Full runs of the strategy, single thread, same machine: JS (Node) about 170 runs/s, Go about 2,600 runs/s. Average Rainbows per run agree (4.03 Go, 4.09 JS over different seeds). Solver worker choices scale with the logical CPU count. Game-first defaults to one worker; Most reserves one logical processor, or two on systems with 12 or more, and the process runs below normal priority. A one-logical-processor system cannot reserve a separate core.

## Not ported yet

- Trainer grades, "Do it here", the Lab, board sharing and reports, site/app updates.
- Structured log records (the Go engine logs plain text when `LogOn` is set).
