package torrent

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	g "github.com/anacrolix/generics"
	libtorrent "github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	torrentstorage "github.com/anacrolix/torrent/storage"
)

// use one root
var appRoot = detectAppRoot()

func AppRoot() string {
	return appRoot
}

func defaultStatePath() string {
	return appFilePath(".torrent-state")
}

func stateSessionPath(statePath string) string {
	return normalizePath(filepath.Join(statePath, "session"))
}

func statePiecesPath(statePath string) string {
	return normalizePath(filepath.Join(statePath, "pieces"))
}

func appFilePath(name string) string {
	return normalizePath(filepath.Join(AppRoot(), name))
}

func detectAppRoot() string {
	if wd, err := os.Getwd(); err == nil && wd != "" {
		if root := findGoModuleRoot(wd); root != "" {
			return normalizePath(root)
		}
		return normalizePath(wd)
	}

	if exe, err := os.Executable(); err == nil && exe != "" {
		dir := filepath.Dir(exe)
		if root := findGoModuleRoot(dir); root != "" {
			return normalizePath(root)
		}
		return normalizePath(dir)
	}

	return normalizePath(".")
}

func findGoModuleRoot(start string) string {
	current := normalizePath(start)
	for current != "" {
		if fileExists(filepath.Join(current, "go.mod")) {
			return current
		}

		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
	return ""
}

func relocateLegacyStateFiles(downloadPath string, statePath string) {
	piecesPath := statePiecesPath(statePath)
	_ = os.MkdirAll(piecesPath, 0o755)

	for _, name := range []string{".torrent.db", ".torrent.db-shm", ".torrent.db-wal"} {
		dst := filepath.Join(piecesPath, name)

		for _, src := range []string{
			filepath.Join(downloadPath, name),
			filepath.Join(statePath, name),
		} {
			if !fileExists(src) || fileExists(dst) {
				continue
			}
			_ = os.Rename(src, dst)
		}
	}
}

func newTorrentStorage(downloadPath string, statePath string) torrentstorage.ClientImplCloser {
	_ = os.MkdirAll(downloadPath, 0o755)
	_ = os.MkdirAll(statePath, 0o755)

	pieceCompletion, err := torrentstorage.NewDefaultPieceCompletionForDir(statePath)
	if err != nil {
		pieceCompletion = torrentstorage.NewMapPieceCompletion()
	}

	return torrentstorage.NewFileOpts(torrentstorage.NewFileClientOpts{
		ClientBaseDir:   downloadPath,
		PieceCompletion: pieceCompletion,
		UsePartFiles:    g.Some(false),
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func isLegacyDefaultDownloadPath(path string) bool {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "." || clean == "" {
		return true
	}
	return clean == filepath.Clean("./downloads") || clean == filepath.Clean("downloads")
}

func defaultDownloadPath() string {
	var candidates []string

	if profile := strings.TrimSpace(os.Getenv("USERPROFILE")); profile != "" {
		candidates = append(candidates, filepath.Join(profile, "Desktop"))
	}
	if oneDrive := strings.TrimSpace(os.Getenv("OneDrive")); oneDrive != "" {
		candidates = append(candidates, filepath.Join(oneDrive, "Desktop"))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, "Desktop"))
	}

	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return normalizePath(candidate)
		}
	}

	if len(candidates) > 0 {
		return normalizePath(candidates[0])
	}

	return appFilePath("downloads")
}

func prepareDownloads(downloads map[string]*DownloadTask) {
	for id, task := range downloads {
		if task == nil {
			delete(downloads, id)
			continue
		}

		if task.ID == "" {
			task.ID = id
		}

		task.Torrent = nil
		task.lastUsefulBytes = 0
		task.lastTickAt = time.Time{}
		task.lastSaveAt = time.Time{}
		task.lastAnnounceAt = time.Time{}
		task.lastTransferAt = time.Time{}
		task.lastPeerAt = time.Time{}
		task.lastRestartAt = time.Time{}
		task.Speed = 0
		task.ETA = 0
		task.Peers = 0

		switch task.Status {
		case "finished":
			task.Paused = false
		case "stopped", "error":
			task.Paused = true
		default:
			task.Status = "queued"
			task.Paused = false
		}
	}
}

