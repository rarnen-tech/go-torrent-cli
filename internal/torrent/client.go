package torrent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/dht/v2"
	analog "github.com/anacrolix/log"
	libtorrent "github.com/anacrolix/torrent"
	torrentstorage "github.com/anacrolix/torrent/storage"
)

const (
	saveInterval           = 3 * time.Second
	trackerRetryInterval   = 15 * time.Second
	progressUpdateInterval = time.Second
	waitingTimeout         = 25 * time.Second
	stalledRestartAfter    = 2 * time.Minute
	restartCooldown        = 4 * time.Minute
)

type EventType string

const (
	EventDownloadStarted  EventType = "download_started"
	EventDownloadProgress EventType = "download_progress"
	EventDownloadFinished EventType = "download_finished"
	EventError            EventType = "error"
)

type Event struct {
	Type    EventType
	TaskID  string
	Message string
}

type DownloadTask struct {
	ID             string
	Name           string
	Source         string
	Progress       int
	Status         string
	Paused         bool
	CompletedBytes int64
	TotalBytes     int64
	Speed          float64
	ETA            int
	Peers          int
	LastError      string
	Torrent        *libtorrent.Torrent `json:"-"`

	lastUsefulBytes int64     `json:"-"`
	lastTickAt      time.Time `json:"-"`
	lastSaveAt      time.Time `json:"-"`
	lastAnnounceAt  time.Time `json:"-"`
	lastTransferAt  time.Time `json:"-"`
	lastPeerAt      time.Time `json:"-"`
	lastRestartAt   time.Time `json:"-"`
}

type Client struct {
	DownloadPath string
	StatePath    string
	Events       chan Event
	Downloads    map[string]*DownloadTask

	store         downloadStore
	lastStoreErr  string
	mu            sync.Mutex
	storageCloser torrentstorage.ClientImplCloser
	torrentClient *libtorrent.Client
}

func NewClient() *Client {
	downloadPath := loadDownloadPath()
	statePath := defaultStatePath()
	store, storeWarn := newDownloadStore()

	_ = os.MkdirAll(downloadPath, 0o755)
	_ = os.MkdirAll(statePath, 0o755)
	_ = os.MkdirAll(stateSessionPath(statePath), 0o755)
	_ = os.MkdirAll(statePiecesPath(statePath), 0o755)
	relocateLegacyStateFiles(downloadPath, statePath)

	downloads, loadWarn := loadDownloads(store)
	prepareDownloads(downloads)

	client := &Client{
		DownloadPath: downloadPath,
		StatePath:    statePath,
		Events:       make(chan Event, 128),
		Downloads:    downloads,
		store:        store,
	}

	if storeWarn != "" {
		client.emit(Event{
			Type:    EventError,
			Message: storeWarn,
		})
	}
	if loadWarn != "" {
		client.emit(Event{
			Type:    EventError,
			Message: loadWarn,
		})
	}

	if err := client.initEngine(); err != nil {
		client.emit(Event{
			Type:    EventError,
			Message: "engine: " + err.Error(),
		})
		return client
	}

	return client
}

func loadDownloadPath() string {
	conf := make(map[string]string)
	_ = loadJSON("config.json", &conf)

	path := normalizePath(conf["download_path"])
	if path == "" || isLegacyDefaultDownloadPath(path) {
		path = defaultDownloadPath()
	}

	_ = saveJSON("config.json", map[string]string{
		"download_path": path,
	})

	return path
}

func (c *Client) initEngine() error {
	storageCloser := newTorrentStorage(c.DownloadPath, statePiecesPath(c.StatePath))

	cfg := newEngineConfig(c.StatePath, storageCloser)
	tc, err := libtorrent.NewClient(cfg)
	if err != nil {
		cfg.ListenPort = 0
		tc, err = libtorrent.NewClient(cfg)
	}
	if err != nil {
		_ = storageCloser.Close()
		return err
	}

	c.mu.Lock()
	c.closeEngineLocked()
	c.storageCloser = storageCloser
	c.torrentClient = tc
	c.mu.Unlock()

	return nil
}

func (c *Client) emit(event Event) {
	select {
	case c.Events <- event:
	default:
	}
}

