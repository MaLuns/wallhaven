package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

func transferFixture() domain.Transfer {
	return domain.Transfer{Kind: domain.DownloadTransfer, Wallpaper: favoriteFixture(), Paused: true, Received: 64, Total: 128, Directory: filepath.Join(os.TempDir(), "downloads"), Path: filepath.Join(os.TempDir(), "original.png"), CreatedAt: time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)}
}

func TestTransfersReloadPrivateHistoryAndNeverDeleteOriginals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "transfers.json")
	store := NewTransfers(path)
	if got, err := store.Load(context.Background()); err != nil || len(got) != 0 {
		t.Fatalf("missing file: %v %v", got, err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read created the history directory")
	}
	want := []domain.Transfer{transferFixture()}
	original := filepath.Join(t.TempDir(), "keep.png")
	if err := os.WriteFile(original, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	want[0].Path = original
	if err := store.Save(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, err := NewTransfers(path).Load(context.Background())
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("reload=%+v err=%v", got, err)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(path)
		dir, _ := os.Stat(filepath.Dir(path))
		if info.Mode().Perm() != 0600 || dir.Mode().Perm() != 0700 {
			t.Fatal("history is not private")
		}
	}
	if err := store.Save(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(original); err != nil || string(data) != "original" {
		t.Fatal("clearing history modified the original")
	}
}

func TestTransfersProtectMalformedHistoryFromReplacement(t *testing.T) {
	for name, data := range map[string]string{
		"broken": `{"version":1,"transfers":[`, "new-version": `{"version":2,"transfers":[]}`, "missing": `{"version":1}`, "null": `{"version":1,"transfers":null}`, "unknown": `{"version":1,"transfers":[],"future":true}`, "trailing": `{"version":1,"transfers":[]} {}`, "invalid-kind": `{"version":1,"transfers":[{"Kind":"other","Wallpaper":{"ID":"a"}}]}`, "invalid-id": `{"version":1,"transfers":[{"Kind":"download","Wallpaper":{"ID":" "}}]}`, "negative": `{"version":1,"transfers":[{"Kind":"download","Wallpaper":{"ID":"a"},"Received":-1}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "history.json")
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			s := NewTransfers(path)
			if _, err := s.Load(context.Background()); err == nil {
				t.Fatal("malformed history accepted")
			}
			if err := s.Save(context.Background(), nil); err == nil {
				t.Fatal("malformed history replaced")
			}
			got, _ := os.ReadFile(path)
			if string(got) != data {
				t.Fatal("history changed")
			}
		})
	}
}

type cancelStagedTransfer struct {
	context.Context
	cancel    context.CancelFunc
	directory string
	observed  bool
}

func (c *cancelStagedTransfer) Err() error {
	entries, _ := os.ReadDir(c.directory)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".transfers-") {
			c.observed = true
			c.cancel()
		}
	}
	return c.Context.Err()
}

func TestTransfersRejectInvalidInputAndCancelBeforeCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	s := NewTransfers(path)
	ctx := context.Background()
	if err := s.Save(ctx, []domain.Transfer{transferFixture()}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	for _, change := range []func(*domain.Transfer){func(v *domain.Transfer) { v.Received = -1 }, func(v *domain.Transfer) { v.BytesPerSecond = math.NaN() }, func(v *domain.Transfer) { v.Directory = "relative" }, func(v *domain.Transfer) { v.Path = "/bad\x00path" }, func(v *domain.Transfer) { v.Error = strings.Repeat("a", maxTransfersSize+1) }} {
		v := transferFixture()
		change(&v)
		if err := s.Save(ctx, []domain.Transfer{v}); err == nil {
			t.Fatal("invalid history accepted")
		}
	}
	v := transferFixture()
	if err := s.Save(ctx, []domain.Transfer{v, v}); err == nil {
		t.Fatal("duplicate history accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Load(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	stageCtx, stageCancel := context.WithCancel(ctx)
	defer stageCancel()
	stage := &cancelStagedTransfer{Context: stageCtx, cancel: stageCancel, directory: filepath.Dir(path)}
	if err := s.Save(stage, nil); !errors.Is(err, context.Canceled) || !stage.observed {
		t.Fatalf("staged cancel=%v observed=%v", err, stage.observed)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("failed/canceled save replaced history")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatal("temporary history leaked")
	}
}

func TestTransfersBoundDecodedCountAndFileSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	s := NewTransfers(path)
	items := make([]domain.Transfer, maxTransfers+1)
	for i := range items {
		items[i] = transferFixture()
		items[i].Wallpaper.ID = fmt.Sprint(i)
	}
	if err := s.Save(context.Background(), items); err == nil {
		t.Fatal("excess records accepted")
	}
	data, err := json.Marshal(transfersFile{Version: 1, Transfers: items})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(context.Background()); err == nil {
		t.Fatal("excess decoded records accepted")
	}
	if err := s.Save(context.Background(), nil); err == nil {
		t.Fatal("excess records overwritten")
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxTransfersSize + 1); err != nil {
		t.Fatal(err)
	}
	file.Close()
	if _, err := s.Load(context.Background()); err == nil {
		t.Fatal("oversized file accepted")
	}
	if err := s.Save(context.Background(), nil); err == nil {
		t.Fatal("oversized file overwritten")
	}
}

func TestTransfersWriteFailureKeepsHistoryAndConcurrentReadsStayWhole(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "history.json")
	s := NewTransfers(path)
	if err := s.Save(context.Background(), []domain.Transfer{transferFixture()}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		if err := os.Chmod(directory, 0500); err != nil {
			t.Fatal(err)
		}
		err := s.Save(context.Background(), nil)
		os.Chmod(directory, 0700)
		if err == nil {
			t.Fatal("save to read-only directory succeeded")
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Fatal("failed replacement changed history")
		}
	}
	var workers sync.WaitGroup
	for index := range 6 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			value := transferFixture()
			value.Wallpaper.ID = fmt.Sprint(index)
			if err := s.Save(context.Background(), []domain.Transfer{value}); err != nil {
				t.Error(err)
			}
			if got, err := s.Load(context.Background()); err != nil || len(got) != 1 {
				t.Errorf("incomplete concurrent snapshot: %v %v", got, err)
			}
		}()
	}
	workers.Wait()
}
