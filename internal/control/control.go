package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/rarnen-tech/go-torrent-cli/internal/torrent"
)

const (
	controlHost = "127.0.0.1"
	portBase    = 41000
	portRange   = 8000
)

type Request struct {
	Kind string `json:"kind"`
	Arg  string `json:"arg,omitempty"`
}

type Response struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
	TaskID  string `json:"task_id,omitempty"`
}

func RequestFromArgs(args []string) (Request, bool) {
	if len(args) == 0 {
		return Request{}, false
	}

	first := strings.TrimSpace(args[0])
	lower := strings.ToLower(first)

	switch {
	case strings.HasSuffix(lower, ".torrent"):
		return Request{Kind: "add_torrent", Arg: first}, true
	case strings.HasPrefix(lower, "magnet:"):
		return Request{Kind: "add_magnet", Arg: first}, true
	}

	switch lower {
	case "magnet":
		if len(args) < 2 {
			return Request{Kind: "add_magnet"}, true
		}
		return Request{Kind: "add_magnet", Arg: args[1]}, true
	case "list":
		return Request{Kind: "list"}, true
	case "status", "stop", "resume", "delete", "path":
		arg := ""
		if len(args) > 1 {
			arg = args[1]
		}
		return Request{Kind: lower, Arg: arg}, true
	default:
		return Request{}, false
	}
}

func Start(workspace string, client *torrent.Client) (*http.Server, error) {
	listener, err := net.Listen("tcp", addressForWorkspace(workspace))
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/control", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		resp := handle(client, req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		_ = server.Serve(listener)
	}()

	return server, nil
}

func TrySend(workspace string, req Request) (Response, bool, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return Response{}, false, err
	}

	httpClient := &http.Client{Timeout: 2 * time.Second}
	resp, err := httpClient.Post("http://"+addressForWorkspace(workspace)+"/control", "application/json", bytes.NewReader(body))
	if err != nil {
		return Response{}, false, nil
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, true, err
	}

	var out Response
	if err := json.Unmarshal(raw, &out); err != nil {
		return Response{}, true, err
	}
	return out, true, nil
}

func addressForWorkspace(workspace string) string {
	normalized := strings.ToLower(filepath.Clean(strings.TrimSpace(workspace)))
	h := fnv.New32a()
	_, _ = h.Write([]byte(normalized))
	port := portBase + int(h.Sum32()%portRange)
	return fmt.Sprintf("%s:%d", controlHost, port)
}

func handle(client *torrent.Client, req Request) Response {
	switch req.Kind {
	case "add_torrent":
		if strings.TrimSpace(req.Arg) == "" {
			return Response{OK: false, Message: "torrent file path is required"}
		}
		id := client.DownloadTorrentFile(req.Arg)
		return Response{OK: id != "", TaskID: id, Message: "queued task " + id}
	case "add_magnet":
		if strings.TrimSpace(req.Arg) == "" {
			return Response{OK: false, Message: "magnet link is required"}
		}
		id := client.DownloadMagnet(req.Arg)
		return Response{OK: id != "", TaskID: id, Message: "queued task " + id}
	case "list":
		return Response{OK: true, Message: formatList(client)}
	case "status":
		return Response{OK: true, Message: formatStatus(client, req.Arg)}
	case "stop":
		if strings.TrimSpace(req.Arg) == "" {
			return Response{OK: false, Message: "download id required"}
		}
		client.StopDownload(req.Arg)
		return Response{OK: true, Message: "stopped task " + req.Arg}
	case "resume":
		if strings.TrimSpace(req.Arg) == "" {
			return Response{OK: false, Message: "download id required"}
		}
		client.ResumeDownload(req.Arg)
		return Response{OK: true, Message: "resumed task " + req.Arg}
	case "delete":
		if strings.TrimSpace(req.Arg) == "" {
			return Response{OK: false, Message: "download id required"}
		}
		client.DeleteDownload(req.Arg)
		return Response{OK: true, Message: "deleted task " + req.Arg}
	case "path":
		if strings.TrimSpace(req.Arg) == "" {
			return Response{OK: false, Message: "save path is required"}
		}
		client.SetDownloadPath(req.Arg)
		return Response{OK: true, Message: "save path updated"}
	default:
		return Response{OK: false, Message: "unknown command: " + req.Kind}
	}
}

func HandleLocal(client *torrent.Client, req Request) (wait bool, taskID string, message string) {
	switch req.Kind {
	case "add_torrent":
		if strings.TrimSpace(req.Arg) == "" {
			return false, "", "torrent file path is required"
		}
		id := client.DownloadTorrentFile(req.Arg)
		return true, id, ""
	case "add_magnet":
		if strings.TrimSpace(req.Arg) == "" {
			return false, "", "magnet link is required"
		}
		id := client.DownloadMagnet(req.Arg)
		return true, id, ""
	case "list":
		return false, "", formatList(client)
	case "status":
		return false, "", formatStatus(client, req.Arg)
	case "stop":
		if strings.TrimSpace(req.Arg) == "" {
			return false, "", "download id required"
		}
		client.StopDownload(req.Arg)
		return false, "", "stopped task " + req.Arg
	case "resume":
		if strings.TrimSpace(req.Arg) == "" {
			return false, "", "download id required"
		}
		client.ResumeDownload(req.Arg)
		return true, req.Arg, ""
	case "delete":
		if strings.TrimSpace(req.Arg) == "" {
			return false, "", "download id required"
		}
		client.DeleteDownload(req.Arg)
		return false, "", "deleted task " + req.Arg
	case "path":
		if strings.TrimSpace(req.Arg) == "" {
			return false, "", "save path is required"
		}
		client.SetDownloadPath(req.Arg)
		return false, "", "save path updated"
	default:
		return false, "", "unknown command: " + req.Kind
	}
}

func IsDoneEvent(event torrent.Event, taskID string) bool {
	if taskID != "" && event.TaskID != "" && event.TaskID != taskID {
		return false
	}
	return event.Type == torrent.EventDownloadFinished || event.Type == torrent.EventError
}

func formatList(client *torrent.Client) string {
	downloads := client.GetDownloads()
	if len(downloads) == 0 {
		return "no downloads"
	}

	lines := make([]string, 0, len(downloads))
	for _, id := range torrentIDs(downloads) {
		task := downloads[id]
		if task == nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("ID:%s | %d%% | %s | %s", id, task.Progress, task.Status, task.Source))
	}
	return strings.Join(lines, "\n")
}

func formatStatus(client *torrent.Client, id string) string {
	if strings.TrimSpace(id) == "" {
		return "download id required"
	}

	task := client.GetDownload(id)
	if task == nil {
		return "download not found"
	}

	return fmt.Sprintf("ID: %s Progress: %d%% Status: %s", task.ID, task.Progress, task.Status)
}

func torrentIDs(downloads map[string]*torrent.DownloadTask) []string {
	return torrent.SortedIDs(downloads)
}