func newEngineConfig(statePath string, storageCloser torrentstorage.ClientImplCloser) *libtorrent.ClientConfig {
	cfg := libtorrent.NewDefaultClientConfig()
	cfg.DataDir = stateSessionPath(statePath)
	cfg.DefaultStorage = storageCloser
	cfg.ListenPort = 42069
	cfg.Seed = false
	cfg.NoUpload = false
	cfg.NoDHT = false
	cfg.DisableUTP = true
	cfg.DisablePEX = false
	cfg.NoDefaultPortForwarding = true
	cfg.DisableIPv6 = true
	cfg.DisableWebtorrent = true
	cfg.DisableAcceptRateLimiting = true
	cfg.DialForPeerConns = true
	cfg.AcceptPeerConnections = true
	cfg.AlwaysWantConns = true
	cfg.DropDuplicatePeerIds = true
	// keep more peers
	cfg.NominalDialTimeout = 10 * time.Second
	cfg.MinDialTimeout = 2 * time.Second
	cfg.EstablishedConnsPerTorrent = 100
	cfg.HalfOpenConnsPerTorrent = 60
	cfg.TotalHalfOpenConns = 320
	cfg.TorrentPeersLowWater = 160
	cfg.TorrentPeersHighWater = 1200
	cfg.HandshakesTimeout = 6 * time.Second
	cfg.KeepAliveTimeout = 30 * time.Second
	cfg.PieceHashersPerTorrent = 4
	cfg.TrackerDialContext = trackerDialContext
	cfg.HTTPDialContext = trackerDialContext
	cfg.Logger = silentTorrentLogger()
	cfg.Slogger = slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg.ConfigureAnacrolixDhtServer = func(server *dht.ServerConfig) {
		server.NoSecurity = true
	}
	return cfg
}

func silentTorrentLogger() analog.Logger {
	logger := analog.NewLogger("torrent").WithFilterLevel(analog.Never)
	logger.SetHandlers()
	return logger
}

func (c *Client) closeEngineLocked() {
	if c.torrentClient != nil {
		c.torrentClient.Close()
		c.torrentClient = nil
	}
	if c.storageCloser != nil {
		_ = c.storageCloser.Close()
		c.storageCloser = nil
	}
}

func (c *Client) saveLocked() {
	snapshot := make(map[string]*DownloadTask, len(c.Downloads))
	for id, task := range c.Downloads {
		if task == nil {
			continue
		}

		copy := *task
		copy.Torrent = nil
		copy.lastUsefulBytes = 0
		copy.lastTickAt = time.Time{}
		copy.lastSaveAt = time.Time{}
		copy.lastAnnounceAt = time.Time{}
		copy.lastTransferAt = time.Time{}
		copy.lastPeerAt = time.Time{}
		copy.lastRestartAt = time.Time{}
		snapshot[id] = &copy
	}

	if c.store == nil {
		return
	}

	if err := c.store.SaveDownloads(snapshot); err != nil {
		if err.Error() != c.lastStoreErr {
			c.lastStoreErr = err.Error()
			c.emit(Event{
				Type:    EventError,
				Message: "state cache: " + err.Error(),
			})
		}
		return
	}

	c.lastStoreErr = ""
}

func (c *Client) generateID() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	for id := 1; ; id++ {
		key := strconv.Itoa(id)
		if _, ok := c.Downloads[key]; ok {
			continue
		}
		return key
	}
}

func (c *Client) findTaskBySourceLocked(source string) *DownloadTask {
	for _, task := range c.Downloads {
		if task != nil && task.Source == source {
			return task
		}
	}
	return nil
}

func (c *Client) StartQueued() {
	c.mu.Lock()
	queued := make([]*DownloadTask, 0, len(c.Downloads))
	for _, task := range c.Downloads {
		if task != nil && task.Status == "queued" {
			queued = append(queued, task)
		}
	}
	c.mu.Unlock()

	for _, task := range queued {
		go c.startTask(task)
	}
}