func normalizePath(path string) string {
	path = strings.Map(func(r rune) rune {
		switch r {
		case '\u200b', '\u200c', '\u200d', '\ufeff', '\u00a0':
			return -1
		default:
			return r
		}
	}, path)
	path = strings.TrimSpace(path)
	path = strings.TrimFunc(path, func(r rune) bool {
		switch r {
		case '"', '\'', '`', '\u201c', '\u201d', '\u2018', '\u2019':
			return true
		default:
			return unicode.IsSpace(r)
		}
	})

	lower := strings.ToLower(path)
	if strings.HasPrefix(lower, "file:///") {
		path = path[len("file:///"):]
		path = filepath.FromSlash(path)

		if len(path) > 2 && path[0] == '\\' && path[2] == ':' {
			path = path[1:]
		}
	}
	path = filepath.Clean(path)

	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}

	return path
}

func sanitizeTorrentFileName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimSuffix(name, filepath.Ext(name))

	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}

	return b.String()
}

func resolveTorrentFilePath(path string) (string, error) {
	normalized := normalizePath(path)

	info, err := os.Stat(normalized)
	if err == nil {
		if info.IsDir() {
			return normalized, fmt.Errorf("path is a directory: %s", normalized)
		}
		return normalized, nil
	}

	dir := filepath.Dir(normalized)
	base := filepath.Base(normalized)
	if dir == "" || dir == "." || base == "" {
		return normalized, err
	}

	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		return normalized, err
	}

	want := sanitizeTorrentFileName(base)
	if want == "" {
		return normalized, err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".torrent") {
			continue
		}

		if sanitizeTorrentFileName(entry.Name()) == want {
			return filepath.Join(dir, entry.Name()), nil
		}
	}

	return normalized, err
}

func ensureTorrentLayout(root string, t *libtorrent.Torrent) {
	if root == "" || t == nil || t.Info() == nil {
		return
	}

	for _, file := range t.Files() {
		if file == nil {
			continue
		}

		targetDir := filepath.Join(root, filepath.Dir(filepath.FromSlash(file.Path())))
		_ = os.MkdirAll(targetDir, 0o755)
	}
}

func taskDownloadRoot(downloadPath string, task *DownloadTask) string {
	name := taskTargetName(task, nil)
	if strings.TrimSpace(name) == "" || strings.TrimSpace(downloadPath) == "" {
		return ""
	}

	return normalizePath(filepath.Join(downloadPath, filepath.FromSlash(name)))
}

func taskTargetName(task *DownloadTask, t *libtorrent.Torrent) string {
	if task == nil {
		return ""
	}

	var name string

	if t != nil {
		if info := t.Info(); info != nil {
			name = strings.TrimSpace(info.BestName())
		}
	}
	if name == "" && task.Torrent != nil {
		if info := task.Torrent.Info(); info != nil {
			name = strings.TrimSpace(info.BestName())
		}
	}
	if name == "" && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(task.Source)), "magnet:") && fileExists(task.Source) {
		if mi, err := metainfo.LoadFromFile(task.Source); err == nil {
			if info, err := mi.UnmarshalInfo(); err == nil {
				name = strings.TrimSpace(info.BestName())
			}
		}
	}

	if name == "" {
		name = strings.TrimSpace(task.Name)
	}

	return name
}

func recoverTaskData(downloadPath string, task *DownloadTask, t *libtorrent.Torrent) {
	name := taskTargetName(task, t)
	if downloadPath == "" || name == "" {
		return
	}

	target := normalizePath(filepath.Join(downloadPath, filepath.FromSlash(name)))
	if pathExists(target) {
		return
	}

	for _, candidateRoot := range alternateDownloadRoots(downloadPath) {
		candidate := normalizePath(filepath.Join(candidateRoot, filepath.FromSlash(name)))
		if !pathExists(candidate) {
			continue
		}

		_ = os.MkdirAll(filepath.Dir(target), 0o755)
		if err := os.Rename(candidate, target); err == nil {
			return
		}
	}
}

func migrateDownloadPath(oldPath string, newPath string, downloads map[string]*DownloadTask) {
	oldPath = normalizePath(oldPath)
	newPath = normalizePath(newPath)
	if oldPath == "" || newPath == "" || strings.EqualFold(oldPath, newPath) {
		return
	}

	for _, task := range downloads {
		if task == nil {
			continue
		}

		source := taskDownloadRoot(oldPath, task)
		target := taskDownloadRoot(newPath, task)
		if source == "" || target == "" || !pathExists(source) || pathExists(target) {
			continue
		}

		_ = os.MkdirAll(filepath.Dir(target), 0o755)
		_ = os.Rename(source, target)
	}
}

func alternateDownloadRoots(current string) []string {
	roots := []string{
		current,
		appFilePath("downloads"),
		defaultDownloadPath(),
	}

	out := make([]string, 0, len(roots))
	seen := make(map[string]struct{})
	for _, root := range roots {
		root = normalizePath(root)
		if root == "" {
			continue
		}
		key := strings.ToLower(root)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, root)
	}

	return out
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
