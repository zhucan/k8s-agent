package cluster

import (
	"context"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher monitors the kubeconfigs directory for changes and hot-reloads clusters.
type Watcher struct {
	dir        string
	configFile string
	manager    *Manager
	ctx        context.Context
}

// NewWatcher creates a new kubeconfigs directory watcher.
func NewWatcher(ctx context.Context, dir, configFile string, mgr *Manager) *Watcher {
	return &Watcher{
		dir:        dir,
		configFile: configFile,
		manager:    mgr,
		ctx:        ctx,
	}
}

// Start begins watching the kubeconfigs directory. Non-blocking (runs in a goroutine).
func (w *Watcher) Start() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	if err := watcher.Add(w.dir); err != nil {
		watcher.Close()
		return err
	}

	log.Printf("[cluster/watcher] watching %s for changes", w.dir)

	go w.loop(watcher)
	return nil
}

func (w *Watcher) loop(watcher *fsnotify.Watcher) {
	defer watcher.Close()

	// Debounce: accumulate events for 500ms before processing
	var debounceTimer *time.Timer
	pending := make(map[string]fsnotify.Op)

	for {
		select {
		case <-w.ctx.Done():
			log.Println("[cluster/watcher] stopped")
			return

		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if !isKubeconfigFile(event.Name) {
				continue
			}

			pending[event.Name] = event.Op

			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.AfterFunc(500*time.Millisecond, func() {
				w.processPending(pending)
				pending = make(map[string]fsnotify.Op)
			})

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("[cluster/watcher] error: %v", err)
		}
	}
}

func (w *Watcher) processPending(events map[string]fsnotify.Op) {
	for path, op := range events {
		name := clusterNameFromPath(path)
		if name == "" {
			continue
		}

		switch {
		case op&fsnotify.Remove != 0 || op&fsnotify.Rename != 0:
			w.handleRemove(name, path)
		case op&fsnotify.Create != 0:
			w.handleCreate(name, path)
		case op&fsnotify.Write != 0:
			w.handleModify(name, path)
		}
	}
}

func (w *Watcher) handleCreate(name, path string) {
	log.Printf("[cluster/watcher] detected new kubeconfig: %s", path)

	contextName, err := extractContext(path)
	if err != nil {
		log.Printf("[cluster/watcher] skip %s: %v", path, err)
		return
	}

	// Update clusters.json
	cfg, _ := LoadConfig(w.configFile)
	if cfg == nil {
		cfg = &Config{Clusters: []ClusterConfig{}}
	}
	cfg.Clusters = append(cfg.Clusters, ClusterConfig{
		Name:       name,
		Context:    contextName,
		Kubeconfig: path,
	})
	if err := cfg.SaveConfig(w.configFile); err != nil {
		log.Printf("[cluster/watcher] failed to save config: %v", err)
	}

	// Hot-load into manager
	if err := w.manager.AddCluster(w.ctx, name, path, contextName); err != nil {
		log.Printf("[cluster/watcher] failed to load cluster %q: %v", name, err)
	} else {
		log.Printf("[cluster/watcher] cluster %q loaded successfully", name)
	}
}

func (w *Watcher) handleRemove(name, path string) {
	log.Printf("[cluster/watcher] detected removed kubeconfig: %s", path)

	// Remove from manager
	if err := w.manager.RemoveCluster(name); err != nil {
		log.Printf("[cluster/watcher] failed to remove cluster %q: %v", name, err)
	} else {
		log.Printf("[cluster/watcher] cluster %q unloaded", name)
	}

	// Update clusters.json
	cfg, _ := LoadConfig(w.configFile)
	if cfg == nil {
		return
	}
	var kept []ClusterConfig
	for _, c := range cfg.Clusters {
		if c.Name != name {
			kept = append(kept, c)
		}
	}
	cfg.Clusters = kept
	if err := cfg.SaveConfig(w.configFile); err != nil {
		log.Printf("[cluster/watcher] failed to save config: %v", err)
	}
}

func (w *Watcher) handleModify(name, path string) {
	log.Printf("[cluster/watcher] detected modified kubeconfig: %s", path)

	// Remove old cluster and re-add
	_ = w.manager.RemoveCluster(name)

	contextName, err := extractContext(path)
	if err != nil {
		log.Printf("[cluster/watcher] skip reload %s: %v", path, err)
		return
	}

	// Update context in clusters.json (may have changed)
	cfg, _ := LoadConfig(w.configFile)
	if cfg != nil {
		for i, c := range cfg.Clusters {
			if c.Name == name {
				cfg.Clusters[i].Context = contextName
				break
			}
		}
		_ = cfg.SaveConfig(w.configFile)
	}

	if err := w.manager.AddCluster(w.ctx, name, path, contextName); err != nil {
		log.Printf("[cluster/watcher] failed to reload cluster %q: %v", name, err)
	} else {
		log.Printf("[cluster/watcher] cluster %q reloaded", name)
	}
}

func isKubeconfigFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml"
}

func clusterNameFromPath(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}