func (c *Client) startTask(task *DownloadTask) {
	c.mu.Lock()

	current, ok := c.Downloads[task.ID]
	if !ok || current != task || task.Status != "queued" {
		c.mu.Unlock()
		return
	}

	if c.torrentClient == nil {
		c.mu.Unlock()
		c.failTask(task.ID, "torrent engine is not ready")
		return
	}

	now := time.Now()
	task.Status = "connecting"
	task.Paused = false
	task.Speed = 0
	task.ETA = 0
	task.Peers = 0
	task.LastError = ""
	task.lastUsefulBytes = 0
	task.lastTickAt = now
	task.lastSaveAt = now
	task.lastAnnounceAt = time.Time{}
	task.lastTransferAt = time.Time{}
	task.lastPeerAt = time.Time{}
	c.saveLocked()

	tc := c.torrentClient
	c.mu.Unlock()

	t, name, err := addTorrentSource(tc, task.Source)
	if err != nil {
		c.failTask(task.ID, err.Error())
		return
	}

	c.mu.Lock()
	current, ok = c.Downloads[task.ID]
	if !ok || current != task || (task.Status != "connecting" && task.Status != "queued") {
		c.mu.Unlock()
		t.Drop()
		return
	}

	task.Torrent = t
	if strings.TrimSpace(name) != "" {
		task.Name = strings.TrimSpace(name)
	} else if strings.TrimSpace(t.Name()) != "" {
		task.Name = strings.TrimSpace(t.Name())
	}
	task.CompletedBytes = t.BytesCompleted()
	task.TotalBytes = torrentLength(t)
	task.Status = "metadata"
	task.lastUsefulBytes = 0
	task.lastTickAt = time.Now()
	task.lastSaveAt = time.Now()
	task.lastPeerAt = time.Time{}
	c.saveLocked()
	c.mu.Unlock()

	c.emit(Event{
		Type:    EventDownloadStarted,
		TaskID:  task.ID,
		Message: fmt.Sprintf("started %s", task.ID),
	})

	go c.runTask(task.ID, t)
}

func (c *Client) runTask(taskID string, t *libtorrent.Torrent) {
	if t == nil {
		return
	}

	applyEffectiveTrackers(t)

	select {
	case <-t.GotInfo():
	case <-t.Closed():
		return
	}

	c.prepareDownload(taskID, t)

	ticker := time.NewTicker(progressUpdateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-t.Closed():
			return
		case <-ticker.C:
			finished, restart := c.updateTask(taskID, t)
			if restart {
				t.Drop()
				go c.restartTask(taskID)
				return
			}
			if finished {
				t.Drop()
				c.emit(Event{
					Type:    EventDownloadFinished,
					TaskID:  taskID,
					Message: fmt.Sprintf("finished %s", taskID),
				})
				return
			}
		}
	}
}

func (c *Client) prepareDownload(taskID string, t *libtorrent.Torrent) {
	info := t.Info()
	if info == nil {
		return
	}

	c.mu.Lock()
	task := c.Downloads[taskID]
	c.mu.Unlock()
	recoverTaskData(c.DownloadPath, task, t)

	ensureTorrentLayout(c.DownloadPath, t)
	t.AllowDataDownload()
	t.DownloadAll()

	c.mu.Lock()
	defer c.mu.Unlock()

	task, ok := c.Downloads[taskID]
	if !ok || task.Torrent != t {
		return
	}

	task.Name = strings.TrimSpace(info.BestName())
	task.TotalBytes = info.TotalLength()
	task.Status = "downloading"
	task.lastSaveAt = time.Now()
	c.saveLocked()
}

