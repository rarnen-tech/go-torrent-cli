package torrent

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/logging"
)

type downloadStore interface {
	LoadDownloads() (map[string]*DownloadTask, error)
	SaveDownloads(map[string]*DownloadTask) error
	Close() error
}

type redisDownloadStore struct {
	client *redis.Client
	ctx    context.Context
	key    string
}

type memoryDownloadStore struct {
	mu   sync.Mutex
	data map[string]*DownloadTask
}

func newDownloadStore() (downloadStore, string) {
	redis.SetLogger(&logging.VoidLogger{})

	addr := strings.TrimSpace(os.Getenv("TORRENT_REDIS_ADDR"))
	if addr == "" {
		addr = "127.0.0.1:6379"
	}

	password := os.Getenv("TORRENT_REDIS_PASSWORD")
	prefix := strings.TrimSpace(os.Getenv("TORRENT_REDIS_PREFIX"))
	if prefix == "" {
		prefix = "go-torrent-cli"
	}

	db := 0
	if raw := strings.TrimSpace(os.Getenv("TORRENT_REDIS_DB")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value >= 0 {
			db = value
		}
	}

	client := redis.NewClient(&redis.Options{
		Addr:          addr,
		Password:      password,
		DB:            db,
		MaxRetries:    0,
		DialTimeout:   2 * time.Second,
		DialerRetries: 1,
		ReadTimeout:   2 * time.Second,
		WriteTimeout:  2 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return newMemoryDownloadStore(), "state cache: redis is not ready, app will keep state only in memory"
	}

	return &redisDownloadStore{
		client: client,
		ctx:    context.Background(),
		key:    prefix + ":downloads",
	}, ""
}

func newMemoryDownloadStore() downloadStore {
	return &memoryDownloadStore{
		data: make(map[string]*DownloadTask),
	}
}

func (s *redisDownloadStore) LoadDownloads() (map[string]*DownloadTask, error) {
	raw, err := s.client.Get(s.ctx, s.key).Bytes()
	if err == redis.Nil {
		return make(map[string]*DownloadTask), nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return make(map[string]*DownloadTask), nil
	}

	out := make(map[string]*DownloadTask)
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *redisDownloadStore) SaveDownloads(downloads map[string]*DownloadTask) error {
	raw, err := json.MarshalIndent(downloads, "", "  ")
	if err != nil {
		return err
	}
	return s.client.Set(s.ctx, s.key, raw, 0).Err()
}

func (s *redisDownloadStore) Close() error {
	return s.client.Close()
}

func (s *memoryDownloadStore) LoadDownloads() (map[string]*DownloadTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]*DownloadTask, len(s.data))
	for id, task := range s.data {
		if task == nil {
			continue
		}
		copy := *task
		out[id] = &copy
	}
	return out, nil
}

func (s *memoryDownloadStore) SaveDownloads(downloads map[string]*DownloadTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data = make(map[string]*DownloadTask, len(downloads))
	for id, task := range downloads {
		if task == nil {
			continue
		}
		copy := *task
		s.data[id] = &copy
	}
	return nil
}

func (s *memoryDownloadStore) Close() error {
	return nil
}
