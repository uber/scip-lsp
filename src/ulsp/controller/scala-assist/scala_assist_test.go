package scalaassist

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"testing"

	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uber/scip-lsp/src/ulsp/entity"
	"github.com/uber/scip-lsp/src/ulsp/repository/session/repositorymock"
	"go.lsp.dev/protocol"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
)

func TestNew(t *testing.T) {
	assert.NotPanics(t, func() {
		New(Params{
			Logger:     zap.NewNop().Sugar(),
			IdeGateway: nil,
			Sessions:   nil,
		})
	})
}

func TestStartupInfo(t *testing.T) {
	ctx := context.Background()
	c := &controller{
		configs: map[entity.MonorepoName]entity.MonorepoConfigEntry{
			"lm/fievel": {
				Languages: []string{"java", "scala"},
			},
		},
	}
	info, err := c.StartupInfo(ctx)
	assert.NoError(t, err)
	assert.NoError(t, info.Validate())
	assert.Equal(t, _nameKey, info.NameKey)
	assert.Contains(t, info.RelevantRepos, entity.MonorepoName("lm/fievel"))
}

func TestInitialized(t *testing.T) {
	ctrl := gomock.NewController(t)
	sessionMock := repositorymock.NewMockRepository(ctrl)

	tests := []struct {
		name          string
		workspaceRoot string
		sessionErr    error
		createBSPFile bool
		wantErr       bool
	}{
		{
			name:          "success with existing config",
			workspaceRoot: t.TempDir(),
			createBSPFile: true,
			wantErr:       false,
		},
		{
			name:          "no-op without existing config",
			workspaceRoot: t.TempDir(),
			createBSPFile: false,
			wantErr:       false,
		},
		{
			name:          "fail to watch file",
			workspaceRoot: "/dev/null",
			createBSPFile: false,
			wantErr:       true,
		},
		{
			name:       "session error",
			sessionErr: errors.New("session not found"),
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockWatcher, err := fsnotify.NewWatcher()
			defer mockWatcher.Close()
			require.NoError(t, err)

			c := &controller{
				logger:            zap.NewNop().Sugar(),
				sessions:          sessionMock,
				watchedWorkspaces: make(map[string]struct{}),
				watcher:           mockWatcher,
			}

			if tt.createBSPFile {
				// Create the .bsp directory and file
				bspDir := path.Join(tt.workspaceRoot, _bspDir)
				require.NoError(t, os.MkdirAll(bspDir, 0755))

				bspFile := path.Join(tt.workspaceRoot, _bspDir, _bspSourceName)
				sampleBSPContent := map[string]interface{}{
					"name":       "bazelbsp",
					"version":    "1.0.0",
					"bspVersion": "2.1.0",
					"languages":  []string{"scala", "java"},
				}
				data, err := json.MarshalIndent(sampleBSPContent, "", "  ")
				require.NoError(t, err)
				err = os.WriteFile(bspFile, data, 0644)
				require.NoError(t, err)
			}

			if tt.sessionErr != nil {
				sessionMock.EXPECT().GetFromContext(gomock.Any()).Return(nil, tt.sessionErr)
			} else {
				sessionMock.EXPECT().GetFromContext(gomock.Any()).Return(&entity.Session{WorkspaceRoot: tt.workspaceRoot}, nil)
			}

			ctx := context.Background()
			params := &protocol.InitializedParams{}

			err = c.initialized(ctx, params)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)

				if tt.createBSPFile {
					// Verify the destination file was created with correct version
					destFile := path.Join(tt.workspaceRoot, ".bsp", "bazelbsp_scala.json")
					data, err := os.ReadFile(destFile)
					assert.NoError(t, err)

					var config map[string]interface{}
					err = json.Unmarshal(data, &config)
					assert.NoError(t, err)
					assert.Equal(t, _versionOverride, config["version"])
				}
			}
		})
	}
}