func (c *Client) updateTask(taskID string, t *libtorrent.Torrent) (bool, bool) {
	now := time.Now()
	info := t.Info()
	stats := t.Stats()
	completed := t.BytesCompleted()
	total := torrentLength(t)
	peers := peerCount(t)
	usefulBytes := stats.BytesReadUsefulData.Int64()

	c.mu.Lock()
	defer c.mu.Unlock()

	task, ok := c.Downloads[taskID]
	if !ok || task.Torrent != t {
		return false, false
	}

	if info == nil {
		task.Status = "metadata"
		task.Peers = peers
		task.Speed = 0
		task.ETA = 0
		task.lastTickAt = now
		if now.Sub(task.lastSaveAt) >= saveInterval {
			task.lastSaveAt = now
			c.saveLocked()
		}

		c.emit(Event{
			Type:    EventDownloadProgress,
			TaskID:  taskID,
			Message: fmt.Sprintf("id:%s fetching metadata... peers:%d", taskID, peers),
		})
		return false, false
	}

	speed := task.Speed
	usefulDelta := usefulBytes - task.lastUsefulBytes
	if usefulDelta > 0 {
		task.lastTransferAt = now
	}
	if peers > 0 {
		task.lastPeerAt = now
	}
	if task.lastTickAt.IsZero() {
		task.lastTickAt = now
	}
	if usefulDelta > 0 || now.Sub(task.lastTickAt) >= 3*time.Second {
		sample := speedMBps(task.lastUsefulBytes, usefulBytes, task.lastTickAt, now)
		speed = smoothSpeed(task.Speed, sample, dataIdleDuration(task, now))
		task.lastUsefulBytes = usefulBytes
		task.lastTickAt = now
	}

	eta := etaSeconds(completed, total, speed)
	progress := percentComplete(completed, total)
	dataIdle := dataIdleDuration(task, now)
	peerIdle := peerIdleDuration(task, now)

	task.Name = strings.TrimSpace(info.BestName())
	task.Status = "downloading"
	if peers == 0 {
		task.Status = "waiting"
	}
	task.Paused = false
	task.Progress = progress
	task.CompletedBytes = completed
	task.TotalBytes = total
	task.Speed = speed
	task.ETA = eta
	task.Peers = peers

	// ask trackers again
	if (peers == 0 || dataIdle >= waitingTimeout) && now.Sub(task.lastAnnounceAt) >= trackerRetryInterval {
		applyEffectiveTrackers(t)
		t.DownloadAll()
		task.lastAnnounceAt = now
	}

	finished := total > 0 && completed >= total
	if finished {
		task.Progress = 100
		task.Status = "finished"
		task.Speed = 0
		task.ETA = 0
		task.Peers = peers
		task.Torrent = nil
		task.lastSaveAt = now
		c.saveLocked()
		return true, false
	}

	stalled := peers == 0 && peerIdle >= stalledRestartAfter
	if stalled && now.Sub(task.lastRestartAt) >= restartCooldown {
		task.Status = "queued"
		task.Speed = 0
		task.ETA = 0
		task.Peers = 0
		task.Torrent = nil
		task.lastRestartAt = now
		task.lastSaveAt = now
		c.saveLocked()

		c.emit(Event{
			Type:    EventDownloadProgress,
			TaskID:  taskID,
			Message: fmt.Sprintf("id:%s restarting idle torrent", taskID),
		})
		return false, true
	}

	if now.Sub(task.lastSaveAt) >= saveInterval || speed > 0 || peers == 0 {
		task.lastSaveAt = now
		c.saveLocked()
	}

	c.emit(Event{
		Type:    EventDownloadProgress,
		TaskID:  taskID,
		Message: formatProgressMessage(taskID, completed, total, peers, speed, eta),
	})

	return false, false
}

func (c *Client) failTask(taskID string, message string) {
	c.mu.Lock()
	task, ok := c.Downloads[taskID]
	if ok {
		task.Status = "error"
		task.Paused = true
		task.Speed = 0
		task.ETA = 0
		task.Peers = 0
		task.LastError = message
		task.Torrent = nil
		task.lastSaveAt = time.Now()
		c.saveLocked()
	}
	c.mu.Unlock()

	c.emit(Event{
		Type:    EventError,
		TaskID:  taskID,
		Message: fmt.Sprintf("id:%s %s", taskID, message),
	})
}

func percentComplete(completed int64, total int64) int {
	if total <= 0 {
		return 0
	}

	progress := int(float64(completed) * 100 / float64(total))
	if progress < 0 {
		return 0
	}
	if progress > 100 {
		return 100
	}
	return progress
}

func speedMBps(lastBytes int64, completed int64, lastTickAt time.Time, now time.Time) float64 {
	if lastTickAt.IsZero() {
		return 0
	}

	delta := completed - lastBytes
	if delta <= 0 {
		return 0
	}

	seconds := now.Sub(lastTickAt).Seconds()
	if seconds <= 0 {
		return 0
	}

	return float64(delta) / seconds / (1024 * 1024)
}

func smoothSpeed(previous float64, sample float64, idleFor time.Duration) float64 {
	if sample > 0 {
		if previous <= 0 {
			return sample
		}
		return previous*0.65 + sample*0.35
	}

	switch {
	case idleFor < 10*time.Second && previous > 0.05:
		return previous * 0.92
	case idleFor < 20*time.Second && previous > 0.05:
		return previous * 0.75
	case idleFor < 30*time.Second && previous > 0.05:
		return previous * 0.55
	default:
		return 0
	}
}

func dataIdleDuration(task *DownloadTask, now time.Time) time.Duration {
	if task == nil {
		return 0
	}
	if !task.lastTransferAt.IsZero() {
		return now.Sub(task.lastTransferAt)
	}
	if !task.lastTickAt.IsZero() {
		return now.Sub(task.lastTickAt)
	}
	return 0
}

func peerIdleDuration(task *DownloadTask, now time.Time) time.Duration {
	if task == nil {
		return 0
	}
	if !task.lastPeerAt.IsZero() {
		return now.Sub(task.lastPeerAt)
	}
	if !task.lastTickAt.IsZero() {
		return now.Sub(task.lastTickAt)
	}
	return 0
}

