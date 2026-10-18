//go:build windows

package main

import (
	"errors"
	"image"
	"regexp"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/kbinani/screenshot"
	"golang.org/x/sys/windows"
)

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	procFindWindowW              = user32.NewProc("FindWindowW")
	procGetWindowLongPtrW        = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtrW        = user32.NewProc("SetWindowLongPtrW")
	procSetWindowPos             = user32.NewProc("SetWindowPos")
	procSetWindowDisplayAffinity = user32.NewProc("SetWindowDisplayAffinity")
	procSetLayeredWindowAttrs    = user32.NewProc("SetLayeredWindowAttributes")
	procShowWindow               = user32.NewProc("ShowWindow")
	procGetWindowRect            = user32.NewProc("GetWindowRect")
	procGetClientRect            = user32.NewProc("GetClientRect")
	procClientToScreen           = user32.NewProc("ClientToScreen")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procIsIconic                 = user32.NewProc("IsIconic")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procRegisterHotKey           = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey         = user32.NewProc("UnregisterHotKey")
	procGetMessageW              = user32.NewProc("GetMessageW")
	procGetDpiForWindow          = user32.NewProc("GetDpiForWindow")
	procGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
	procSetWindowsHookExW        = user32.NewProc("SetWindowsHookExW")
	procCallNextHookEx           = user32.NewProc("CallNextHookEx")
	procUnhookWindowsHookEx      = user32.NewProc("UnhookWindowsHookEx")
	procGetModuleHandleW         = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW")
)

const (
	gwlStyle   = -16
	gwlExStyle = -20

	wsCaption     = 0x00C00000
	wsThickFrame  = 0x00040000
	wsSysMenu     = 0x00080000
	wsMinimizeBox = 0x00020000
	wsMaximizeBox = 0x00010000

	wsExToolWindow  = 0x00000080
	wsExNoActivate  = 0x08000000
	wsExLayered     = 0x00080000
	wsExTransparent = 0x00000020
	wsExAppWindow   = 0x00040000

	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoZOrder     = 0x0004
	swpNoActivate   = 0x0010
	swpFrameChanged = 0x0020
	swpShowWindow   = 0x0040

	swHide           = 0
	swShowNoActivate = 4
	swShow           = 5

	hwndTopmost   = ^uintptr(0) // -1
	hwndNoTopmost = ^uintptr(1) // -2
	wdaNone       = 0x0
	wdaExclude    = 0x11
	lwaAlpha      = 0x2
	wmHotkey      = 0x0312
	whMouseLL     = 14
	hcAction      = 0
	wmLButtonUp   = 0x0202
	wmRButtonUp   = 0x0205
	wmMButtonUp   = 0x0208
	wmXButtonUp   = 0x020C
	modAlt        = 0x1
	modControl    = 0x2
	modShift      = 0x4
	modNoRepeat   = 0x4000
	smCxScreen    = 0
	smCyScreen    = 1
)

type rect struct{ Left, Top, Right, Bottom int32 }
type point struct{ X, Y int32 }
type mouseLLHookData struct {
	Point     point
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type hotkeySpec struct {
	modifiers uint
	key       rune
}

func utf16(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }

func findWindow(title string) uintptr {
	h, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(utf16(title))))
	return h
}

func getLong(h uintptr, idx int) uintptr {
	v, _, _ := procGetWindowLongPtrW.Call(h, uintptr(idx))
	return v
}

func setLong(h uintptr, idx int, v uintptr) {
	procSetWindowLongPtrW.Call(h, uintptr(idx), v)
}

func windowRect(h uintptr) (rect, bool) {
	var r rect
	ok, _, _ := procGetWindowRect.Call(h, uintptr(unsafe.Pointer(&r)))
	return r, ok != 0
}

func dpiScale(h uintptr) float64 {
	d, _, _ := procGetDpiForWindow.Call(h)
	if d == 0 {
		return 1
	}
	return float64(d) / 96
}