func TestBeginWatchingBspDir(t *testing.T) {
	t.Run("success with new file", func(t *testing.T) {
		workspaceRoot := t.TempDir()
		bspSourcePath := path.Join(workspaceRoot, _bspDir, _bspSourceName)
		bspDestPath := path.Join(workspaceRoot, _bspDir, _bspDestName)

		// Create initial BSP file
		bspDir := path.Join(workspaceRoot, ".bsp")
		require.NoError(t, os.MkdirAll(bspDir, 0755))

		initialConfig := map[string]interface{}{
			"name":       "bazelbsp",
			"version":    "1.0.0",
			"bspVersion": "2.1.0",
		}
		data, err := json.MarshalIndent(initialConfig, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(bspSourcePath, data, 0644))

		watcher, err := fsnotify.NewWatcher()
		require.NoError(t, err)
		defer watcher.Close()

		c := &controller{
			logger:            zap.NewNop().Sugar(),
			watcher:           watcher,
			watchedWorkspaces: make(map[string]struct{}),
		}

		ctx := context.Background()
		err = c.beginWatchingBspDir(ctx, workspaceRoot)

		assert.NoError(t, err)
		assert.NotNil(t, c.watchedWorkspaces[workspaceRoot])

		destData, err := os.ReadFile(bspDestPath)
		assert.NoError(t, err)

		var destConfig map[string]interface{}
		err = json.Unmarshal(destData, &destConfig)
		assert.NoError(t, err)
		assert.Equal(t, _versionOverride, destConfig["version"])
	})

	t.Run("nil watcher", func(t *testing.T) {
		c := &controller{
			logger:            zap.NewNop().Sugar(),
			watcher:           nil, // No watcher
			watchedWorkspaces: make(map[string]struct{}),
		}

		ctx := context.Background()
		err := c.beginWatchingBspDir(ctx, "/path/to/source")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "watcher not initialized")
	})

	t.Run("already watching", func(t *testing.T) {
		watcher, err := fsnotify.NewWatcher()
		require.NoError(t, err)
		defer watcher.Close()

		c := &controller{
			logger:            zap.NewNop().Sugar(),
			watcher:           watcher,
			watchedWorkspaces: map[string]struct{}{"/path/to/source": {}},
		}

		ctx := context.Background()
		err = c.beginWatchingBspDir(ctx, "/path/to/source")

		assert.NoError(t, err)
	})

	t.Run("mkdirall error", func(t *testing.T) {
		workspaceRoot := t.TempDir()

		// Create a file where the .bsp directory should be, causing MkdirAll to fail
		bspPath := path.Join(workspaceRoot, ".bsp")
		require.NoError(t, os.WriteFile(bspPath, []byte("blocking file"), 0644))

		watcher, err := fsnotify.NewWatcher()
		require.NoError(t, err)
		defer watcher.Close()

		c := &controller{
			logger:            zap.NewNop().Sugar(),
			watcher:           watcher,
			watchedWorkspaces: make(map[string]struct{}),
		}

		ctx := context.Background()
		err = c.beginWatchingBspDir(ctx, workspaceRoot)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "create BSP directory")

		// Verify no mappings were added due to the error
		assert.Empty(t, c.watchedWorkspaces)
	})

	t.Run("watcher add error", func(t *testing.T) {
		workspaceRoot := t.TempDir()

		watcher, err := fsnotify.NewWatcher()
		require.NoError(t, err)
		// Close the watcher to make Add() fail
		watcher.Close()

		c := &controller{
			logger:            zap.NewNop().Sugar(),
			watcher:           watcher,
			watchedWorkspaces: make(map[string]struct{}),
		}

		ctx := context.Background()
		err = c.beginWatchingBspDir(ctx, workspaceRoot)

		assert.Error(t, err)

		// Verify no mappings were added due to the error
		assert.Empty(t, c.watchedWorkspaces)
	})

	t.Run("update bsp json error", func(t *testing.T) {
		workspaceRoot := t.TempDir()
		bspSourcePath := path.Join(workspaceRoot, _bspSourceName)
		bspDestPath := path.Join(workspaceRoot, _bspDestName)

		// Create BSP directory and invalid JSON file to make updateBSPJson fail
		bspDir := path.Join(workspaceRoot, ".bsp")
		require.NoError(t, os.MkdirAll(bspDir, 0755))
		require.NoError(t, os.WriteFile(bspSourcePath, []byte("invalid json content"), 0644))

		watcher, err := fsnotify.NewWatcher()
		require.NoError(t, err)
		defer watcher.Close()

		c := &controller{
			logger:            zap.NewNop().Sugar(),
			watcher:           watcher,
			watchedWorkspaces: make(map[string]struct{}),
		}

		ctx := context.Background()
		err = c.beginWatchingBspDir(ctx, workspaceRoot)

		// Should succeed despite updateBSPJson error
		assert.NoError(t, err)

		// Verify file is still being watched despite updateBSPJson failure
		assert.NotNil(t, c.watchedWorkspaces[workspaceRoot])

		// Verify no destination file was created due to updateBSPJson failure
		_, err = os.Stat(bspDestPath)
		assert.True(t, os.IsNotExist(err))
	})
}