func etaSeconds(completed int64, total int64, speed float64) int {
	if total <= 0 || speed <= 0 || completed >= total {
		return 0
	}

	remaining := float64(total - completed)
	return int(remaining / (speed * 1024 * 1024))
}

func formatProgressMessage(taskID string, completed int64, total int64, peers int, speed float64, eta int) string {
	progressText := "0%"
	if total > 0 {
		percent := float64(completed) * 100 / float64(total)
		switch {
		case percent >= 1:
			progressText = fmt.Sprintf("%.0f%%", percent)
		case completed > 0:
			progressText = fmt.Sprintf("%.1f%%", percent)
		}
	}

	return fmt.Sprintf("id:%s %s peers:%d speed:%.2fMB/s ETA:%ds", taskID, progressText, peers, speed, eta)
}

func torrentLength(t *libtorrent.Torrent) int64 {
	if t == nil {
		return 0
	}

	info := t.Info()
	if info == nil {
		return 0
	}

	return info.TotalLength()
}

func (c *Client) addOrReuseTask(source string) string {
	c.mu.Lock()
	if existing := c.findTaskBySourceLocked(source); existing != nil {
		if existing.Status != "finished" {
			existing.Status = "queued"
			existing.Paused = false
			existing.Speed = 0
			existing.ETA = 0
			existing.Peers = 0
			existing.LastError = ""
			existing.lastSaveAt = time.Now()
			c.saveLocked()
		}

		id := existing.ID
		ready := c.torrentClient != nil
		c.mu.Unlock()

		if ready && existing.Status == "queued" {
			go c.startTask(existing)
		}
		return id
	}
	c.mu.Unlock()

	id := c.generateID()
	task := &DownloadTask{
		ID:     id,
		Source: source,
		Status: "queued",
	}

	c.mu.Lock()
	c.Downloads[id] = task
	ready := c.torrentClient != nil
	c.saveLocked()
	c.mu.Unlock()

	if ready {
		go c.startTask(task)
	}

	return id
}

func (c *Client) DownloadMagnet(link string) string {
	link = strings.Trim(strings.TrimSpace(link), "\"'`")
	if link == "" {
		return ""
	}

	return c.addOrReuseTask(link)
}

func (c *Client) DownloadTorrentFile(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}

	if resolved, err := resolveTorrentFilePath(path); err == nil {
		path = resolved
	} else {
		path = normalizePath(path)
	}

	return c.addOrReuseTask(path)
}

func (c *Client) StopDownload(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	task, ok := c.Downloads[id]
	if !ok {
		return
	}

	if task.Torrent != nil {
		task.Torrent.Drop()
		task.Torrent = nil
	}

	task.Status = "stopped"
	task.Paused = true
	task.Speed = 0
	task.ETA = 0
	task.Peers = 0
	task.lastSaveAt = time.Now()
	c.saveLocked()
}

func (c *Client) ResumeDownload(id string) {
	c.mu.Lock()

	task, ok := c.Downloads[id]
	if !ok || task.Status == "finished" || task.Status == "connecting" || task.Status == "metadata" || task.Status == "downloading" {
		c.mu.Unlock()
		return
	}

	if task.Torrent != nil {
		task.Torrent.Drop()
		task.Torrent = nil
	}

	task.Status = "queued"
	task.Paused = false
	task.Speed = 0
	task.ETA = 0
	task.Peers = 0
	task.LastError = ""
	task.lastSaveAt = time.Now()
	c.saveLocked()

	ready := c.torrentClient != nil
	c.mu.Unlock()

	if ready {
		go c.startTask(task)
	}
}

func (c *Client) DeleteDownload(id string) {
	c.mu.Lock()

	task, ok := c.Downloads[id]
	if !ok {
		c.mu.Unlock()
		return
	}

	downloadRoot := taskDownloadRoot(c.DownloadPath, task)
	if task.Torrent != nil {
		task.Torrent.Drop()
		task.Torrent = nil
	}

	delete(c.Downloads, id)
	c.saveLocked()
	c.mu.Unlock()

	if downloadRoot == "" {
		return
	}

	cleanRoot := filepath.Clean(downloadRoot)
	cleanBase := filepath.Clean(c.GetDownloadPath())
	if cleanRoot == cleanBase {
		return
	}

	prefix := strings.ToLower(cleanBase + string(os.PathSeparator))
	if !strings.HasPrefix(strings.ToLower(cleanRoot), prefix) {
		return
	}

	_ = os.RemoveAll(cleanRoot)
}

