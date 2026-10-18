//go:build windows

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const steamAppID = "1974050"

var (
	installDirPattern  = regexp.MustCompile(`(?im)"installdir"\s+"([^"]+)"`)
	libraryPathPattern = regexp.MustCompile(`(?im)"path"\s+"([^"]+)"`)
)

func registryString(root registry.Key, keyPath, valueName string) string {
	key, err := registry.OpenKey(root, keyPath, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()
	value, _, err := key.GetStringValue(valueName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func steamInstallRoots() []string {
	candidates := []string{
		registryString(registry.CURRENT_USER, `Software\Valve\Steam`, "SteamPath"),
		registryString(registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Valve\Steam`, "InstallPath"),
		registryString(registry.LOCAL_MACHINE, `SOFTWARE\Valve\Steam`, "InstallPath"),
		`C:\Program Files (x86)\Steam`,
	}
	seen := make(map[string]bool)
	var roots []string
	for _, root := range candidates {
		if root == "" {
			continue
		}
		root = filepath.Clean(strings.ReplaceAll(root, "/", `\`))
		key := strings.ToLower(root)
		if !seen[key] {
			seen[key] = true
			roots = append(roots, root)
		}
	}
	return roots
}

func virtualStorePath(path string) string {
	local := os.Getenv("LOCALAPPDATA")
	clean := filepath.Clean(path)
	lower := strings.ToLower(clean)
	for _, prefix := range []string{`c:\program files\`, `c:\program files (x86)\`} {
		if strings.HasPrefix(lower, prefix) && local != "" {
			rel := clean[3:]
			return filepath.Join(local, "VirtualStore", rel)
		}
	}
	return ""
}

// findUELog prefers an explicit path, then the active Steam installation, then
// known standalone installs. A candidate path may be returned before the game
// creates the file so the tailer can begin following it on launch.
func findUELog(preferred string) (string, string) {
	if strings.TrimSpace(preferred) != "" {
		return filepath.Clean(strings.TrimSpace(preferred)), "chosen path"
	}
	var roots []string
	seenRoots := make(map[string]bool)
	addRoot := func(path string) {
		if path == "" {
			return
		}
		path = filepath.Clean(strings.ReplaceAll(path, `\\`, `\`))
		key := strings.ToLower(path)
		if !seenRoots[key] {
			seenRoots[key] = true
			roots = append(roots, path)
		}
	}
	for _, root := range steamInstallRoots() {
		addRoot(root)
	}
	for _, root := range append([]string(nil), roots...) {
		data, err := os.ReadFile(filepath.Join(root, "steamapps", "libraryfolders.vdf"))
		if err != nil {
			continue
		}
		for _, match := range libraryPathPattern.FindAllStringSubmatch(string(data), -1) {
			addRoot(match[1])
		}
	}

	var logs []string
	for _, root := range roots {
		manifest := filepath.Join(root, "steamapps", "appmanifest_"+steamAppID+".acf")
		data, err := os.ReadFile(manifest)
		if err != nil {
			continue
		}
		if match := installDirPattern.FindSubmatch(data); len(match) == 2 {
			logs = append(logs, filepath.Join(root, "steamapps", "common", string(match[1]), "UE_game", "TorchLight", "Saved", "Logs", "UE_game.log"))
		}
	}
	for _, base := range []string{`C:\Program Files (x86)\Torchlight Infinite`, `C:\Program Files\Torchlight Infinite`} {
		logs = append(logs, filepath.Join(base, "Game", "UE_game", "TorchLight", "Saved", "Logs", "UE_game.log"))
	}
	for _, path := range append([]string(nil), logs...) {
		if virtual := virtualStorePath(path); virtual != "" {
			logs = append(logs, virtual)
		}
	}

	type candidate struct {
		path string
		mod  int64
	}
	var present []candidate
	for _, path := range logs {
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() {
			present = append(present, candidate{path: path, mod: info.ModTime().UnixNano()})
		}
	}
	if len(present) > 0 {
		sort.Slice(present, func(i, j int) bool { return present[i].mod > present[j].mod })
		return present[0].path, "installed game log"
	}
	for _, path := range logs {
		gameDir := filepath.Clean(filepath.Join(filepath.Dir(path), "..", "..", ".."))
		if info, err := os.Stat(gameDir); err == nil && info.IsDir() {
			return path, "waiting for game log"
		}
	}
	return "", "not found"
}