func TestEnsureWatcher(t *testing.T) {
	t.Run("new watcher", func(t *testing.T) {
		c := &controller{
			logger:            zap.NewNop().Sugar(),
			watchedWorkspaces: make(map[string]struct{}),
		}

		ctx := context.Background()
		err := c.ensureWatcher(ctx)

		assert.NoError(t, err)
		assert.NotNil(t, c.watcher)
		c.watcher.Close()
		c.wg.Wait()
	})

	t.Run("existing watcher", func(t *testing.T) {
		existingWatcher, err := fsnotify.NewWatcher()
		require.NoError(t, err)
		defer existingWatcher.Close()

		c := &controller{
			logger:            zap.NewNop().Sugar(),
			watcher:           existingWatcher,
			watchedWorkspaces: make(map[string]struct{}),
		}

		ctx := context.Background()
		err = c.ensureWatcher(ctx)

		assert.NoError(t, err)
		assert.Equal(t, existingWatcher, c.watcher)
	})
}

func TestUpdateBSPJson(t *testing.T) {
	tests := []struct {
		name                string
		sourceExists        bool
		sourceContent       map[string]interface{}
		setupDest           bool
		wantErr             bool
		expectedResult      map[string]interface{}
		makeUnreadable      bool
		makeDestDirReadOnly bool
	}{
		{
			name:         "success with valid JSON",
			sourceExists: true,
			sourceContent: map[string]interface{}{
				"name":       "bazelbsp",
				"version":    "1.0.0",
				"bspVersion": "2.1.0",
				"languages":  []string{"scala", "java"},
			},
			setupDest: true,
			expectedResult: map[string]interface{}{
				"name":       "bazelbsp",
				"version":    _versionOverride,
				"bspVersion": "2.1.0",
				"languages":  []interface{}{"scala", "java"},
			},
		},
		{
			name:         "no-op when source doesn't exist",
			sourceExists: false,
			setupDest:    true,
			wantErr:      false,
		},
		{
			name:         "invalid JSON in source",
			sourceExists: true,
			setupDest:    true,
			wantErr:      true,
		},
		{
			name:           "source file read error",
			sourceExists:   true,
			setupDest:      true,
			makeUnreadable: true,
			sourceContent: map[string]interface{}{
				"name":    "bazelbsp",
				"version": "1.0.0",
			},
			wantErr: true,
		},
		{
			name:                "destination file write error",
			sourceExists:        true,
			setupDest:           true,
			makeDestDirReadOnly: true,
			sourceContent: map[string]interface{}{
				"name":    "bazelbsp",
				"version": "1.0.0",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspaceRoot := t.TempDir()
			bspDir := path.Join(workspaceRoot, ".bsp")
			require.NoError(t, os.MkdirAll(bspDir, 0755))

			bspSourcePath := path.Join(bspDir, "bazelbsp.json")
			bspDestPath := path.Join(bspDir, "bazelbsp_scala.json")

			watchedWorkspaces := make(map[string]struct{})
			if tt.setupDest {
				watchedWorkspaces[workspaceRoot] = struct{}{}
			}

			c := &controller{
				logger:            zap.NewNop().Sugar(),
				watchedWorkspaces: watchedWorkspaces,
			}

			if tt.sourceExists {
				if tt.sourceContent != nil {
					data, err := json.MarshalIndent(tt.sourceContent, "", "  ")
					require.NoError(t, err)
					require.NoError(t, os.WriteFile(bspSourcePath, data, 0644))

					// Make file unreadable to trigger read error
					if tt.makeUnreadable {
						require.NoError(t, os.Chmod(bspSourcePath, 0000))
						// Ensure we can restore permissions for cleanup
						t.Cleanup(func() {
							os.Chmod(bspSourcePath, 0644)
						})
					}
				} else {
					// Create invalid JSON for testing
					require.NoError(t, os.WriteFile(bspSourcePath, []byte("invalid json"), 0644))
				}
			}

			// Make destination directory read-only to trigger write error
			if tt.makeDestDirReadOnly {
				require.NoError(t, os.Chmod(bspDir, 0555))
				t.Cleanup(func() {
					os.Chmod(bspDir, 0755)
				})
			}

			ctx := context.Background()
			err := c.updateBSPJson(ctx, workspaceRoot)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)

				if tt.expectedResult != nil {
					destData, err := os.ReadFile(bspDestPath)
					assert.NoError(t, err)

					var actualResult map[string]interface{}
					err = json.Unmarshal(destData, &actualResult)
					assert.NoError(t, err)
					assert.Equal(t, tt.expectedResult, actualResult)
				}
			}
		})
	}
}

