package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rarnen-tech/go-torrent-cli/internal/control"
	"github.com/rarnen-tech/go-torrent-cli/internal/torrent"
	"github.com/rarnen-tech/go-torrent-cli/internal/ui"
)

func main() {
	args := os.Args[1:]
	workspace := torrent.AppRoot()

	if req, ok := control.RequestFromArgs(args); ok {
		if resp, sent, err := control.TrySend(workspace, req); sent {
			if err != nil {
				fmt.Println(err)
				return
			}
			if strings.TrimSpace(resp.Message) != "" {
				fmt.Println(resp.Message)
			}
			return
		}
	}

	client := torrent.NewClient()
	server, err := control.Start(workspace, client)
	if err != nil {
		fmt.Println("control:", err)
		return
	}
	defer server.Close()

	if len(args) == 0 {
		client.StartQueued()
		ui.RunTUI(client)
		return
	}

	req, ok := control.RequestFromArgs(args)
	if !ok {
		fmt.Println("unknown command:", args[0])
		return
	}

	wait, taskID, message := control.HandleLocal(client, req)
	if strings.TrimSpace(message) != "" {
		fmt.Println(message)
	}
	if wait {
		client.StartQueued()
		runForeground(client, client.Events, taskID)
	}
}

type activeTaskChecker interface {
	HasActiveTasks() bool
}

func runForeground(client activeTaskChecker, ch <-chan torrent.Event, taskID string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	targetDone := taskID == ""

	for {
		select {
		case event, ok := <-ch:
			if !ok {
				return
			}

			if taskID != "" && event.TaskID != "" && event.TaskID != taskID {
				continue
			}

			if strings.TrimSpace(event.Message) != "" && !(taskID != "" && event.TaskID == "") {
				fmt.Println(event.Message)
			}

			if control.IsDoneEvent(event, taskID) {
				targetDone = true
			}
		case <-ticker.C:
			if targetDone && !client.HasActiveTasks() {
				return
			}
		}
	}
}