func (c *Client) SetDownloadPath(path string) {
	path = normalizePath(path)
	if path == "" {
		return
	}

	if err := os.MkdirAll(path, 0o755); err != nil {
		c.emit(Event{Type: EventError, Message: err.Error()})
		return
	}

	c.mu.Lock()
	oldPath := c.DownloadPath
	c.closeEngineLocked()
	migrateDownloadPath(oldPath, path, c.Downloads)

	c.DownloadPath = path
	for _, task := range c.Downloads {
		if task == nil {
			continue
		}

		task.Torrent = nil
		task.Speed = 0
		task.ETA = 0
		task.Peers = 0
		task.lastUsefulBytes = 0

		if task.Status == "finished" || task.Status == "stopped" || task.Status == "error" {
			continue
		}

		task.Status = "queued"
		task.Paused = false
	}
	c.saveLocked()
	c.mu.Unlock()

	_ = saveJSON("config.json", map[string]string{
		"download_path": path,
	})

	if err := c.initEngine(); err != nil {
		c.emit(Event{Type: EventError, Message: "engine: " + err.Error()})
		return
	}

	c.StartQueued()
}

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeEngineLocked()
	if c.store != nil {
		_ = c.store.Close()
	}
}

func (c *Client) restartTask(taskID string) {
	c.mu.Lock()
	task, ok := c.Downloads[taskID]
	if !ok || task == nil {
		c.mu.Unlock()
		return
	}

	task.Status = "queued"
	task.Paused = false
	task.Speed = 0
	task.ETA = 0
	task.Peers = 0
	task.LastError = ""
	task.lastUsefulBytes = 0
	task.lastTickAt = time.Now()
	task.lastSaveAt = time.Now()
	task.lastAnnounceAt = time.Time{}
	task.lastTransferAt = time.Time{}
	task.lastPeerAt = time.Time{}
	c.saveLocked()
	c.mu.Unlock()

	c.startTask(task)
}

func (c *Client) GetDownloads() map[string]*DownloadTask {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make(map[string]*DownloadTask, len(c.Downloads))
	for id, task := range c.Downloads {
		if task == nil {
			continue
		}

		copy := *task
		copy.Torrent = nil
		out[id] = &copy
	}

	return out
}

func (c *Client) GetDownload(id string) *DownloadTask {
	c.mu.Lock()
	defer c.mu.Unlock()

	task, ok := c.Downloads[id]
	if !ok || task == nil {
		return nil
	}

	copy := *task
	copy.Torrent = nil
	return &copy
}

func (c *Client) GetDownloadPath() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.DownloadPath
}

func (c *Client) HasActiveTasks() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, task := range c.Downloads {
		if task == nil {
			continue
		}

		switch task.Status {
		case "queued", "connecting", "metadata", "downloading", "waiting":
			return true
		}
	}

	return false
}

func SortedIDs(downloads map[string]*DownloadTask) []string {
	ids := make([]string, 0, len(downloads))
	for id := range downloads {
		ids = append(ids, id)
	}

	sort.Slice(ids, func(i, j int) bool {
		left, leftErr := strconv.Atoi(ids[i])
		right, rightErr := strconv.Atoi(ids[j])
		if leftErr == nil && rightErr == nil {
			return left < right
		}
		return ids[i] < ids[j]
	})

	return ids
}

func saveJSON(path string, data interface{}) error {
	path = appFilePath(path)
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, "tmp-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err == nil {
		return nil
	}
	_ = os.Remove(path)
	return os.Rename(tmpName, path)
}

func loadJSON(path string, data interface{}) error {
	path = appFilePath(path)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}

	return json.Unmarshal(raw, data)
}

func loadDownloads(store downloadStore) (map[string]*DownloadTask, string) {
	if store == nil {
		return make(map[string]*DownloadTask), ""
	}

	downloads, err := store.LoadDownloads()
	if err == nil {
		if len(downloads) > 0 {
			return downloads, ""
		}
	}

	legacy := make(map[string]*DownloadTask)
	_ = loadJSON("downloads.json", &legacy)
	if len(legacy) > 0 {
		if saveErr := store.SaveDownloads(legacy); saveErr == nil {
			if _, ok := store.(*redisDownloadStore); ok {
				_ = os.Remove(appFilePath("downloads.json"))
			}
		}
		return legacy, ""
	}

	if err != nil {
		return make(map[string]*DownloadTask), "state cache: " + err.Error()
	}

	return make(map[string]*DownloadTask), ""
}