func TestWatchBSPChanges(t *testing.T) {
	t.Run("no watcher set", func(t *testing.T) {
		c := &controller{
			logger:            zap.NewNop().Sugar(),
			watcher:           nil,
			watchedWorkspaces: make(map[string]struct{}),
		}

		ctx := context.Background()
		assert.NotPanics(t, func() {
			c.watchBSPChanges(ctx)
		})
	})

	t.Run("context cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		watcher, err := fsnotify.NewWatcher()
		require.NoError(t, err)
		defer watcher.Close()

		c := &controller{
			logger:            zap.NewNop().Sugar(),
			watcher:           watcher,
			watchedWorkspaces: make(map[string]struct{}),
		}

		c.watchBSPChanges(ctx)
		cancel()
		c.wg.Wait()
	})
}

func TestConsumeWatcherEvent(t *testing.T) {
	t.Run("write event", func(t *testing.T) {
		workspaceRoot := t.TempDir()
		bspDir := path.Join(workspaceRoot, _bspDir)
		require.NoError(t, os.MkdirAll(bspDir, 0755))

		sourcePath := path.Join(workspaceRoot, _bspDir, _bspSourceName)
		destPath := path.Join(workspaceRoot, _bspDir, _bspDestName)

		sourceContent := map[string]interface{}{
			"name":       "bazelbsp",
			"version":    "1.0.0",
			"bspVersion": "2.1.0",
		}
		data, err := json.MarshalIndent(sourceContent, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(sourcePath, data, 0644))

		c := &controller{
			logger:            zap.NewNop().Sugar(),
			watchedWorkspaces: map[string]struct{}{workspaceRoot: {}},
		}

		event := fsnotify.Event{
			Name: sourcePath,
			Op:   fsnotify.Write,
		}

		ctx := context.Background()
		c.consumeWatcherEvent(ctx, event)

		destData, err := os.ReadFile(destPath)
		assert.NoError(t, err, "destination file should be created")

		var destConfig map[string]interface{}
		err = json.Unmarshal(destData, &destConfig)
		assert.NoError(t, err)
		assert.Equal(t, _versionOverride, destConfig["version"], "version should be overridden")
	})

	t.Run("other event", func(t *testing.T) {
		workspaceRoot := t.TempDir()
		bspDir := path.Join(workspaceRoot, _bspDir)
		require.NoError(t, os.MkdirAll(bspDir, 0755))

		sourcePath := path.Join(workspaceRoot, _bspDir, _bspSourceName)
		destPath := path.Join(workspaceRoot, _bspDir, _bspDestName)

		sourceContent := map[string]interface{}{
			"name":    "bazelbsp",
			"version": "1.0.0",
		}
		data, err := json.MarshalIndent(sourceContent, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(sourcePath, data, 0644))

		c := &controller{
			logger:            zap.NewNop().Sugar(),
			watchedWorkspaces: map[string]struct{}{workspaceRoot: {}},
		}

		event := fsnotify.Event{
			Name: sourcePath,
			Op:   fsnotify.Create, // Not a write event
		}

		ctx := context.Background()
		c.consumeWatcherEvent(ctx, event)

		_, err = os.Stat(destPath)
		assert.True(t, os.IsNotExist(err), "destination file should not exist")
	})

	t.Run("other file path", func(t *testing.T) {
		workspaceRoot := t.TempDir()
		bspDir := path.Join(workspaceRoot, _bspDir)
		require.NoError(t, os.MkdirAll(bspDir, 0755))

		sourcePath := path.Join(workspaceRoot, _bspDir, _bspSourceName)
		destPath := path.Join(workspaceRoot, _bspDir, _bspDestName)

		sourceContent := map[string]interface{}{
			"name":    "bazelbsp",
			"version": "1.0.0",
		}
		data, err := json.MarshalIndent(sourceContent, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(sourcePath, data, 0644))

		c := &controller{
			logger:            zap.NewNop().Sugar(),
			watchedWorkspaces: map[string]struct{}{workspaceRoot: {}},
		}

		event := fsnotify.Event{
			Name: "/some/other/file.json",
			Op:   fsnotify.Write,
		}

		ctx := context.Background()
		c.consumeWatcherEvent(ctx, event)

		_, err = os.Stat(destPath)
		assert.True(t, os.IsNotExist(err), "destination file should not exist")
	})

	t.Run("unwatched workspace", func(t *testing.T) {
		workspaceRoot := t.TempDir()
		bspDir := path.Join(workspaceRoot, _bspDir)
		require.NoError(t, os.MkdirAll(bspDir, 0755))

		sourcePath := path.Join(workspaceRoot, _bspDir, _bspSourceName)
		destPath := path.Join(workspaceRoot, _bspDir, _bspDestName)

		sourceContent := map[string]interface{}{
			"name":    "bazelbsp",
			"version": "1.0.0",
		}
		data, err := json.MarshalIndent(sourceContent, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(sourcePath, data, 0644))

		c := &controller{
			logger:            zap.NewNop().Sugar(),
			watchedWorkspaces: make(map[string]struct{}),
		}

		event := fsnotify.Event{
			Name: sourcePath,
			Op:   fsnotify.Write,
		}

		ctx := context.Background()
		c.consumeWatcherEvent(ctx, event)

		_, err = os.Stat(destPath)
		assert.True(t, os.IsNotExist(err), "destination file should not exist")
	})

	t.Run("error - updateBSPJson fails", func(t *testing.T) {
		workspaceRoot := t.TempDir()
		bspDir := path.Join(workspaceRoot, _bspDir)
		require.NoError(t, os.MkdirAll(bspDir, 0755))

		sourcePath := path.Join(workspaceRoot, _bspDir, _bspSourceName)
		destPath := path.Join(workspaceRoot, _bspDir, _bspDestName)

		// Create source file
		sourceContent := map[string]interface{}{
			"name":    "bazelbsp",
			"version": "1.0.0",
		}
		data, err := json.MarshalIndent(sourceContent, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(sourcePath, data, 0644))

		// Make destination directory read-only to cause write error
		require.NoError(t, os.Chmod(bspDir, 0555))
		t.Cleanup(func() {
			os.Chmod(bspDir, 0755)
		})

		c := &controller{
			logger:            zap.NewNop().Sugar(),
			watchedWorkspaces: map[string]struct{}{workspaceRoot: {}},
		}

		event := fsnotify.Event{
			Name: sourcePath,
			Op:   fsnotify.Write,
		}

		ctx := context.Background()

		assert.NotPanics(t, func() {
			c.consumeWatcherEvent(ctx, event)
		})

		_, err = os.Stat(destPath)
		assert.True(t, os.IsNotExist(err), "destination file should not exist")
	})
}
