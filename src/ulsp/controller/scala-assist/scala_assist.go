package scalaassist

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/uber/scip-lsp/src/ulsp/entity"
	ulspplugin "github.com/uber/scip-lsp/src/ulsp/entity/ulsp-plugin"
	ideclient "github.com/uber/scip-lsp/src/ulsp/gateway/ide-client"
	"github.com/uber/scip-lsp/src/ulsp/repository/session"
	"go.lsp.dev/protocol"
	"go.uber.org/config"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

const (
	_nameKey = "scala-assist"

	_bspDir        = ".bsp"
	_bspSourceName = "bazelbsp.json"
	_bspDestName   = "bazelbsp_scala.json"

	// Keep version set to the hard coded value in Scala Metals.
	// This ensures Metals will not try to manage the installed BSP server version.
	_versionOverride = "3.2.0-20240629-e3d8bdf-NIGHTLY"
)

// Controller is the interface for the scala-assist plugin.
type Controller interface {
	StartupInfo(ctx context.Context) (ulspplugin.PluginInfo, error)
}

type controller struct {
	logger     *zap.SugaredLogger
	ideGateway ideclient.Gateway
	sessions   session.Repository

	watcher           *fsnotify.Watcher
	watchedWorkspaces map[string]struct{}
	configs           entity.MonorepoConfigs
	mu                sync.RWMutex
	wg                sync.WaitGroup
}

// Params are the parameters to set up this controller.
type Params struct {
	fx.In

	Logger     *zap.SugaredLogger
	IdeGateway ideclient.Gateway
	Sessions   session.Repository
	Config     config.Provider
}

// New creates a new scala-assist controller.
func New(p Params) Controller {
	c := &controller{
		ideGateway: p.IdeGateway,
		logger:     p.Logger,
		sessions:   p.Sessions,

		watchedWorkspaces: make(map[string]struct{}),
	}

	return c
}

// StartupInfo returns PluginInfo for this controller.
func (c *controller) StartupInfo(ctx context.Context) (ulspplugin.PluginInfo, error) {
	// Set a priority for each method that this plugin provides.
	priorities := map[string]ulspplugin.Priority{
		protocol.MethodInitialized: ulspplugin.PriorityAsync,
	}

	// Assign method keys to implementations.
	methods := &ulspplugin.Methods{
		PluginNameKey: _nameKey,

		Initialized: c.initialized,
	}

	return ulspplugin.PluginInfo{
		Priorities:    priorities,
		Methods:       methods,
		NameKey:       _nameKey,
		RelevantRepos: c.configs.RelevantScalaRepos(),
	}, nil
}

// initialized ensures that a watcher exists for the bsp config file in this workspace.
func (c *controller) initialized(ctx context.Context, params *protocol.InitializedParams) error {
	err := c.ensureWatcher(ctx)
	if err != nil {
		return err
	}

	s, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return err
	}

	if err := c.beginWatchingBspDir(ctx, s.WorkspaceRoot); err != nil {
		return err
	}

	return nil
}

// beginWatchingBspDir begins tracking a given path for changes to the bsp config file.
func (c *controller) beginWatchingBspDir(ctx context.Context, workspaceRoot string) error {
	if c.watcher == nil {
		return fmt.Errorf("watcher not initialized")
	}

	// Add the BSP source file to the watcher
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.watchedWorkspaces[workspaceRoot]; ok {
		// Already watching
		return nil
	}

	// Watch the .bsp directory for potential changes.
	bspDir := path.Join(workspaceRoot, _bspDir)
	if err := os.MkdirAll(bspDir, 0755); err != nil {
		return fmt.Errorf("create BSP directory %s: %w", bspDir, err)
	}

	if err := c.watcher.Add(bspDir); err != nil {
		return fmt.Errorf("watch BSP directory %s: %w", bspDir, err)
	}
	c.watchedWorkspaces[workspaceRoot] = struct{}{}

	// Handle initial update.
	if err := c.updateBSPJson(ctx, workspaceRoot); err != nil {
		c.logger.Warnf("failed to update BSP JSON on initial setup: %v", err)
	}

	return nil
}

// ensureWatcher creates a watcher and starts processing events.
func (c *controller) ensureWatcher(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.watcher != nil {
		// Already watching, no action needed.
		return nil
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create file system watcher: %w", err)
	}
	c.watcher = watcher

	go c.watchBSPChanges(context.Background())
	return nil
}

// watchBSPChanges monitors file system events and updates BSP JSON when any watched file changes
func (c *controller) watchBSPChanges(ctx context.Context) {
	c.wg.Add(1)
	defer c.wg.Done()

	if c.watcher == nil {
		return
	}

	for {
		select {
		case event, ok := <-c.watcher.Events:
			if !ok {
				return
			}

			c.consumeWatcherEvent(ctx, event)
		case err, ok := <-c.watcher.Errors:
			if !ok {
				return
			}
			c.logger.Errorf("bsp config file watcher error: %v", err)
		case <-ctx.Done():
			return
		}
	}
}

func (c *controller) consumeWatcherEvent(ctx context.Context, event fsnotify.Event) {
	if event.Op&fsnotify.Write != fsnotify.Write {
		return
	}

	// Find the workspace root that corresponds to the event.
	c.mu.RLock()
	workspaceRoot := ""
	for root := range c.watchedWorkspaces {
		if strings.HasPrefix(event.Name, root) {
			workspaceRoot = root
			break
		}
	}
	c.mu.RUnlock()

	if event.Name != c.sourcePath(workspaceRoot) {
		return
	}

	if workspaceRoot != "" {
		if err := c.updateBSPJson(ctx, workspaceRoot); err != nil {
			c.logger.Errorf("failed to update BSP config: %v", err)
		}
	}
}

func (c *controller) sourcePath(workspaceRoot string) string {
	return path.Join(workspaceRoot, _bspDir, _bspSourceName)
}

func (c *controller) destPath(workspaceRoot string) string {
	return path.Join(workspaceRoot, _bspDir, _bspDestName)
}

// updateBSPJson creates a copy of the bsp config file, with version overridden.
func (c *controller) updateBSPJson(ctx context.Context, workspaceRoot string) error {
	bspSourcePath := c.sourcePath(workspaceRoot)
	bspDestPath := c.destPath(workspaceRoot)

	if _, err := os.Stat(bspSourcePath); os.IsNotExist(err) {
		c.logger.Warnf("no bsp server config file found at %s, skipping", bspSourcePath)
		return nil
	}

	// Read the source JSON file
	sourceData, err := os.ReadFile(bspSourcePath)
	if err != nil {
		return err
	}

	// Parse the JSON into a map and apply the version override.
	var bspConfig map[string]interface{}
	if err := json.Unmarshal(sourceData, &bspConfig); err != nil {
		return err
	}
	bspConfig["version"] = _versionOverride

	// Output the modified JSON file to the destination path.
	modifiedData, err := json.MarshalIndent(bspConfig, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(bspDestPath, modifiedData, 0644); err != nil {
		return err
	}

	return nil
}
