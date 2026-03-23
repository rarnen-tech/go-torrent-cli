package torrent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type badStore struct {
	err error
}

func (s badStore) LoadDownloads() (map[string]*DownloadTask, error) {
	return nil, s.err
}

func (s badStore) SaveDownloads(map[string]*DownloadTask) error {
	return s.err
}

func (s badStore) Close() error {
	return nil
}

func withTestRoot(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	oldRoot := appRoot
	appRoot = root
	t.Cleanup(func() {
		appRoot = oldRoot
	})

	return root
}

func sampleTasks(count int) map[string]*DownloadTask {
	out := make(map[string]*DownloadTask, count)
	for i := 1; i <= count; i++ {
		id := strconv.Itoa(i)
		out[id] = &DownloadTask{
			ID:             id,
			Name:           "task-" + id,
			Source:         "source-" + id,
			Status:         "downloading",
			Progress:       i % 100,
			CompletedBytes: int64(i) * 1024,
			TotalBytes:     int64(i+1) * 2048,
			Speed:          float64(i) / 10,
			ETA:            i,
			Peers:          i % 8,
		}
	}
	return out
}

func TestPrepareDownloadsResetsRuntimeState(t *testing.T) {
	downloads := map[string]*DownloadTask{
		"1": {
			ID:             "1",
			Status:         "downloading",
			Speed:          2.4,
			ETA:            10,
			Peers:          4,
			lastUsefulBytes: 10,
			lastTickAt:     time.Now(),
		},
		"2": {
			ID:     "2",
			Status: "finished",
			Paused: true,
		},
		"3": {
			ID:     "3",
			Status: "stopped",
			Paused: false,
		},
		"4": {
			ID:     "4",
			Status: "error",
			Paused: false,
		},
	}

	prepareDownloads(downloads)

	if downloads["1"].Status != "queued" {
		t.Fatalf("task 1 status = %q, want queued", downloads["1"].Status)
	}
	if downloads["1"].Paused {
		t.Fatal("task 1 must not be paused")
	}
	if downloads["1"].Speed != 0 || downloads["1"].ETA != 0 || downloads["1"].Peers != 0 {
		t.Fatal("task 1 runtime fields were not reset")
	}
	if downloads["2"].Status != "finished" || downloads["2"].Paused {
		t.Fatal("finished task state changed in a wrong way")
	}
	if downloads["3"].Status != "stopped" || !downloads["3"].Paused {
		t.Fatal("stopped task must stay paused")
	}
	if downloads["4"].Status != "error" || !downloads["4"].Paused {
		t.Fatal("error task must stay paused")
	}
}

func TestSortedIDsUsesNumericOrder(t *testing.T) {
	downloads := map[string]*DownloadTask{
		"10": {},
		"2":  {},
		"1":  {},
	}

	got := SortedIDs(downloads)
	want := []string{"1", "2", "10"}

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ids = %v, want %v", got, want)
	}
}

func TestMemoryDownloadStoreCopiesData(t *testing.T) {
	store := newMemoryDownloadStore()

	source := map[string]*DownloadTask{
		"1": {
			ID:     "1",
			Name:   "Detroit",
			Status: "queued",
		},
	}

	if err := store.SaveDownloads(source); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	source["1"].Name = "changed"

	loaded, err := store.LoadDownloads()
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	if loaded["1"].Name != "Detroit" {
		t.Fatalf("store kept external mutation: %q", loaded["1"].Name)
	}

	loaded["1"].Name = "broken"

	loadedAgain, err := store.LoadDownloads()
	if err != nil {
		t.Fatalf("second load failed: %v", err)
	}
	if loadedAgain["1"].Name != "Detroit" {
		t.Fatalf("store returned shared pointer: %q", loadedAgain["1"].Name)
	}
}

func TestLoadDownloadsReadsLegacyJSON(t *testing.T) {
	root := withTestRoot(t)

	legacy := map[string]*DownloadTask{
		"1": {
			ID:     "1",
			Name:   "Detroit",
			Status: "queued",
			Source: "detroit.torrent",
		},
	}

	raw, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "downloads.json"), raw, 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	store := newMemoryDownloadStore()
	downloads, warn := loadDownloads(store)

	if warn != "" {
		t.Fatalf("unexpected warning: %s", warn)
	}
	if len(downloads) != 1 || downloads["1"] == nil {
		t.Fatalf("legacy downloads were not loaded: %#v", downloads)
	}
	if downloads["1"].Name != "Detroit" {
		t.Fatalf("loaded wrong task name: %q", downloads["1"].Name)
	}
	if _, err := os.Stat(filepath.Join(root, "downloads.json")); err != nil {
		t.Fatalf("legacy file must stay for memory store: %v", err)
	}
}

func TestLoadDownloadsReportsStoreError(t *testing.T) {
	withTestRoot(t)

	downloads, warn := loadDownloads(badStore{err: errors.New("boom")})
	if len(downloads) != 0 {
		t.Fatalf("downloads = %#v, want empty", downloads)
	}
	if !strings.Contains(warn, "boom") {
		t.Fatalf("warn = %q, want boom", warn)
	}
}

func TestMigrateDownloadPathMovesData(t *testing.T) {
	oldPath := t.TempDir()
	newPath := t.TempDir()

	task := &DownloadTask{
		ID:   "1",
		Name: "Detroit.iso",
	}

	source := filepath.Join(oldPath, "Detroit.iso")
	if err := os.WriteFile(source, []byte("data"), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	migrateDownloadPath(oldPath, newPath, map[string]*DownloadTask{
		"1": task,
	})

	if _, err := os.Stat(filepath.Join(newPath, "Detroit.iso")); err != nil {
		t.Fatalf("target file was not moved: %v", err)
	}
	if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source file still exists: %v", err)
	}
}

func TestResolveTorrentFilePathFindsCloseName(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "Detroit-Become-Human.torrent")
	if err := os.WriteFile(want, []byte("x"), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	got, err := resolveTorrentFilePath(filepath.Join(dir, "Detroit Become Human.torrent"))
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestCombineTrackerListsKeepsOrderAndDropsDupes(t *testing.T) {
	got := combineTrackerLists(
		[][]string{{"udp://a", "udp://b"}},
		[][]string{{"udp://b", "udp://c"}},
	)

	if len(got) != 2 {
		t.Fatalf("tiers = %d, want 2", len(got))
	}
	if strings.Join(got[0], ",") != "udp://a,udp://b" {
		t.Fatalf("tier 0 = %v", got[0])
	}
	if strings.Join(got[1], ",") != "udp://c" {
		t.Fatalf("tier 1 = %v", got[1])
	}
}

func TestKinozalTrackerPrefix(t *testing.T) {
	prefix, ok := kinozalTrackerPrefix("tr3.torrent4me.com")
	if !ok {
		t.Fatal("expected kinozal tracker host")
	}
	if prefix != "tr3" {
		t.Fatalf("prefix = %q, want tr3", prefix)
	}

	if _, ok := kinozalTrackerPrefix("example.com"); ok {
		t.Fatal("unexpected match for regular host")
	}
}

func BenchmarkSortedIDs100(b *testing.B) {
	downloads := sampleTasks(100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = SortedIDs(downloads)
	}
}

func BenchmarkMemoryStoreSave100(b *testing.B) {
	store := newMemoryDownloadStore()
	downloads := sampleTasks(100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := store.SaveDownloads(downloads); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkClientGetDownloads100(b *testing.B) {
	client := &Client{
		Downloads: sampleTasks(100),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = client.GetDownloads()
	}
}
