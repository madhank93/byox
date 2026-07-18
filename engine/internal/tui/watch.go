package tui

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fsnotify/fsnotify"
)

type fileChangedMsg struct{}

// watchRelevant reports whether a changed path should trigger a test run.
func watchRelevant(path string) bool {
	switch filepath.Ext(path) {
	case ".go", ".sh", ".yml", ".yaml", ".mod":
		return !strings.HasPrefix(filepath.Base(path), ".")
	}
	return false
}

// startWatcher watches dir (recursively) and pushes a debounced
// fileChangedMsg into ch on relevant changes. The returned watcher must
// be Closed when the watched course changes; ch is shared for the app's
// lifetime so listeners stay valid across watcher swaps.
func startWatcher(dir string, ch chan tea.Msg) (*fsnotify.Watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() && !strings.HasPrefix(d.Name(), ".") {
			_ = w.Add(path)
		}
		return nil
	})
	go func() {
		var timer *time.Timer
		for {
			select {
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if !watchRelevant(ev.Name) {
					continue
				}
				if timer != nil {
					timer.Stop()
				}
				timer = time.AfterFunc(400*time.Millisecond, func() {
					ch <- fileChangedMsg{}
				})
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
			}
		}
	}()
	return w, nil
}

func listenWatch(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}