func screenSize() (int, int) {
	w, _, _ := procGetSystemMetrics.Call(smCxScreen)
	h, _, _ := procGetSystemMetrics.Call(smCyScreen)
	return int(w), int(h)
}

func setPos(h uintptr, after uintptr, x, y, w, hgt int, flags uintptr) {
	procSetWindowPos.Call(h, after, uintptr(x), uintptr(y), uintptr(w), uintptr(hgt), flags)
}

func showWindow(h uintptr, cmd int) { procShowWindow.Call(h, uintptr(cmd)) }

// ---------- Overlay styling ----------

type frameState struct {
	style, exStyle uintptr
	saved          bool
}

var saved frameState

// styleOverlay strips the frame, keeps the window out of the taskbar and
// focus order, and (optionally) hides it from screen captures.
func styleOverlay(h uintptr, hideFromCapture bool, opacity int) {
	if !saved.saved {
		saved = frameState{style: getLong(h, gwlStyle), exStyle: getLong(h, gwlExStyle), saved: true}
	}
	style := saved.style &^ (wsCaption | wsThickFrame | wsSysMenu | wsMinimizeBox | wsMaximizeBox)
	setLong(h, gwlStyle, style)
	ex := (saved.exStyle | wsExToolWindow | wsExNoActivate | wsExLayered) &^ wsExAppWindow
	setLong(h, gwlExStyle, ex)
	setOpacity(h, opacity)
	affinity := uintptr(wdaNone)
	if hideFromCapture {
		affinity = wdaExclude
	}
	procSetWindowDisplayAffinity.Call(h, affinity)
	setPos(h, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate|swpFrameChanged)
}

func styleNormal(h uintptr) {
	if saved.saved {
		setLong(h, gwlStyle, saved.style)
		setLong(h, gwlExStyle, saved.exStyle&^(wsExTransparent))
	}
	procSetWindowDisplayAffinity.Call(h, wdaNone)
	setPos(h, hwndNoTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpFrameChanged)
}

func setOpacity(h uintptr, percent int) {
	percent = max(30, min(100, percent))
	procSetLayeredWindowAttrs.Call(h, 0, uintptr(percent*255/100), lwaAlpha)
}

// setClickThrough lets mouse clicks fall through the overlay to the game.
func setClickThrough(h uintptr, on bool) {
	ex := getLong(h, gwlExStyle)
	if on {
		ex |= wsExTransparent | wsExLayered
	} else {
		ex &^= wsExTransparent
	}
	setLong(h, gwlExStyle, ex)
}

func setHideFromCapture(h uintptr, on bool) {
	a := uintptr(wdaNone)
	if on {
		a = wdaExclude
	}
	procSetWindowDisplayAffinity.Call(h, a)
}

// ---------- Finding the game window ----------

type gameWindow struct {
	HWND       uintptr
	Title      string
	Rect       rect
	Foreground bool
	Minimized  bool
}

// findGame returns the first visible top-level window whose title matches re.
func findGame(re *regexp.Regexp) (gameWindow, bool) {
	var found gameWindow
	var ok bool
	fg, _, _ := procGetForegroundWindow.Call()
	cb := syscall.NewCallback(func(h uintptr, _ uintptr) uintptr {
		if vis, _, _ := procIsWindowVisible.Call(h); vis == 0 {
			return 1
		}
		var buf [256]uint16
		n, _, _ := procGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), 256)
		if n == 0 {
			return 1
		}
		title := syscall.UTF16ToString(buf[:n])
		if !re.MatchString(title) {
			return 1
		}
		r, _ := windowRect(h)
		ico, _, _ := procIsIconic.Call(h)
		found, ok = gameWindow{HWND: h, Title: title, Rect: r, Foreground: h == fg, Minimized: ico != 0}, true
		return 0
	})
	procEnumWindows.Call(cb, 0)
	return found, ok
}

