package control

import (
	"strings"
	"testing"

	"github.com/rarnen-tech/go-torrent-cli/internal/torrent"
)

func TestRequestFromArgs(t *testing.T) {
	tests := []struct {
		args []string
		kind string
		arg  string
		ok   bool
	}{
		{args: []string{"movie.torrent"}, kind: "add_torrent", arg: "movie.torrent", ok: true},
		{args: []string{"magnet:?xt=urn:btih:test"}, kind: "add_magnet", arg: "magnet:?xt=urn:btih:test", ok: true},
		{args: []string{"list"}, kind: "list", ok: true},
		{args: []string{"status", "2"}, kind: "status", arg: "2", ok: true},
		{args: []string{"oops"}, ok: false},
	}

	for _, tc := range tests {
		req, ok := RequestFromArgs(tc.args)
		if ok != tc.ok {
			t.Fatalf("args %v: ok = %v, want %v", tc.args, ok, tc.ok)
		}
		if !ok {
			continue
		}
		if req.Kind != tc.kind || req.Arg != tc.arg {
			t.Fatalf("args %v: got %+v", tc.args, req)
		}
	}
}

func TestAddressForWorkspaceIsStable(t *testing.T) {
	left := addressForWorkspace(`C:\work\one`)
	right := addressForWorkspace(`C:\work\one`)
	other := addressForWorkspace(`C:\work\two`)

	if left != right {
		t.Fatalf("same workspace returned different address: %q vs %q", left, right)
	}
	if left == other {
		t.Fatalf("different workspaces returned same address: %q", left)
	}
}

func TestFormatListAndStatus(t *testing.T) {
	client := &torrent.Client{
		Downloads: map[string]*torrent.DownloadTask{
			"2": {
				ID:       "2",
				Name:     "Chikatilo",
				Source:   "two.torrent",
				Status:   "queued",
				Progress: 7,
			},
			"1": {
				ID:       "1",
				Name:     "Detroit",
				Source:   "one.torrent",
				Status:   "finished",
				Progress: 100,
			},
		},
	}

	list := formatList(client)
	if !strings.Contains(list, "ID:1 | 100% | finished | one.torrent") {
		t.Fatalf("list is missing first task: %s", list)
	}
	if !strings.Contains(list, "ID:2 | 7% | queued | two.torrent") {
		t.Fatalf("list is missing second task: %s", list)
	}
	if strings.Index(list, "ID:1") > strings.Index(list, "ID:2") {
		t.Fatalf("list is not sorted: %s", list)
	}

	status := formatStatus(client, "2")
	if status != "ID: 2 Progress: 7% Status: queued" {
		t.Fatalf("status = %q", status)
	}
}
