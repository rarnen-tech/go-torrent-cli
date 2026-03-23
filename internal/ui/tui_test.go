package ui

import (
	"strings"
	"testing"

	"github.com/rarnen-tech/go-torrent-cli/internal/torrent"
)

func TestCleanTextRemovesBinaryTail(t *testing.T) {
	got := cleanText("hello\tworld\\x00trash")
	if got != "hello world" {
		t.Fatalf("clean text = %q", got)
	}
}

func TestRenderProgressBarKeepsSize(t *testing.T) {
	got := renderProgressBar(50, 10)
	if got != "=====....." {
		t.Fatalf("bar = %q", got)
	}
}

func TestProgressCellShowsOneDecimalBelowTen(t *testing.T) {
	got := progressCell(0.8, 16)
	if !strings.Contains(got, "0.8%") {
		t.Fatalf("cell = %q", got)
	}
}

func TestTaskStateLabelUsesPeerState(t *testing.T) {
	task := &torrent.DownloadTask{
		Status: "waiting",
		Peers:  3,
	}

	if got := taskStateLabel(task); got != "peers" {
		t.Fatalf("state = %q", got)
	}
}