// captureGameFrame captures only the game's client area from the desktop.
func captureGameFrame(hwnd uintptr) (*image.RGBA, error) {
	var bounds rect
	if ok, _, err := procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&bounds))); ok == 0 {
		return nil, err
	}
	if bounds.Right <= bounds.Left || bounds.Bottom <= bounds.Top {
		return nil, errors.New("game client area is empty")
	}
	topLeft := point{X: bounds.Left, Y: bounds.Top}
	bottomRight := point{X: bounds.Right, Y: bounds.Bottom}
	if ok, _, err := procClientToScreen.Call(hwnd, uintptr(unsafe.Pointer(&topLeft))); ok == 0 {
		return nil, err
	}
	if ok, _, err := procClientToScreen.Call(hwnd, uintptr(unsafe.Pointer(&bottomRight))); ok == 0 {
		return nil, err
	}
	r := image.Rect(int(topLeft.X), int(topLeft.Y), int(bottomRight.X), int(bottomRight.Y))
	if r.Empty() {
		return nil, errors.New("game client area is empty")
	}
	return screenshot.CaptureRect(r)
}

func pointInClient(hwnd uintptr, x, y int32) bool {
	var bounds rect
	if ok, _, _ := procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&bounds))); ok == 0 {
		return false
	}
	topLeft := point{X: bounds.Left, Y: bounds.Top}
	bottomRight := point{X: bounds.Right, Y: bounds.Bottom}
	if ok, _, _ := procClientToScreen.Call(hwnd, uintptr(unsafe.Pointer(&topLeft))); ok == 0 {
		return false
	}
	if ok, _, _ := procClientToScreen.Call(hwnd, uintptr(unsafe.Pointer(&bottomRight))); ok == 0 {
		return false
	}
	return x >= topLeft.X && x < bottomRight.X && y >= topLeft.Y && y < bottomRight.Y
}

func foregroundWindow() uintptr {
	h, _, _ := procGetForegroundWindow.Call()
	return h
}

func windowVisible(hwnd uintptr) bool {
	visible, _, _ := procIsWindowVisible.Call(hwnd)
	return visible != 0
}

func activateWindow(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	showWindow(hwnd, swShow)
	ok, _, _ := procSetForegroundWindow.Call(hwnd)
	return ok != 0
}

// ---------- Global hotkeys ----------

// runHotkeys registers global hotkeys and calls fn with the id of each press.
// It blocks, so run it in its own goroutine.
func runHotkeys(keys map[int]hotkeySpec, fn func(id int), onMouseUp func(hwnd uintptr, x, y int32)) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	for id, key := range keys {
		procRegisterHotKey.Call(0, uintptr(id), uintptr(key.modifiers)|modNoRepeat, uintptr(key.key))
	}
	mouseCallback := syscall.NewCallback(func(code uintptr, message uintptr, input *mouseLLHookData) uintptr {
		if code == hcAction && onMouseUp != nil && (message == wmLButtonUp || message == wmRButtonUp || message == wmMButtonUp || message == wmXButtonUp) {
			foreground := foregroundWindow()
			onMouseUp(foreground, input.Point.X, input.Point.Y)
		}
		result, _, _ := procCallNextHookEx.Call(0, uintptr(code), message, uintptr(unsafe.Pointer(input)))
		return result
	})
	module, _, _ := procGetModuleHandleW.Call(0)
	hook, _, _ := procSetWindowsHookExW.Call(whMouseLL, mouseCallback, module, 0)
	defer func() {
		if hook != 0 {
			procUnhookWindowsHookEx.Call(hook)
		}
		for id := range keys {
			procUnregisterHotKey.Call(0, uintptr(id))
		}
	}()
	var msg struct {
		hwnd    uintptr
		message uint32
		wParam  uintptr
		lParam  uintptr
		time    uint32
		pt      struct{ x, y int32 }
	}
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		if msg.message == wmHotkey {
			fn(int(msg.wParam))
		}
	}
}

// lowerPriority makes the app yield to the game.
func lowerPriority(on bool) {
	class := uint32(windows.NORMAL_PRIORITY_CLASS)
	if on {
		class = windows.BELOW_NORMAL_PRIORITY_CLASS
	}
	windows.SetPriorityClass(windows.CurrentProcess(), class)
}
