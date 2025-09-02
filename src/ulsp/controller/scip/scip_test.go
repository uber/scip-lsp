package scip

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/sourcegraph/scip/bindings/go/scip"
	scipproto "github.com/sourcegraph/scip/bindings/go/scip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uber-go/tally"
	"github.com/uber/scip-lsp/src/scip-lib/mapper"
	"github.com/uber/scip-lsp/src/scip-lib/model"
	"github.com/uber/scip-lsp/src/ulsp/controller/diagnostics/diagnosticsmock"
	docsync "github.com/uber/scip-lsp/src/ulsp/controller/doc-sync"
	"github.com/uber/scip-lsp/src/ulsp/controller/doc-sync/docsyncmock"
	"github.com/uber/scip-lsp/src/ulsp/entity"
	"github.com/uber/scip-lsp/src/ulsp/factory"
	"github.com/uber/scip-lsp/src/ulsp/gateway/ide-client/ideclientmock"
	"github.com/uber/scip-lsp/src/ulsp/internal/fs/fsmock"
	notifier "github.com/uber/scip-lsp/src/ulsp/internal/persistent-notifier"
	"github.com/uber/scip-lsp/src/ulsp/internal/persistent-notifier/notifiermock"
	"github.com/uber/scip-lsp/src/ulsp/repository/session/repositorymock"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
	"go.uber.org/config"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
)

const _monorepoNameJava entity.MonorepoName = "lm/fievel"

type mockDirEntry struct {
	name string
}

// Name ...
func (e mockDirEntry) Name() string {
	return e.name
}

func (e mockDirEntry) IsDir() bool {
	return false
}

func (e mockDirEntry) Type() os.FileMode {
	return 0666
}

func (e mockDirEntry) Info() (os.FileInfo, error) {
	return nil, nil
}

func NewMockDirEntry(name string) os.DirEntry {
	return &mockDirEntry{
		name,
	}
}

func TestNew(t *testing.T) {
	mockConfig, _ := config.NewStaticProvider(map[string]map[string]entity.MonorepoConfigEntry{
		entity.MonorepoConfigKey: {
			"go-code": {
				Scip: entity.ScipConfig{
					LoadFromDirectories: true,
					Directories:         []string{},
				},
			},
		}})
	assert.NotPanics(t, func() {
		New(Params{
			Stats:  tally.NewTestScope("testing", make(map[string]string, 0)),
			Config: mockConfig,
			Logger: zap.NewNop().Sugar(),
		})
	})
}

func TestStartupInfo(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	c, _ := getMockedController(t, ctrl)
	result, err := c.StartupInfo(ctx)

	assert.NoError(t, err)
	assert.NoError(t, result.Validate())
	assert.Equal(t, _nameKey, result.NameKey)
	var expected map[entity.MonorepoName]struct{} = nil
	assert.Equal(t, expected, result.RelevantRepos)
}

func TestInitialize(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	sampleWorkspaceRoot := path.Join("/sample/home/", string(_monorepoNameJava))
	s := &entity.Session{
		UUID:     factory.UUID(),
		Monorepo: "_default",
	}
	s.WorkspaceRoot = sampleWorkspaceRoot
	notMgrMock := notifiermock.NewMockNotificationManager(ctrl)
	notMgrMock.EXPECT().StartNotification(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("No notifier")).AnyTimes()

	dir := t.TempDir()
	scipDir := path.Join(dir, ".scip")

	newScipCtl := func(fs *fsmock.MockUlspFS, reg *MockRegistry, monorepo entity.MonorepoName, loadFromDir bool, sessionFail bool, createScipDir bool) controller {
		regs := map[string]Registry{}

		s.Monorepo = monorepo
		s.WorkspaceRoot = dir
		sessionRepository := repositorymock.NewMockRepository(ctrl)
		if sessionFail {
			sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, errors.New("no session")).AnyTimes()
		} else {
			sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil).AnyTimes()
		}
		if createScipDir {
			os.Mkdir(scipDir, os.ModePerm)
		} else {
			os.Remove(scipDir)
		}
		w, err := fsnotify.NewWatcher()
		assert.NoError(t, err)

		return controller{
			configs: map[entity.MonorepoName]entity.MonorepoConfigEntry{"_default": {
				Scip: entity.ScipConfig{
					LoadFromDirectories: loadFromDir,
					Directories: []string{
						".scip",
					},
				},
			}},
			sessions:      sessionRepository,
			registries:    regs,
			logger:        zap.NewNop().Sugar(),
			fs:            fs,
			watcher:       w,
			watchCloser:   make(chan bool),
			initialLoad:   make(chan bool, 1),
			loadedIndices: make(map[string]string),
			newScipRegistry: func(workspaceRoot string, indexFolder string) Registry {
				return reg
			},
			indexNotifier: NewIndexNotifier(notMgrMock),
		}
	}

	t.Run("not enabled", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		regMock := NewMockRegistry(ctrl)
		fsMock := fsmock.NewMockUlspFS(ctrl)

		c := newScipCtl(fsMock, regMock, _monorepoNameJava, false, false, true)

		err := c.initialize(ctx, &protocol.InitializeParams{}, &protocol.InitializeResult{})
		assert.NoError(t, err)
		err = c.initialized(ctx, &protocol.InitializedParams{})

		assert.NoError(t, err)
		c.watchCloser <- true
	})

	t.Run("no scip files", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		regMock := NewMockRegistry(ctrl)
		regMock.EXPECT().LoadConcurrency().Return(1)
		regMock.EXPECT().SetDocumentLoadedCallback(gomock.Any())
		fsMock := fsmock.NewMockUlspFS(ctrl)
		fsMock.EXPECT().ReadDir(gomock.Any()).Return([]os.DirEntry{}, nil)
		fsMock.EXPECT().MkdirAll(gomock.Any()).Return(nil)

		c := newScipCtl(fsMock, regMock, "_default", true, false, true)

		err := c.initialize(ctx, &protocol.InitializeParams{}, &protocol.InitializeResult{})
		assert.NoError(t, err)
		err = c.initialized(ctx, &protocol.InitializedParams{})

		assert.NoError(t, err)
		c.watchCloser <- true
	})

	t.Run("failed to add watcher", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		regMock := NewMockRegistry(ctrl)
		fsMock := fsmock.NewMockUlspFS(ctrl)
		fsMock.EXPECT().MkdirAll(gomock.Any()).Return(errors.New("failed"))

		c := newScipCtl(fsMock, regMock, "_default", false, false, false)

		err := c.initialize(ctx, &protocol.InitializeParams{}, &protocol.InitializeResult{})
		assert.NoError(t, err)
		err = c.initialized(ctx, &protocol.InitializedParams{})
		assert.Error(t, err)
		c.watchCloser <- true
	})

	t.Run("failed to create scip dir", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		regMock := NewMockRegistry(ctrl)
		fsMock := fsmock.NewMockUlspFS(ctrl)
		fsMock.EXPECT().MkdirAll(gomock.Any()).Return(errors.New("failed to create scip dir"))

		c := newScipCtl(fsMock, regMock, "_default", false, false, false)

		err := c.initialize(ctx, &protocol.InitializeParams{}, &protocol.InitializeResult{})
		assert.NoError(t, err)
		err = c.initialized(ctx, &protocol.InitializedParams{})
		assert.Error(t, err)
		c.watchCloser <- true
	})

	t.Run("with scip files", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		regMock := NewMockRegistry(ctrl)
		regMock.EXPECT().LoadConcurrency().Return(1)
		regMock.EXPECT().SetDocumentLoadedCallback(gomock.Any())
		regMock.EXPECT().LoadIndexFile(gomock.Any()).Return(nil)
		fsMock := fsmock.NewMockUlspFS(ctrl)
		fsMock.EXPECT().MkdirAll(gomock.Any()).Return(nil)
		fsMock.EXPECT().ReadDir(gomock.Any()).Return([]os.DirEntry{
			NewMockDirEntry("sample.scip"),
			NewMockDirEntry("sample.scip.sha256"),
		}, nil)
		fsMock.EXPECT().ReadFile(gomock.Any()).Return([]byte("sampleSha"), nil)

		c := newScipCtl(fsMock, regMock, "_default", true, false, true)

		err := c.initialize(ctx, &protocol.InitializeParams{}, &protocol.InitializeResult{})
		assert.NoError(t, err)
		err = c.initialized(ctx, &protocol.InitializedParams{})

		assert.NoError(t, err)
		c.watchCloser <- true
	})

	t.Run("file open error doesn't fail", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		regMock := NewMockRegistry(ctrl)
		regMock.EXPECT().SetDocumentLoadedCallback(gomock.Any())
		regMock.EXPECT().LoadConcurrency().Return(1)
		regMock.EXPECT().LoadIndexFile(gomock.Any()).Return(errors.New("file not found"))

		fsMock := fsmock.NewMockUlspFS(ctrl)
		fsMock.EXPECT().MkdirAll(gomock.Any()).Return(nil)
		fsMock.EXPECT().ReadDir(gomock.Any()).Return([]os.DirEntry{
			NewMockDirEntry("sample.scip"),
		}, nil)
		fsMock.EXPECT().ReadFile(gomock.Any()).Return([]byte("sampleSha"), nil)

		c := newScipCtl(fsMock, regMock, "_default", true, false, true)

		err := c.initialize(ctx, &protocol.InitializeParams{}, &protocol.InitializeResult{})
		assert.NoError(t, err)
		err = c.initialized(ctx, &protocol.InitializedParams{})

		assert.NoError(t, err)
		c.watchCloser <- true
	})

	t.Run("LoadIndex error doesn't fail", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		regMock := NewMockRegistry(ctrl)
		regMock.EXPECT().SetDocumentLoadedCallback(gomock.Any())
		regMock.EXPECT().LoadConcurrency().Return(1)
		regMock.EXPECT().LoadIndexFile(gomock.Any()).Return(errors.New("invalid scip file"))
		fsMock := fsmock.NewMockUlspFS(ctrl)
		fsMock.EXPECT().MkdirAll(gomock.Any()).Return(nil)
		fsMock.EXPECT().ReadDir(gomock.Any()).Return([]os.DirEntry{
			NewMockDirEntry("sample.scip"),
			NewMockDirEntry("sample.scip.sha256"),
		}, nil)
		fsMock.EXPECT().ReadFile(gomock.Any()).Return([]byte("sampleSha"), nil)

		c := newScipCtl(fsMock, regMock, "_default", true, false, true)

		err := c.initialize(ctx, &protocol.InitializeParams{}, &protocol.InitializeResult{})
		assert.NoError(t, err)
		err = c.initialized(ctx, &protocol.InitializedParams{})

		assert.NoError(t, err)
		c.watchCloser <- true
	})

	t.Run("ReadDir error doesn't fail", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		regMock := NewMockRegistry(ctrl)
		regMock.EXPECT().SetDocumentLoadedCallback(gomock.Any())
		fsMock := fsmock.NewMockUlspFS(ctrl)
		fsMock.EXPECT().MkdirAll(gomock.Any()).Return(nil)
		fsMock.EXPECT().ReadDir(gomock.Any()).Return([]os.DirEntry{}, errors.New("invalid scip file"))

		c := newScipCtl(fsMock, regMock, "_default", true, false, true)

		err := c.initialize(ctx, &protocol.InitializeParams{}, &protocol.InitializeResult{})
		assert.NoError(t, err)
		err = c.initialized(ctx, &protocol.InitializedParams{})

		assert.NoError(t, err)
		c.watchCloser <- true
	})

	t.Run("No registry", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		fsMock := fsmock.NewMockUlspFS(ctrl)
		fsMock.EXPECT().MkdirAll(gomock.Any()).Return(nil)
		fsMock.EXPECT().ReadDir(gomock.Any()).Return([]os.DirEntry{
			NewMockDirEntry("sample.scip"),
		}, nil)
		fsMock.EXPECT().ReadFile(gomock.Any()).Return([]byte("sampleSha"), nil)
		mockReg := NewMockRegistry(ctrl)
		mockReg.EXPECT().SetDocumentLoadedCallback(gomock.Any())
		mockReg.EXPECT().LoadConcurrency().Return(1)
		mockReg.EXPECT().LoadIndexFile(gomock.Any()).Return(nil)
		c := newScipCtl(fsMock, mockReg, "_default", true, false, true)

		err := c.initialize(ctx, &protocol.InitializeParams{}, &protocol.InitializeResult{})
		assert.NoError(t, err)
		err = c.initialized(ctx, &protocol.InitializedParams{})

		assert.NoError(t, err)
		c.watchCloser <- true
	})

	t.Run("no session", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		fsMock := fsmock.NewMockUlspFS(ctrl)
		c := newScipCtl(fsMock, nil, "_default", true, true, true)

		err := c.initialize(ctx, &protocol.InitializeParams{}, &protocol.InitializeResult{})
		assert.NoError(t, err)
		err = c.initialized(ctx, &protocol.InitializedParams{})
		assert.Error(t, err)
	})

	t.Run("no config", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		fsMock := fsmock.NewMockUlspFS(ctrl)
		c := newScipCtl(fsMock, nil, "lm/fievel", true, false, true)

		err := c.initialize(ctx, &protocol.InitializeParams{}, &protocol.InitializeResult{})
		assert.NoError(t, err)
		err = c.initialized(ctx, &protocol.InitializedParams{})

		assert.NoError(t, err)
		c.watchCloser <- true
	})
}

func TestDidChange(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	c, _ := getMockedController(t, ctrl)
	params := getMockedDidOpenTextDocumentParams()
	err := c.didChange(ctx, params)

	assert.NoError(t, err)
}

func TestGotoDeclaration(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()
	c, _ := getMockedController(t, ctrl)
	err := c.gotoDeclaration(ctx, nil, nil)

	assert.NoError(t, err)
}

func getMockedController(t *testing.T, ctrl *gomock.Controller) (controller, *MockRegistry) {
	reg := NewMockRegistry(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	s.WorkspaceRoot = "/some/path"
	sessionRepository := repositorymock.NewMockRepository(ctrl)
	sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil).AnyTimes()

	return controller{
		registries: map[string]Registry{
			"/some/path": reg,
		},
		sessions: sessionRepository,
		logger:   zap.NewNop().Sugar(),
	}, reg
}

func getMockTextDocumentPositionParams() protocol.TextDocumentPositionParams {
	return protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{
			URI: protocol.DocumentURI("file:///test.go"),
		},
		Position: protocol.Position{
			Line:      1,
			Character: 1,
		},
	}
}

func getMockedTextDocumentParams() protocol.TextDocumentIdentifier {
	return protocol.TextDocumentIdentifier{
		URI: protocol.DocumentURI("file:///test.go"),
	}
}

func getMockedDidOpenTextDocumentParams() *protocol.DidChangeTextDocumentParams {
	return &protocol.DidChangeTextDocumentParams{
		TextDocument: protocol.VersionedTextDocumentIdentifier{
			Version:                1,
			TextDocumentIdentifier: getMockedTextDocumentParams(),
		},
	}
}

func TestGotoDefinition(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	tests := []struct {
		name        string
		setupMocks  func(t *testing.T, c *controller, reg *MockRegistry)
		expected    []protocol.LocationLink
		expectedErr error
	}{
		{
			name: "no symbol data",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).DoAndReturn(func(pos protocol.Position) (protocol.Position, bool, error) {
					return pos, false, nil
				})
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil).AnyTimes()
				c.documents = documents

				reg.EXPECT().Definition(gomock.Any(), gomock.Any()).Return(nil, nil, nil)
			},
			expected: []protocol.LocationLink{},
		},
		{
			name: "has error return",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).DoAndReturn(func(pos protocol.Position) (protocol.Position, bool, error) {
					return pos, false, nil
				})
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil)
				c.documents = documents

				reg.EXPECT().Definition(gomock.Any(), gomock.Any()).Return(nil, nil, errors.New("test error"))
			},
			expected:    []protocol.LocationLink{},
			expectedErr: errors.New("test error"),
		},
		{
			name: "has location",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).DoAndReturn(func(pos protocol.Position) (protocol.Position, bool, error) {
					return pos, false, nil
				})
				positionMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).DoAndReturn(func(pos protocol.Position) (protocol.Position, error) {
					return pos, nil
				}).Times(4)
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil).AnyTimes()
				c.documents = documents

				reg.EXPECT().Definition(gomock.Any(), gomock.Any()).Return(&model.SymbolOccurrence{
					Location: protocol.DocumentURI("file:///source.go"),
					Occurrence: &model.Occurrence{
						Range: []int32{4, 5, 6, 6},
					},
					Info: &model.SymbolInformation{
						Symbol: "test",
					},
				}, &model.SymbolOccurrence{
					Location: protocol.DocumentURI("file:///definition.go"),
					Occurrence: &model.Occurrence{
						Range: []int32{1, 2, 3},
					},
					Info: &model.SymbolInformation{
						Symbol: "test",
					},
				}, nil)
			},
			expected: []protocol.LocationLink{
				{
					TargetRange:          mapper.ScipToProtocolRange([]int32{1, 2, 3}),
					TargetSelectionRange: mapper.ScipToProtocolRange([]int32{1, 2, 3}),
					TargetURI:            protocol.DocumentURI("file:///definition.go"),
					OriginSelectionRange: &protocol.Range{
						Start: protocol.Position{Line: 4, Character: 5},
						End:   protocol.Position{Line: 6, Character: 6},
					},
				},
			},
		},
		{
			name: "nil definition occurrence",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).DoAndReturn(func(pos protocol.Position) (protocol.Position, bool, error) {
					return pos, false, nil
				})
				positionMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).DoAndReturn(func(pos protocol.Position) (protocol.Position, error) {
					return pos, nil
				}).Times(2)
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil).AnyTimes()
				c.documents = documents

				reg.EXPECT().Definition(gomock.Any(), gomock.Any()).Return(&model.SymbolOccurrence{
					Location: protocol.DocumentURI("file:///source.go"),
					Occurrence: &model.Occurrence{
						Range: []int32{4, 5, 6, 6},
					},
					Info: &model.SymbolInformation{
						Symbol: "test",
					},
				}, &model.SymbolOccurrence{
					Location:   protocol.DocumentURI("file:///definition.go"),
					Occurrence: nil,
					Info: &model.SymbolInformation{
						Symbol: "test",
					},
				}, nil)
			},
			expected: []protocol.LocationLink{
				{
					TargetRange:          protocol.Range{},
					TargetSelectionRange: protocol.Range{},
					TargetURI:            protocol.DocumentURI("file:///definition.go"),
					OriginSelectionRange: &protocol.Range{
						Start: protocol.Position{Line: 4, Character: 5},
						End:   protocol.Position{Line: 6, Character: 6},
					},
				},
			},
		},
		{
			name: "shifted position in request",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).Return(protocol.Position{Line: 2, Character: 2}, false, nil)
				positionMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).DoAndReturn(func(pos protocol.Position) (protocol.Position, error) {
					return pos, nil
				}).Times(2)
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil).AnyTimes()
				c.documents = documents

				reg.EXPECT().Definition(gomock.Any(), gomock.Any()).Return(nil, &model.SymbolOccurrence{
					Location: protocol.DocumentURI("file:///test.go"),
					Occurrence: &model.Occurrence{
						Range: []int32{2, 2, 3},
					},
					Info: nil,
				}, nil)
			},
			expected: []protocol.LocationLink{
				{
					TargetRange:          mapper.ScipToProtocolRange([]int32{2, 2, 3}),
					TargetSelectionRange: mapper.ScipToProtocolRange([]int32{2, 2, 3}),
					TargetURI:            protocol.DocumentURI("file:///test.go"),
				},
			},
		},
		{
			name: "new position in request",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).Return(protocol.Position{Line: 2, Character: 2}, true, nil)
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil)
				c.documents = documents
			},
			expected: []protocol.LocationLink{},
		},
		{
			name: "shifted position error",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).Return(protocol.Position{}, false, errors.New("test error"))
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil)
				c.documents = documents
			},
			expected:    []protocol.LocationLink{},
			expectedErr: errors.New("test error"),
		},
		{
			name: "shifted position in response",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).Return(protocol.Position{Line: 2, Character: 2}, false, nil)

				// Mock specific position mappings for start and end positions of both ranges
				positionMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).Return(protocol.Position{Line: 15, Character: 10}, nil) // TargetRange.Start
				positionMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).Return(protocol.Position{Line: 16, Character: 20}, nil) // TargetRange.End

				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil).AnyTimes()
				c.documents = documents

				reg.EXPECT().Definition(gomock.Any(), gomock.Any()).Return(nil, &model.SymbolOccurrence{
					Location: protocol.DocumentURI("file:///test.go"),
					Occurrence: &model.Occurrence{
						Range: []int32{5, 5, 6},
					},
					Info: nil,
				}, nil)
			},
			expected: []protocol.LocationLink{
				{
					TargetRange: protocol.Range{
						Start: protocol.Position{Line: 15, Character: 10},
						End:   protocol.Position{Line: 16, Character: 20},
					},
					TargetSelectionRange: protocol.Range{
						Start: protocol.Position{Line: 15, Character: 10},
						End:   protocol.Position{Line: 16, Character: 20},
					},
					TargetURI: protocol.DocumentURI("file:///test.go"),
				},
			},
		},
		{
			name: "shifted position errors in response",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).Return(protocol.Position{Line: 2, Character: 2}, false, nil)

				// First point in each position succeeds, second fails with error
				positionMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).Return(protocol.Position{Line: 15, Character: 10}, nil)
				positionMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).Return(protocol.Position{}, errors.New("failed to map position"))

				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil).AnyTimes()
				c.documents = documents

				reg.EXPECT().Definition(gomock.Any(), gomock.Any()).Return(nil, &model.SymbolOccurrence{
					Location: protocol.DocumentURI("file:///test.go"),
					Occurrence: &model.Occurrence{
						Range: []int32{5, 5, 6, 6},
					},
					Info: nil,
				}, nil)
			},
			expected: []protocol.LocationLink{
				{
					TargetRange: protocol.Range{
						Start: protocol.Position{Line: 5, Character: 5},
						End:   protocol.Position{Line: 6, Character: 6},
					},
					TargetSelectionRange: protocol.Range{
						Start: protocol.Position{Line: 5, Character: 5},
						End:   protocol.Position{Line: 6, Character: 6},
					},
					TargetURI: protocol.DocumentURI("file:///test.go"),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			c, reg := getMockedController(t, ctrl)
			tt.setupMocks(t, &c, reg)

			req := &protocol.DefinitionParams{
				TextDocumentPositionParams: getMockTextDocumentPositionParams(),
			}

			res := []protocol.LocationLink{}
			err := c.gotoDefinition(ctx, req, &res)

			if tt.expectedErr != nil {
				assert.ErrorContains(t, err, tt.expectedErr.Error())
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, res)
			}
		})
	}
}

func TestDocumentSymbol(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	tests := []struct {
		name        string
		setupMocks  func(t *testing.T, c *controller, reg *MockRegistry)
		expected    []protocol.DocumentSymbol
		expectedErr error
	}{
		{
			name: "empty symbol",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).DoAndReturn(func(pos protocol.Position) (protocol.Position, error) {
					return pos, nil
				}).MinTimes(0)
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil).MinTimes(0)
				c.documents = documents

				reg.EXPECT().DocumentSymbols(gomock.Any()).Return([]*model.SymbolOccurrence{}, nil)
			},
			expected: []protocol.DocumentSymbol{},
		},
		{
			name: "has error return",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).DoAndReturn(func(pos protocol.Position) (protocol.Position, error) {
					return pos, nil
				}).MinTimes(0)
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil).MinTimes(0)
				c.documents = documents
				reg.EXPECT().DocumentSymbols(gomock.Any()).Return([]*model.SymbolOccurrence{}, errors.New("test error"))
			},
			expected:    []protocol.DocumentSymbol{},
			expectedErr: errors.New("test error"),
		},
		{
			name: "has symbol",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).DoAndReturn(func(pos protocol.Position) (protocol.Position, error) {
					return pos, nil
				}).AnyTimes()
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil).AnyTimes()
				c.documents = documents

				reg.EXPECT().DocumentSymbols(gomock.Any()).Return([]*model.SymbolOccurrence{
					{
						Info: &model.SymbolInformation{
							DisplayName: "test",
							Kind:        scipproto.SymbolInformation_Class,
						},
						Occurrence: &model.Occurrence{
							Range: []int32{1, 2, 1, 3},
						},
						Location: protocol.URI("file:///test.go"),
					},
				}, nil)
			},
			expected: []protocol.DocumentSymbol{
				{
					Name:   "test",
					Detail: "[uLSP]test",
					Kind:   protocol.SymbolKindClass,
					Range: protocol.Range{
						Start: protocol.Position{
							Line:      1,
							Character: 2,
						},
						End: protocol.Position{
							Line:      1,
							Character: 3,
						},
					},
					SelectionRange: protocol.Range{
						Start: protocol.Position{
							Line:      1,
							Character: 2,
						},
						End: protocol.Position{
							Line:      1,
							Character: 3,
						},
					},
				},
			},
		},
		{
			name: "shifted symbol positions in response",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				// Mock position mapping to shift positions by 10 lines and 5 characters
				positionMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).DoAndReturn(func(pos protocol.Position) (protocol.Position, error) {
					return protocol.Position{
						Line:      pos.Line + 10,
						Character: pos.Character + 5,
					}, nil
				}).Times(4)
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil).AnyTimes()
				c.documents = documents

				reg.EXPECT().DocumentSymbols(gomock.Any()).Return([]*model.SymbolOccurrence{
					{
						Info: &model.SymbolInformation{
							DisplayName: "TestMethod",
							Kind:        scipproto.SymbolInformation_Method,
						},
						Occurrence: &model.Occurrence{
							Range: []int32{5, 10, 5, 20},
						},
						Location: protocol.URI("file:///test.go"),
					},
				}, nil)
			},
			expected: []protocol.DocumentSymbol{
				{
					Name:   "TestMethod",
					Detail: "[uLSP]TestMethod",
					Kind:   protocol.SymbolKindMethod,
					Range: protocol.Range{
						Start: protocol.Position{
							Line:      15,
							Character: 15,
						},
						End: protocol.Position{
							Line:      15,
							Character: 25,
						},
					},
					SelectionRange: protocol.Range{
						Start: protocol.Position{
							Line:      15,
							Character: 15,
						},
						End: protocol.Position{
							Line:      15,
							Character: 25,
						},
					},
				},
			},
		},
		{
			name: "position mapping errors in response",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).DoAndReturn(func(pos protocol.Position) (protocol.Position, error) {
					return protocol.Position{}, errors.New("failed to map position")
				}).Times(2)
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil).AnyTimes()
				c.documents = documents

				reg.EXPECT().DocumentSymbols(gomock.Any()).Return([]*model.SymbolOccurrence{
					{
						Info: &model.SymbolInformation{
							DisplayName: "TestMethod",
							Kind:        scipproto.SymbolInformation_Method,
						},
						Occurrence: &model.Occurrence{
							Range: []int32{5, 10, 5, 20},
						},
						Location: protocol.URI("file:///test.go"),
					},
				}, nil)
			},
			expected: []protocol.DocumentSymbol{
				{
					Name:   "TestMethod",
					Detail: "[uLSP]TestMethod",
					Kind:   protocol.SymbolKindMethod,
					Range: protocol.Range{
						Start: protocol.Position{
							Line:      5,
							Character: 10,
						},
						End: protocol.Position{
							Line:      5,
							Character: 20,
						},
					},
					SelectionRange: protocol.Range{
						Start: protocol.Position{
							Line:      5,
							Character: 10,
						},
						End: protocol.Position{
							Line:      5,
							Character: 20,
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			c, reg := getMockedController(t, ctrl)
			tt.setupMocks(t, &c, reg)

			req := &protocol.DocumentSymbolParams{
				TextDocument: getMockedTextDocumentParams(),
			}
			res := []protocol.DocumentSymbol{}
			err := c.documentSymbol(ctx, req, &res)

			if tt.expectedErr != nil {
				assert.ErrorContains(t, err, tt.expectedErr.Error())
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, res)
			}
		})
	}
}

func TestHover(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	tests := []struct {
		name        string
		expected    *protocol.Hover
		expectedErr error
		setupMocks  func(t *testing.T, c *controller, reg *MockRegistry)
	}{
		{
			name: "no symbol data",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).Return(protocol.Position{Line: 0, Character: 0}, false, nil)
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil)
				c.documents = documents

				reg.EXPECT().Hover(gomock.Any(), gomock.Any()).Return("", nil, nil)
			},
			expected: &protocol.Hover{},
		},
		{
			name: "has error return",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).Return(protocol.Position{Line: 0, Character: 0}, false, nil)
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil)
				c.documents = documents

				reg.EXPECT().Hover(gomock.Any(), gomock.Any()).Return("", nil, errors.New("test error"))
			},
			expected:    &protocol.Hover{},
			expectedErr: errors.New("test error"),
		},
		{
			name: "normal return",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).Return(protocol.Position{Line: 0, Character: 0}, false, nil)
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil).AnyTimes()
				c.documents = documents

				reg.EXPECT().Hover(gomock.Any(), gomock.Any()).Return("hello\nSCIP", &model.Occurrence{
					Range: []int32{1, 2, 3},
				}, nil)
				positionMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).DoAndReturn(func(pos protocol.Position) (protocol.Position, error) {
					return pos, nil
				}).Times(2)
			},
			expected: &protocol.Hover{
				Contents: protocol.MarkupContent{
					Kind:  "markdown",
					Value: "hello\nSCIP",
				},
				Range: &protocol.Range{
					Start: protocol.Position{
						Line:      1,
						Character: 2,
					},
					End: protocol.Position{
						Line:      1,
						Character: 3,
					},
				},
			},
		},
		{
			name: "shifted position in request",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).Return(protocol.Position{Line: 2, Character: 2}, false, nil)
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil).AnyTimes()
				c.documents = documents

				reg.EXPECT().Hover(gomock.Any(), gomock.Any()).Return("hello world", &model.Occurrence{
					Range: []int32{2, 2, 3},
				}, nil)
				positionMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).DoAndReturn(func(pos protocol.Position) (protocol.Position, error) {
					return pos, nil
				}).Times(2)
			},
			expected: &protocol.Hover{
				Contents: protocol.MarkupContent{
					Kind:  "markdown",
					Value: "hello world",
				},
				Range: &protocol.Range{
					Start: protocol.Position{
						Line:      2,
						Character: 2,
					},
					End: protocol.Position{
						Line:      2,
						Character: 3,
					},
				},
			},
		},
		{
			name: "shifted position in response",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).DoAndReturn(func(pos protocol.Position) (protocol.Position, bool, error) {
					return pos, false, nil
				})
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil).AnyTimes()
				c.documents = documents

				reg.EXPECT().Hover(gomock.Any(), gomock.Any()).Return("hello world", &model.Occurrence{
					Range: []int32{2, 2, 3},
				}, nil)
				positionMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).Return(protocol.Position{Line: 10, Character: 5}, nil)
				positionMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).Return(protocol.Position{Line: 10, Character: 8}, nil)
			},
			expected: &protocol.Hover{
				Contents: protocol.MarkupContent{
					Kind:  "markdown",
					Value: "hello world",
				},
				Range: &protocol.Range{
					Start: protocol.Position{
						Line:      10,
						Character: 5,
					},
					End: protocol.Position{
						Line:      10,
						Character: 8,
					},
				},
			},
		},
		{
			name: "new position",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).Return(protocol.Position{Line: 2, Character: 2}, true, nil)
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil)
				c.documents = documents
			},
			expected: &protocol.Hover{},
		},
		{
			name: "shifted position error",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).Return(protocol.Position{}, false, errors.New("test error"))
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil)
				c.documents = documents
			},
			expected:    &protocol.Hover{},
			expectedErr: errors.New("test error"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			c, reg := getMockedController(t, ctrl)

			req := &protocol.HoverParams{
				TextDocumentPositionParams: getMockTextDocumentPositionParams(),
			}

			tt.setupMocks(t, &c, reg)

			res := protocol.Hover{}
			err := c.hover(ctx, req, &res)

			if tt.expectedErr != nil {
				assert.ErrorContains(t, err, tt.expectedErr.Error())
			} else {
				assert.NoError(t, err)
				assert.Equal(t, *tt.expected, res)
			}
		})
	}
}

func TestGotoTypeDefinition(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	c, _ := getMockedController(t, ctrl)
	err := c.gotoTypeDefinition(ctx, nil, nil)

	assert.NoError(t, err)
}

func TestGotoImplementation(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	c, _ := getMockedController(t, ctrl)
	err := c.gotoImplementation(ctx, nil, nil)

	assert.NoError(t, err)
}

func TestReferences(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	tests := []struct {
		name        string
		setupMocks  func(t *testing.T, c *controller, reg *MockRegistry)
		expected    []protocol.Location
		expectedErr error
	}{
		{
			name: "no references",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).Return(protocol.Position{Line: 1, Character: 1}, false, nil)
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil)
				c.documents = documents

				reg.EXPECT().References(gomock.Any(), gomock.Any()).Return([]protocol.Location{}, nil)
			},
			expected: []protocol.Location{},
		},
		{
			name: "has error return",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).Return(protocol.Position{Line: 1, Character: 1}, false, nil)
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil)
				c.documents = documents

				reg.EXPECT().References(gomock.Any(), gomock.Any()).Return(nil, errors.New("test error"))
			},
			expected:    []protocol.Location{},
			expectedErr: errors.New("test error"),
		},
		{
			name: "normal return",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).Return(protocol.Position{Line: 1, Character: 1}, false, nil)
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil).AnyTimes()
				c.documents = documents

				reg.EXPECT().References(gomock.Any(), gomock.Any()).Return([]protocol.Location{
					{
						Range: mapper.ScipToProtocolRange([]int32{1, 2, 3}),
						URI:   protocol.DocumentURI("file:///test.go"),
					},
				}, nil)
				positionMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).DoAndReturn(func(pos protocol.Position) (protocol.Position, error) {
					return pos, nil
				}).Times(2)
			},
			expected: []protocol.Location{
				{
					Range: mapper.ScipToProtocolRange([]int32{1, 2, 3}),
					URI:   protocol.DocumentURI("file:///test.go"),
				},
			},
		},
		{
			name: "shifted symbol in request",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).Return(protocol.Position{Line: 2, Character: 2}, false, nil)
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil).AnyTimes()
				c.documents = documents

				reg.EXPECT().References(gomock.Any(), gomock.Any()).Return([]protocol.Location{
					{
						Range: mapper.ScipToProtocolRange([]int32{2, 2, 3}),
						URI:   protocol.DocumentURI("file:///test.go"),
					},
				}, nil)
				positionMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).DoAndReturn(func(pos protocol.Position) (protocol.Position, error) {
					return pos, nil
				}).Times(2)
			},
			expected: []protocol.Location{
				{
					Range: mapper.ScipToProtocolRange([]int32{2, 2, 3}),
					URI:   protocol.DocumentURI("file:///test.go"),
				},
			},
		},
		{
			name: "new position in request",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).Return(protocol.Position{Line: 2, Character: 2}, true, nil)
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil)
				c.documents = documents
			},
			expected: []protocol.Location{},
		},
		{
			name: "shifted symbol in response",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).Return(protocol.Position{Line: 2, Character: 2}, false, nil)
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil).AnyTimes()
				c.documents = documents

				reg.EXPECT().References(gomock.Any(), gomock.Any()).Return([]protocol.Location{
					{
						Range: mapper.ScipToProtocolRange([]int32{2, 2, 3}),
						URI:   protocol.DocumentURI("file:///test.go"),
					},
				}, nil)
				positionMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).Return(protocol.Position{Line: 3, Character: 4}, nil)
				positionMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).Return(protocol.Position{Line: 3, Character: 6}, nil)
			},
			expected: []protocol.Location{
				{
					Range: mapper.ScipToProtocolRange([]int32{3, 4, 6}),
					URI:   protocol.DocumentURI("file:///test.go"),
				},
			},
		},
		{
			name: "shifted position error in request",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).Return(protocol.Position{}, false, errors.New("test error"))
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil)
				c.documents = documents
			},
			expected:    nil,
			expectedErr: errors.New("test error"),
		},
		{
			name: "shifted position errors in response",
			setupMocks: func(t *testing.T, c *controller, reg *MockRegistry) {
				positionMapper := docsyncmock.NewMockPositionMapper(ctrl)
				positionMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).Return(protocol.Position{Line: 2, Character: 2}, false, nil)
				documents := docsyncmock.NewMockController(ctrl)
				documents.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(positionMapper, nil).AnyTimes()
				c.documents = documents

				reg.EXPECT().References(gomock.Any(), gomock.Any()).Return([]protocol.Location{
					{
						Range: mapper.ScipToProtocolRange([]int32{2, 2, 3}),
						URI:   protocol.DocumentURI("file:///test.go"),
					},
					{
						Range: mapper.ScipToProtocolRange([]int32{3, 2, 3}),
						URI:   protocol.DocumentURI("file:///test.go"),
					},
					{
						Range: mapper.ScipToProtocolRange([]int32{6, 2, 3}),
						URI:   protocol.DocumentURI("file:///test.go"),
					},
				}, nil)
				positionMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).Return(protocol.Position{}, errors.New("test error"))
				positionMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).Return(protocol.Position{Line: 4, Character: 2}, nil)
				positionMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).Return(protocol.Position{Line: 4, Character: 4}, nil)
				positionMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).Return(protocol.Position{Line: 6, Character: 6}, nil)
				positionMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).Return(protocol.Position{}, errors.New("test error"))
			},
			expected: []protocol.Location{
				{
					Range: mapper.ScipToProtocolRange([]int32{2, 2, 3}),
					URI:   protocol.DocumentURI("file:///test.go"),
				},
				{
					Range: mapper.ScipToProtocolRange([]int32{4, 2, 4}),
					URI:   protocol.DocumentURI("file:///test.go"),
				},
				{
					Range: mapper.ScipToProtocolRange([]int32{6, 2, 3}),
					URI:   protocol.DocumentURI("file:///test.go"),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			c, reg := getMockedController(t, ctrl)
			tt.setupMocks(t, &c, reg)

			req := &protocol.ReferenceParams{
				TextDocumentPositionParams: getMockTextDocumentPositionParams(),
			}

			res := []protocol.Location{}
			err := c.references(ctx, req, &res)

			if tt.expectedErr != nil {
				assert.ErrorContains(t, err, tt.expectedErr.Error())
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, res)
			}
		})
	}
}

func TestIndexReloading(t *testing.T) {
	t.Skip() // TODO @JamyDev: fix timing issues that block this test
	t.Run("basic reload", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		mockFs := fsmock.NewMockUlspFS(ctrl)
		mockReg := NewMockRegistry(ctrl)
		mockNotMgr := notifiermock.NewMockNotificationManager(ctrl)
		mockNotMgr.EXPECT().StartNotification(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("No notifier"))

		dir := t.TempDir()
		w, err := fsnotify.NewWatcher()
		assert.NoError(t, err)

		operationDone := make(chan struct{})

		c := &controller{
			registries:     map[string]Registry{},
			logger:         zap.NewNop().Sugar(),
			fs:             mockFs,
			watcher:        w,
			loadedIndices:  make(map[string]string),
			debounceTimers: make(map[string]*time.Timer),
			newScipRegistry: func(workspaceRoot, indexFolder string) Registry {
				return mockReg
			},
			indexNotifier: NewIndexNotifier(mockNotMgr),
		}

		done := make(chan bool, 1)
		closer := make(chan bool, 1)

		mockFs.EXPECT().ReadFile(gomock.Any()).Return([]byte("sampleSha"), nil)
		mockReg.EXPECT().LoadIndexFile(gomock.Any()).DoAndReturn(func(arg interface{}) error {
			defer close(operationDone)
			return nil
		})
		mockReg.EXPECT().SetDocumentLoadedCallback(gomock.Any())

		c.registries[dir] = c.createNewScipRegistry(dir, _monorepoNameJava)
		go func() {
			c.handleChanges(closer)
			done <- true
		}()

		scipFile := path.Join(dir, ".scip", "test.scip")
		c.watcher.Events <- fsnotify.Event{
			Name: scipFile,
			Op:   fsnotify.Create,
		}

		<-operationDone

		closer <- true
		<-done

		// Verify debounce timer was cleaned up
		c.debounceMu.Lock()
		_, exists := c.debounceTimers[scipFile]
		c.debounceMu.Unlock()
		assert.False(t, exists)
	})

	t.Run("debounce multiple rapid events", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		mockFs := fsmock.NewMockUlspFS(ctrl)
		mockReg := NewMockRegistry(ctrl)
		mockNotMgr := notifiermock.NewMockNotificationManager(ctrl)
		mockNotMgr.EXPECT().StartNotification(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("No notifier"))

		dir := t.TempDir()
		w, err := fsnotify.NewWatcher()
		assert.NoError(t, err)

		operationDone := make(chan struct{})

		c := &controller{
			registries:     map[string]Registry{},
			logger:         zap.NewNop().Sugar(),
			fs:             mockFs,
			watcher:        w,
			loadedIndices:  make(map[string]string),
			debounceTimers: make(map[string]*time.Timer),
			newScipRegistry: func(workspaceRoot, indexFolder string) Registry {
				return mockReg
			},
			indexNotifier: NewIndexNotifier(mockNotMgr),
		}

		done := make(chan bool, 1)
		closer := make(chan bool, 1)

		// Use a sync.Once to ensure operationDone is only closed once to prevent race conditions
		var operationOnce sync.Once

		// Expect only one reload despite multiple events, but allow for race conditions
		// where additional timer callbacks might fire after the first one completes
		mockFs.EXPECT().ReadFile(gomock.Any()).DoAndReturn(func(arg interface{}) ([]byte, error) {
			return []byte("sampleSha"), nil
		}).MinTimes(1).MaxTimes(2)
		mockReg.EXPECT().LoadIndexFile(gomock.Any()).DoAndReturn(func(arg interface{}) error {
			operationOnce.Do(func() { close(operationDone) })
			return nil
		}).MinTimes(1).MaxTimes(2)
		mockReg.EXPECT().SetDocumentLoadedCallback(gomock.Any())

		c.registries[dir] = c.createNewScipRegistry(dir, _monorepoNameJava)
		go func() {
			c.handleChanges(closer)
			done <- true
		}()

		scipFile := path.Join(dir, ".scip", "test.scip")
		// Send multiple rapid events
		for i := 0; i < 5; i++ {
			c.watcher.Events <- fsnotify.Event{
				Name: scipFile,
				Op:   fsnotify.Create,
			}
		}

		<-operationDone

		closer <- true
		<-done

		c.debounceMu.Lock()
		_, exists := c.debounceTimers[scipFile]
		c.debounceMu.Unlock()
		assert.False(t, exists)
	})

	t.Run("cleanup debounce timers on close", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		mockFs := fsmock.NewMockUlspFS(ctrl)
		mockFs.EXPECT().ReadFile(gomock.Any()).Return([]byte("sampleSha"), nil).AnyTimes()
		mockReg := NewMockRegistry(ctrl)
		mockReg.EXPECT().LoadIndexFile(gomock.Any()).Return(nil).AnyTimes()
		mockNotMgr := notifiermock.NewMockNotificationManager(ctrl)
		mockNotMgr.EXPECT().StartNotification(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("No notifier"))

		dir := t.TempDir()
		w, err := fsnotify.NewWatcher()
		assert.NoError(t, err)

		c := &controller{
			registries:     map[string]Registry{},
			logger:         zap.NewNop().Sugar(),
			fs:             mockFs,
			watcher:        w,
			loadedIndices:  make(map[string]string),
			debounceTimers: make(map[string]*time.Timer),
			newScipRegistry: func(workspaceRoot, indexFolder string) Registry {
				return mockReg
			},
			indexNotifier: NewIndexNotifier(mockNotMgr),
		}

		done := make(chan bool, 1)
		closer := make(chan bool, 1)

		mockReg.EXPECT().SetDocumentLoadedCallback(gomock.Any())
		c.registries[dir] = c.createNewScipRegistry(dir, _monorepoNameJava)
		go func() {
			c.handleChanges(closer)
			done <- true
		}()

		scipFile := path.Join(dir, ".scip", "test.scip")
		// Send event but close before debounce timeout
		c.watcher.Events <- fsnotify.Event{
			Name: scipFile,
			Op:   fsnotify.Create,
		}
		closer <- true
		<-done
		c.debounceMu.Lock()
		assert.Empty(t, c.debounceTimers, "all debounce timers should be cleaned up")
		c.debounceMu.Unlock()
	})

	t.Run("reload index", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		operationDone := make(chan struct{})
		mockFs := fsmock.NewMockUlspFS(ctrl)
		mockFs.EXPECT().ReadFile(gomock.Any()).DoAndReturn(func(arg interface{}) ([]byte, error) {
			return []byte("sampleSha"), nil
		}).Times(1)
		mockReg := NewMockRegistry(ctrl)
		mockNotMgr := notifiermock.NewMockNotificationManager(ctrl)
		mockNotHndlr := notifiermock.NewMockNotificationHandler(ctrl)
		mockNotHndlr.EXPECT().Done(gomock.Any()).DoAndReturn(func(ctx context.Context) bool {
			close(operationDone)
			return true
		}).Return()
		notChan := make(chan notifier.Notification, 10)
		mockNotHndlr.EXPECT().Channel().Return(notChan)
		mockNotMgr.EXPECT().StartNotification(gomock.Any(), gomock.Any(), gomock.Any()).Return(mockNotHndlr, nil)
		diagnostics := diagnosticsmock.NewMockController(ctrl)
		diagnostics.EXPECT().ApplyDiagnostics(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		documents := docsyncmock.NewMockController(ctrl)
		documents.EXPECT().ResetBase(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

		sampleWorkspaceRoot := path.Join("/sample/home/", string(_monorepoNameJava))
		sessionRepository := repositorymock.NewMockRepository(ctrl)
		s := &entity.Session{
			UUID: factory.UUID(),
		}
		s.WorkspaceRoot = sampleWorkspaceRoot
		s.Monorepo = _monorepoNameJava
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil).AnyTimes()

		dir := t.TempDir()
		w, err := fsnotify.NewWatcher()
		assert.NoError(t, err)

		c := &controller{
			configs: map[entity.MonorepoName]entity.MonorepoConfigEntry{"_default": {
				Scip: entity.ScipConfig{
					LoadFromDirectories: true,
					Directories: []string{
						".scip",
					},
				},
			}},
			sessions:      sessionRepository,
			registries:    map[string]Registry{},
			logger:        zap.NewNop().Sugar(),
			initialLoad:   make(chan bool, 1),
			fs:            mockFs,
			watcher:       w,
			loadedIndices: make(map[string]string),
			diagnostics:   diagnostics,
			documents:     documents,
			newScipRegistry: func(workspaceRoot, indexFolder string) Registry {
				return mockReg
			},
			debounceTimers: make(map[string]*time.Timer),
			indexNotifier:  NewIndexNotifier(mockNotMgr),
		}

		done := make(chan bool, 1)
		closer := make(chan bool, 1)
		cb := func(doc *model.Document) {}

		mockReg.EXPECT().LoadIndexFile(gomock.Any()).Return(nil).Times(1)
		mockReg.EXPECT().GetURI(gomock.Any()).Return(uri.File("file:///test.go"))
		mockReg.EXPECT().SetDocumentLoadedCallback(gomock.Any()).DoAndReturn(func(callback func(*model.Document)) {
			cb = callback
		})

		c.registries[dir] = c.createNewScipRegistry(dir, _monorepoNameJava)
		go func() {
			c.handleChanges(closer)
			assert.NotEmpty(t, notChan)
			done <- true
		}()

		// Send only the relevant SCIP file event
		scipFile := path.Join(dir, ".scip", "mockfile.scip")
		c.watcher.Events <- fsnotify.Event{
			Name: scipFile,
			Op:   fsnotify.Create,
		}
		<-operationDone

		cb(&model.Document{
			RelativePath: "test.go",
		})

		closer <- true
		<-done
	})
}

func TestHandleChangesErrorConfig(t *testing.T) {
	ctrl := gomock.NewController(t)

	mockFs := fsmock.NewMockUlspFS(ctrl)
	mockReg := NewMockRegistry(ctrl)
	sampleWorkspaceRoot := path.Join("/sample/home/", string(_monorepoNameJava))
	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	s.WorkspaceRoot = sampleWorkspaceRoot
	s.Monorepo = _monorepoNameJava
	sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil).AnyTimes()

	dir := t.TempDir()
	w, err := fsnotify.NewWatcher()
	assert.NoError(t, err)

	c := &controller{
		configs:  map[entity.MonorepoName]entity.MonorepoConfigEntry{},
		sessions: sessionRepository,
		registries: map[string]Registry{
			dir: mockReg,
		},
		logger:      zap.NewNop().Sugar(),
		initialLoad: make(chan bool, 1),
		fs:          mockFs,
		watcher:     w,
	}

	done := make(chan bool, 1)
	closer := make(chan bool, 1)

	go func() {
		c.handleChanges(closer)
		done <- true
	}()

	c.watcher.Errors <- errors.New("test error")

	c.watcher.Events <- fsnotify.Event{
		Name: path.Join(dir, ".scip", "mockfile.meta"),
		Op:   fsnotify.Create,
	}

	closer <- true
	<-done
}

func TestHandleChangesErrorSessionsObject(t *testing.T) {
	ctrl := gomock.NewController(t)

	mockFs := fsmock.NewMockUlspFS(ctrl)
	mockReg := NewMockRegistry(ctrl)
	sampleWorkspaceRoot := path.Join("/sample/home/", string(_monorepoNameJava))
	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	s.WorkspaceRoot = sampleWorkspaceRoot
	s.Monorepo = _monorepoNameJava
	sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(nil, errors.New("whoops")).AnyTimes()

	dir := t.TempDir()
	w, err := fsnotify.NewWatcher()
	assert.NoError(t, err)

	c := &controller{
		configs:  map[entity.MonorepoName]entity.MonorepoConfigEntry{},
		sessions: sessionRepository,
		registries: map[string]Registry{
			dir: mockReg,
		},
		logger:      zap.NewNop().Sugar(),
		initialLoad: make(chan bool, 1),
		fs:          mockFs,
		watcher:     w,
	}

	done := make(chan bool, 1)
	closer := make(chan bool, 1)

	go func() {
		c.handleChanges(closer)
		done <- true
	}()

	c.watcher.Errors <- errors.New("test error")

	c.watcher.Events <- fsnotify.Event{
		Name: path.Join(dir, ".scip", "mockfile.meta"),
		Op:   fsnotify.Create,
	}

	closer <- true
	<-done
}

func TestNotifier(t *testing.T) {
	t.Run("no existing index", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ctx := context.Background()

		sampleWorkspaceRoot := path.Join("/sample/home/", string(_monorepoNameJava))
		sessionRepository := repositorymock.NewMockRepository(ctrl)
		s := &entity.Session{
			UUID: factory.UUID(),
		}
		s.WorkspaceRoot = sampleWorkspaceRoot
		s.Monorepo = _monorepoNameJava
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil).AnyTimes()

		fsMock := fsmock.NewMockUlspFS(ctrl)
		fsMock.EXPECT().ReadDir(gomock.Any()).Return([]os.DirEntry{}, nil)
		mockReg := NewMockRegistry(ctrl)
		mockReg.EXPECT().LoadConcurrency().Return(1)
		mockReg.EXPECT().SetDocumentLoadedCallback(gomock.Any())
		mockNotMgr := notifiermock.NewMockNotificationManager(ctrl)

		c := &controller{
			configs: map[entity.MonorepoName]entity.MonorepoConfigEntry{"_default": {
				Scip: entity.ScipConfig{
					LoadFromDirectories: true,
					Directories: []string{
						".scip",
					},
				},
			}},
			sessions:      sessionRepository,
			registries:    map[string]Registry{},
			logger:        zap.NewNop().Sugar(),
			initialLoad:   make(chan bool, 1),
			fs:            fsMock,
			loadedIndices: make(map[string]string),
			newScipRegistry: func(workspaceRoot string, indexFolder string) Registry {
				return mockReg
			},
			indexNotifier: NewIndexNotifier(mockNotMgr),
		}
		err := c.initialize(ctx, &protocol.InitializeParams{}, &protocol.InitializeResult{})
		assert.NoError(t, err)
		err = c.initialized(ctx, &protocol.InitializedParams{})

		assert.NoError(t, err)
	})
	t.Run("initial load", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ctx := context.Background()

		sampleWorkspaceRoot := path.Join("/sample/home/", string(_monorepoNameJava))
		sessionRepository := repositorymock.NewMockRepository(ctrl)
		s := &entity.Session{
			UUID: factory.UUID(),
		}
		s.WorkspaceRoot = sampleWorkspaceRoot
		s.Monorepo = _monorepoNameJava
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil).AnyTimes()

		regMock := NewMockRegistry(ctrl)
		regMock.EXPECT().LoadConcurrency().Return(1)
		regMock.EXPECT().LoadIndexFile(gomock.Any()).Return(nil)
		regMock.EXPECT().SetDocumentLoadedCallback(gomock.Any())
		fsMock := fsmock.NewMockUlspFS(ctrl)
		fsMock.EXPECT().ReadDir(gomock.Any()).Return([]os.DirEntry{
			NewMockDirEntry("sample.scip"),
			NewMockDirEntry("sample.meta"),
		}, nil)
		fsMock.EXPECT().ReadFile(gomock.Any()).Return([]byte("sampleSha"), nil)

		mockNotMgr := notifiermock.NewMockNotificationManager(ctrl)
		mockNotHndlr := notifiermock.NewMockNotificationHandler(ctrl)
		mockNotHndlr.EXPECT().Done(gomock.Any()).Return()
		notChan := make(chan notifier.Notification, 10)
		mockNotHndlr.EXPECT().Channel().Return(notChan).AnyTimes()
		mockNotMgr.EXPECT().StartNotification(gomock.Any(), gomock.Any(), gomock.Any()).Return(mockNotHndlr, nil)

		c := &controller{
			configs: map[entity.MonorepoName]entity.MonorepoConfigEntry{"_default": {
				Scip: entity.ScipConfig{
					LoadFromDirectories: true,
					Directories: []string{
						".scip",
					},
				},
			}},
			sessions:      sessionRepository,
			registries:    map[string]Registry{},
			logger:        zap.NewNop().Sugar(),
			initialLoad:   make(chan bool, 1),
			fs:            fsMock,
			loadedIndices: make(map[string]string),
			newScipRegistry: func(workspaceRoot string, indexFolder string) Registry {
				return regMock
			},
			indexNotifier: NewIndexNotifier(mockNotMgr),
		}
		err := c.initialize(ctx, &protocol.InitializeParams{}, &protocol.InitializeResult{})
		assert.NoError(t, err)
		err = c.initialized(ctx, &protocol.InitializedParams{})

		assert.NoError(t, err)
	})
	t.Run("has failed loads", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ctx := context.Background()

		sampleWorkspaceRoot := path.Join("/sample/home/", string(_monorepoNameJava))
		sessionRepository := repositorymock.NewMockRepository(ctrl)
		s := &entity.Session{
			UUID: factory.UUID(),
		}
		s.WorkspaceRoot = sampleWorkspaceRoot
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil).AnyTimes()

		regMock := NewMockRegistry(ctrl)
		regMock.EXPECT().LoadConcurrency().Return(1)
		regMock.EXPECT().SetDocumentLoadedCallback(gomock.Any())
		regMock.EXPECT().LoadIndexFile(gomock.Any()).Return(errors.New("failed to open"))
		fsMock := fsmock.NewMockUlspFS(ctrl)
		fsMock.EXPECT().ReadDir(gomock.Any()).Return([]os.DirEntry{
			NewMockDirEntry("sample.scip"),
		}, nil)
		fsMock.EXPECT().ReadFile(gomock.Any()).Return([]byte("sampleSha"), nil)

		mockNotMgr := notifiermock.NewMockNotificationManager(ctrl)
		mockNotHndlr := notifiermock.NewMockNotificationHandler(ctrl)
		mockNotHndlr.EXPECT().Done(gomock.Any()).Return()
		notChan := make(chan notifier.Notification, 10)
		mockNotHndlr.EXPECT().Channel().Return(notChan).AnyTimes()
		mockNotMgr.EXPECT().StartNotification(gomock.Any(), gomock.Any(), gomock.Any()).Return(mockNotHndlr, nil)

		mockGateway := ideclientmock.NewMockGateway(ctrl)
		mockGateway.EXPECT().ShowMessage(gomock.Any(), gomock.Any()).Return(nil)

		c := &controller{
			configs: map[entity.MonorepoName]entity.MonorepoConfigEntry{"_default": {
				Scip: entity.ScipConfig{
					LoadFromDirectories: true,
					Directories: []string{
						".scip",
					},
				},
			}},
			sessions:    sessionRepository,
			registries:  map[string]Registry{},
			logger:      zap.NewNop().Sugar(),
			initialLoad: make(chan bool, 1),
			fs:          fsMock,
			ideGateway:  mockGateway,
			newScipRegistry: func(workspaceRoot string, indexFolder string) Registry {
				return regMock
			},
			indexNotifier: NewIndexNotifier(mockNotMgr),
		}
		err := c.initialize(ctx, &protocol.InitializeParams{}, &protocol.InitializeResult{})
		assert.NoError(t, err)
		err = c.initialized(ctx, &protocol.InitializedParams{})

		assert.NoError(t, err)
	})
	t.Run("reload index", func(t *testing.T) {
		t.Skip() // TODO @JamyDev: fix timing issues that block this test
		ctrl := gomock.NewController(t)
		operationDone := make(chan struct{})

		mockFs := fsmock.NewMockUlspFS(ctrl)
		mockFs.EXPECT().ReadFile(gomock.Any()).Return([]byte("sampleSha"), nil)
		mockReg := NewMockRegistry(ctrl)
		mockNotMgr := notifiermock.NewMockNotificationManager(ctrl)
		mockNotHndlr := notifiermock.NewMockNotificationHandler(ctrl)
		mockNotHndlr.EXPECT().Done(gomock.Any()).DoAndReturn(func(ctx context.Context) bool {
			close(operationDone)
			return true
		}).Return()
		notChan := make(chan notifier.Notification, 10)
		mockNotHndlr.EXPECT().Channel().Return(notChan)
		mockNotMgr.EXPECT().StartNotification(gomock.Any(), gomock.Any(), gomock.Any()).Return(mockNotHndlr, nil)
		diagnostics := diagnosticsmock.NewMockController(ctrl)
		diagnostics.EXPECT().ApplyDiagnostics(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		documents := docsyncmock.NewMockController(ctrl)
		documents.EXPECT().ResetBase(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

		sampleWorkspaceRoot := path.Join("/sample/home/", string(_monorepoNameJava))
		sessionRepository := repositorymock.NewMockRepository(ctrl)
		s := &entity.Session{
			UUID: factory.UUID(),
		}
		s.WorkspaceRoot = sampleWorkspaceRoot
		s.Monorepo = _monorepoNameJava

		dir := t.TempDir()
		w, err := fsnotify.NewWatcher()
		assert.NoError(t, err)

		c := &controller{
			configs: map[entity.MonorepoName]entity.MonorepoConfigEntry{"_default": {
				Scip: entity.ScipConfig{
					LoadFromDirectories: true,
					Directories: []string{
						".scip",
					},
				},
			}},
			sessions:      sessionRepository,
			registries:    map[string]Registry{},
			logger:        zap.NewNop().Sugar(),
			initialLoad:   make(chan bool, 1),
			fs:            mockFs,
			watcher:       w,
			loadedIndices: make(map[string]string),
			diagnostics:   diagnostics,
			documents:     documents,
			newScipRegistry: func(workspaceRoot, indexFolder string) Registry {
				return mockReg
			},
			debounceTimers: make(map[string]*time.Timer),
			indexNotifier:  NewIndexNotifier(mockNotMgr),
		}

		done := make(chan bool, 1)
		closer := make(chan bool, 1)
		cb := func(doc *model.Document) {}

		mockReg.EXPECT().LoadIndexFile(gomock.Any()).Return(nil).Times(1)
		mockReg.EXPECT().GetURI(gomock.Any()).Return(uri.File("file:///test.go"))
		mockReg.EXPECT().SetDocumentLoadedCallback(gomock.Any()).DoAndReturn(func(callback func(*model.Document)) {
			cb = callback
		})

		c.registries[dir] = c.createNewScipRegistry(dir, _monorepoNameJava)
		go func() {
			c.handleChanges(closer)
			assert.NotEmpty(t, notChan)
			done <- true
		}()

		c.watcher.Errors <- errors.New("test error")

		c.watcher.Events <- fsnotify.Event{
			Name: path.Join(dir, ".scip", "mockfile.meta"),
			Op:   fsnotify.Create,
		}

		c.watcher.Events <- fsnotify.Event{
			Name: "asdf",
			Op:   fsnotify.Chmod,
		}

		c.watcher.Events <- fsnotify.Event{
			Name: path.Join(".scip", "mockfile.scip"),
			Op:   fsnotify.Create,
		}

		c.watcher.Events <- fsnotify.Event{
			Name: path.Join(dir, ".scip", "mockfile.scip"),
			Op:   fsnotify.Create,
		}
		<-operationDone

		cb(&model.Document{
			RelativePath: "test.go",
		})

		closer <- true
		<-done
	})

	t.Run("reload index failed notification", func(t *testing.T) {
		t.Skip() // TODO @JamyDev: fix timing issues that block this test
		ctrl := gomock.NewController(t)
		mockFs := fsmock.NewMockUlspFS(ctrl)
		operationDone := make(chan struct{})
		mockFs.EXPECT().ReadFile(gomock.Any()).DoAndReturn(func(arg interface{}) ([]byte, error) {
			return []byte("sampleSha"), nil
		})
		mockReg := NewMockRegistry(ctrl)
		mockNotMgr := notifiermock.NewMockNotificationManager(ctrl)
		mockNotHndlr := notifiermock.NewMockNotificationHandler(ctrl)
		mockNotHndlr.EXPECT().Done(gomock.Any()).Return()
		notChan := make(chan notifier.Notification, 10)
		mockNotHndlr.EXPECT().Channel().Return(notChan)
		mockNotMgr.EXPECT().StartNotification(gomock.Any(), gomock.Any(), gomock.Any()).Return(mockNotHndlr, nil)

		mockIdeGw := ideclientmock.NewMockGateway(ctrl)
		mockIdeGw.EXPECT().ShowMessage(gomock.Any(), gomock.Any()).
			Do(func(ctx context.Context, params *protocol.ShowMessageParams) bool {
				defer close(operationDone)
				return true
			}).
			Return(nil)
		diagnostics := diagnosticsmock.NewMockController(ctrl)
		diagnostics.EXPECT().ApplyDiagnostics(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		documents := docsyncmock.NewMockController(ctrl)
		documents.EXPECT().ResetBase(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

		sampleWorkspaceRoot := path.Join("/sample/home/", string(_monorepoNameJava))
		sessionRepository := repositorymock.NewMockRepository(ctrl)
		s := &entity.Session{
			UUID: factory.UUID(),
		}
		s.WorkspaceRoot = sampleWorkspaceRoot
		s.Monorepo = _monorepoNameJava
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil).AnyTimes()

		dir := t.TempDir()
		w, err := fsnotify.NewWatcher()
		assert.NoError(t, err)

		c := &controller{
			configs: map[entity.MonorepoName]entity.MonorepoConfigEntry{"_default": {
				Scip: entity.ScipConfig{
					LoadFromDirectories: true,
					Directories: []string{
						".scip",
					},
				},
			}},
			registries: map[string]Registry{
				dir: mockReg,
			},
			sessions:       sessionRepository,
			ideGateway:     mockIdeGw,
			logger:         zap.NewNop().Sugar(),
			initialLoad:    make(chan bool, 1),
			fs:             mockFs,
			watcher:        w,
			loadedIndices:  make(map[string]string),
			diagnostics:    diagnostics,
			documents:      documents,
			debounceTimers: make(map[string]*time.Timer),
			newScipRegistry: func(workspaceRoot, indexFolder string) Registry {
				return mockReg
			},
			indexNotifier: NewIndexNotifier(mockNotMgr),
		}

		done := make(chan bool, 1)
		closer := make(chan bool, 1)
		cb := func(doc *model.Document) {}
		mockReg.EXPECT().GetURI(gomock.Any()).Return(uri.File("file:///test.go"))
		mockReg.EXPECT().LoadIndexFile(gomock.Any()).Return(errors.New("whoopsie"))
		mockReg.EXPECT().SetDocumentLoadedCallback(gomock.Any()).DoAndReturn(func(callback func(*model.Document)) {
			cb = callback
		})
		c.registries[dir] = c.createNewScipRegistry(dir, _monorepoNameJava)
		go func() {
			c.handleChanges(closer)
			assert.NotEmpty(t, notChan)
			done <- true
		}()

		c.watcher.Events <- fsnotify.Event{
			Name: path.Join(dir, ".scip", "mockfile.scip"),
			Op:   fsnotify.Create,
		}
		<-operationDone

		cb(&model.Document{
			RelativePath: "test.go",
		})

		closer <- true
		<-done
	})
}

func TestGetSha(t *testing.T) {
	t.Run("sha read success", func(t *testing.T) {
		scipFile := "sample.scip"
		ctrl := gomock.NewController(t)

		mockFs := fsmock.NewMockUlspFS(ctrl)
		mockReg := NewMockRegistry(ctrl)

		dir := t.TempDir()
		sampleWorkspaceRoot := path.Join(dir, string(_monorepoNameJava))
		s := &entity.Session{
			UUID: factory.UUID(),
		}
		s.WorkspaceRoot = sampleWorkspaceRoot
		s.Monorepo = _monorepoNameJava

		c := &controller{
			configs: map[entity.MonorepoName]entity.MonorepoConfigEntry{"_default": {
				Scip: entity.ScipConfig{
					LoadFromDirectories: true,
					Directories: []string{
						".scip",
					},
				},
			}},
			registries: map[string]Registry{
				sampleWorkspaceRoot: mockReg,
			},
			logger:        zap.NewNop().Sugar(),
			initialLoad:   make(chan bool, 1),
			fs:            mockFs,
			loadedIndices: make(map[string]string),
		}

		expectedSha := "1234567890abcdef"
		mockFs.EXPECT().ReadFile(gomock.Any()).Return([]byte(expectedSha), nil).Times(1)

		sha, err := c.getSha(path.Join(s.WorkspaceRoot, scipFile))
		assert.NoError(t, err)
		assert.Equal(t, expectedSha, sha)
	})

	t.Run("readfile failures success", func(t *testing.T) {
		scipFile := "sample.scip"
		ctrl := gomock.NewController(t)

		mockFs := fsmock.NewMockUlspFS(ctrl)
		mockReg := NewMockRegistry(ctrl)

		dir := t.TempDir()
		sampleWorkspaceRoot := path.Join(dir, string(_monorepoNameJava))
		s := &entity.Session{
			UUID: factory.UUID(),
		}
		s.WorkspaceRoot = sampleWorkspaceRoot
		s.Monorepo = _monorepoNameJava

		c := &controller{
			configs: map[entity.MonorepoName]entity.MonorepoConfigEntry{"_default": {
				Scip: entity.ScipConfig{
					LoadFromDirectories: true,
					Directories: []string{
						".scip",
					},
				},
			}},
			registries: map[string]Registry{
				sampleWorkspaceRoot: mockReg,
			},
			logger:        zap.NewNop().Sugar(),
			initialLoad:   make(chan bool, 1),
			fs:            mockFs,
			loadedIndices: make(map[string]string),
		}

		readFileErr := errors.New("read file error")
		mockFs.EXPECT().ReadFile(gomock.Any()).Return(nil, readFileErr).Times(1)

		sha, err := c.getSha(path.Join(s.WorkspaceRoot, scipFile))
		assert.Error(t, err)
		assert.Equal(t, _noHash, sha)

	})
}
func TestControllerGetDocumentSymbols(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		uri         string
		sessionErr  error
		overrideWSR string
		registry    Registry
		expected    []*model.SymbolOccurrence
		expectedErr error
	}{
		{
			name:        "session error",
			uri:         "file:///test.go",
			sessionErr:  errors.New("session error"),
			expected:    nil,
			expectedErr: errors.New("session error"),
		},
		{
			name:        "no registry",
			uri:         "file:///test.go",
			sessionErr:  nil,
			registry:    nil,
			expected:    nil,
			expectedErr: nil,
		},
		{
			name:       "registry returns document symbols",
			uri:        "file:///test.go",
			sessionErr: nil,
			registry: func() Registry {
				reg := NewMockRegistry(gomock.NewController(t))
				reg.EXPECT().DocumentSymbols(uri.URI("file:///test.go")).Return([]*model.SymbolOccurrence{
					{
						Info: &model.SymbolInformation{
							DisplayName: "test",
						},
						Occurrence: &model.Occurrence{
							Range: []int32{1, 1, 1, 1},
						},
						Location: protocol.URI("file:///test.go"),
					},
					{
						Info: &model.SymbolInformation{
							DisplayName: "test2",
						},
						Occurrence: &model.Occurrence{
							Range: []int32{1, 1, 1, 1},
						},
						Location: protocol.URI("file:///test.go"),
					},
				}, nil)
				return reg
			}(),
			expected: []*model.SymbolOccurrence{
				{
					Info: &model.SymbolInformation{
						DisplayName: "test",
					},
					Occurrence: &model.Occurrence{
						Range: []int32{1, 1, 1, 1},
					},
					Location: protocol.URI("file:///test.go"),
				},
				{
					Info: &model.SymbolInformation{
						DisplayName: "test2",
					},
					Occurrence: &model.Occurrence{
						Range: []int32{1, 1, 1, 1},
					},
					Location: protocol.URI("file:///test.go"),
				},
			},
			expectedErr: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			sessionRepository := repositorymock.NewMockRepository(ctrl)
			s := &entity.Session{
				UUID: factory.UUID(),
			}
			s.WorkspaceRoot = "/some/path"
			sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, tt.sessionErr).AnyTimes()

			c := &controller{
				sessions: sessionRepository,
				registries: map[string]Registry{
					"/some/path": tt.registry,
				},
				logger: zap.NewNop().Sugar(),
			}

			result, err := c.GetDocumentSymbols(ctx, tt.overrideWSR, tt.uri)

			if tt.expectedErr != nil {
				assert.ErrorContains(t, err, tt.expectedErr.Error())
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}
func TestControllerGetSymbolDefinitionOccurrence(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		descriptors []model.Descriptor
		overrideWSR string
		setupMocks  func(*gomock.Controller) (controller, *MockRegistry)
		expected    *model.SymbolOccurrence
		expectedErr error
	}{
		{
			name: "no session",
			descriptors: []model.Descriptor{
				{
					Name:   "test",
					Suffix: scip.Descriptor_Namespace,
				},
				{
					Name:   "symbol",
					Suffix: scip.Descriptor_Type,
				},
			},
			overrideWSR: "",
			setupMocks: func(ctrl *gomock.Controller) (controller, *MockRegistry) {
				sessionRepo := repositorymock.NewMockRepository(ctrl)
				sessionRepo.EXPECT().GetFromContext(gomock.Any()).Return(nil, errors.New("no session")).AnyTimes()
				return controller{
					sessions: sessionRepo,
					logger:   zap.NewNop().Sugar(),
				}, nil
			},
			expected:    nil,
			expectedErr: errors.New("no session"),
		},
		{
			name:        "no registry",
			descriptors: []model.Descriptor{},
			setupMocks: func(ctrl *gomock.Controller) (controller, *MockRegistry) {
				sessionRepo := repositorymock.NewMockRepository(ctrl)
				s := &entity.Session{
					UUID: factory.UUID(),
				}
				s.WorkspaceRoot = "/some/path"
				sessionRepo.EXPECT().GetFromContext(gomock.Any()).Return(s, nil).AnyTimes()
				return controller{
					sessions:   sessionRepo,
					logger:     zap.NewNop().Sugar(),
					registries: map[string]Registry{},
				}, nil
			},
			expected:    nil,
			expectedErr: nil,
		},
		{
			name: "registry returns symbol definition occurrence",
			descriptors: []model.Descriptor{
				{
					Name:   "test",
					Suffix: scip.Descriptor_Namespace,
				},
				{
					Name:   "symbol",
					Suffix: scip.Descriptor_Type,
				},
			},
			setupMocks: func(ctrl *gomock.Controller) (controller, *MockRegistry) {
				sessionRepo := repositorymock.NewMockRepository(ctrl)
				s := &entity.Session{
					UUID: factory.UUID(),
				}
				s.WorkspaceRoot = "/some/path"
				sessionRepo.EXPECT().GetFromContext(gomock.Any()).Return(s, nil).AnyTimes()

				reg := NewMockRegistry(ctrl)
				reg.EXPECT().GetSymbolDefinitionOccurrence([]model.Descriptor{
					{
						Name:   "test",
						Suffix: scip.Descriptor_Namespace,
					},
					{
						Name:   "symbol",
						Suffix: scip.Descriptor_Type,
					},
				}, gomock.Any()).Return(&model.SymbolOccurrence{
					Info: &model.SymbolInformation{
						DisplayName: "testPkg",
					},
					Occurrence: &model.Occurrence{
						Range: []int32{1, 1, 1, 1},
					},
					Location: protocol.URI("file:///test.go"),
				}, nil)

				return controller{
					sessions: sessionRepo,
					logger:   zap.NewNop().Sugar(),
					registries: map[string]Registry{
						"/some/path": reg,
					},
				}, reg
			},
			expected: &model.SymbolOccurrence{
				Info: &model.SymbolInformation{
					DisplayName: "testPkg",
				},
				Occurrence: &model.Occurrence{
					Range: []int32{1, 1, 1, 1},
				},
				Location: protocol.URI("file:///test.go"),
			},
			expectedErr: nil,
		},
		{
			name: "registry returns nil",
			descriptors: []model.Descriptor{
				{
					Name:   "test",
					Suffix: scip.Descriptor_Namespace,
				},
				{
					Name:   "symbol",
					Suffix: scip.Descriptor_Type,
				},
			},
			setupMocks: func(ctrl *gomock.Controller) (controller, *MockRegistry) {
				sessionRepo := repositorymock.NewMockRepository(ctrl)
				s := &entity.Session{
					UUID: factory.UUID(),
				}
				s.WorkspaceRoot = "/some/path"
				sessionRepo.EXPECT().GetFromContext(gomock.Any()).Return(s, nil).AnyTimes()

				reg := NewMockRegistry(ctrl)
				reg.EXPECT().GetSymbolDefinitionOccurrence([]model.Descriptor{
					{
						Name:   "test",
						Suffix: scip.Descriptor_Namespace,
					},
					{
						Name:   "symbol",
						Suffix: scip.Descriptor_Type,
					},
				}, gomock.Any()).Return(nil, nil)

				return controller{
					sessions: sessionRepo,
					logger:   zap.NewNop().Sugar(),
					registries: map[string]Registry{
						"/some/path": reg,
					},
				}, reg
			},
			expected:    nil,
			expectedErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			c, _ := tt.setupMocks(ctrl)

			result, err := c.GetSymbolDefinitionOccurrence(ctx, tt.overrideWSR, tt.descriptors, ".")

			if tt.expectedErr != nil {
				assert.ErrorContains(t, err, tt.expectedErr.Error())
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestGetBasePosition(t *testing.T) {
	ctrl := gomock.NewController(t)
	tests := []struct {
		name           string
		setupMocks     func(t *testing.T) docsync.Controller
		doc            protocol.TextDocumentIdentifier
		pos            protocol.Position
		expectedPos    protocol.Position
		expectedErrStr string
	}{
		{
			name: "document not found - returns original position",
			setupMocks: func(t *testing.T) docsync.Controller {
				docSync := docsyncmock.NewMockController(ctrl)
				docSync.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(nil, nil)
				return docSync
			},
			doc:         protocol.TextDocumentIdentifier{URI: "file:///test.go"},
			pos:         protocol.Position{Line: 10, Character: 20},
			expectedPos: protocol.Position{Line: 10, Character: 20},
		},
		{
			name: "mapper error - returns error",
			setupMocks: func(t *testing.T) docsync.Controller {
				docSync := docsyncmock.NewMockController(ctrl)
				docSync.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("mapper error"))
				return docSync
			},
			doc:            protocol.TextDocumentIdentifier{URI: "file:///test.go"},
			pos:            protocol.Position{Line: 10, Character: 20},
			expectedErrStr: "getting position mapper: mapper error",
		},
		{
			name: "successful mapping - position changed",
			setupMocks: func(t *testing.T) docsync.Controller {
				docSync := docsyncmock.NewMockController(ctrl)
				posMapper := docsyncmock.NewMockPositionMapper(ctrl)

				posMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).Return(protocol.Position{Line: 5, Character: 15}, false, nil)
				docSync.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(posMapper, nil)

				return docSync
			},
			doc:         protocol.TextDocumentIdentifier{URI: "file:///test.go"},
			pos:         protocol.Position{Line: 10, Character: 20},
			expectedPos: protocol.Position{Line: 5, Character: 15},
		},
		{
			name: "successful mapping - no position change",
			setupMocks: func(t *testing.T) docsync.Controller {
				docSync := docsyncmock.NewMockController(ctrl)
				posMapper := docsyncmock.NewMockPositionMapper(ctrl)

				// Position maps to itself (no changes in document)
				posMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).Return(protocol.Position{Line: 10, Character: 20}, false, nil)
				docSync.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(posMapper, nil)

				return docSync
			},
			doc:         protocol.TextDocumentIdentifier{URI: "file:///test.go"},
			pos:         protocol.Position{Line: 10, Character: 20},
			expectedPos: protocol.Position{Line: 10, Character: 20},
		},
		{
			name: "mapping error - returns error",
			setupMocks: func(t *testing.T) docsync.Controller {
				docSync := docsyncmock.NewMockController(ctrl)
				posMapper := docsyncmock.NewMockPositionMapper(ctrl)

				posMapper.EXPECT().MapCurrentPositionToBase(gomock.Any()).Return(protocol.Position{}, false, fmt.Errorf("mapping error"))
				docSync.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(posMapper, nil)

				return docSync
			},
			doc:            protocol.TextDocumentIdentifier{URI: "file:///test.go"},
			pos:            protocol.Position{Line: 10, Character: 20},
			expectedErrStr: "mapping position to base: mapping error",
		},
		{
			name: "document not tracked - returns original position",
			setupMocks: func(t *testing.T) docsync.Controller {
				docSync := docsyncmock.NewMockController(ctrl)
				docSync.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(nil, nil)
				return docSync
			},
			doc:         protocol.TextDocumentIdentifier{URI: "file:///untracked.go"},
			pos:         protocol.Position{Line: 30, Character: 40},
			expectedPos: protocol.Position{Line: 30, Character: 40},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &controller{
				documents: tt.setupMocks(t),
			}

			pos, err := c.getBasePosition(context.Background(), tt.doc, tt.pos)

			if tt.expectedErrStr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErrStr)
				return
			}

			require.NoError(t, err)
			if pos != nil {
				assert.Equal(t, tt.expectedPos, *pos)
			} else {
				assert.Nil(t, pos)
			}
		})
	}
}

func TestGetLatestPosition(t *testing.T) {
	ctrl := gomock.NewController(t)
	tests := []struct {
		name           string
		setupMocks     func(t *testing.T) docsync.Controller
		doc            protocol.TextDocumentIdentifier
		pos            protocol.Position
		expectedPos    protocol.Position
		expectedErrStr string
	}{
		{
			name: "document not found - returns original position",
			setupMocks: func(t *testing.T) docsync.Controller {
				docSync := docsyncmock.NewMockController(ctrl)
				docSync.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(nil, nil)
				return docSync
			},
			doc:         protocol.TextDocumentIdentifier{URI: "file:///test.go"},
			pos:         protocol.Position{Line: 10, Character: 20},
			expectedPos: protocol.Position{Line: 10, Character: 20},
		},
		{
			name: "mapper error - returns error",
			setupMocks: func(t *testing.T) docsync.Controller {
				docSync := docsyncmock.NewMockController(ctrl)
				docSync.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("mapper error"))
				return docSync
			},
			doc:            protocol.TextDocumentIdentifier{URI: "file:///test.go"},
			pos:            protocol.Position{Line: 10, Character: 20},
			expectedErrStr: "getting position mapper: mapper error",
		},
		{
			name: "successful mapping - position changed",
			setupMocks: func(t *testing.T) docsync.Controller {
				docSync := docsyncmock.NewMockController(ctrl)
				posMapper := docsyncmock.NewMockPositionMapper(ctrl)

				posMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).Return(protocol.Position{Line: 15, Character: 25}, nil)
				docSync.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(posMapper, nil)

				return docSync
			},
			doc:         protocol.TextDocumentIdentifier{URI: "file:///test.go"},
			pos:         protocol.Position{Line: 10, Character: 20},
			expectedPos: protocol.Position{Line: 15, Character: 25},
		},
		{
			name: "successful mapping - no position change",
			setupMocks: func(t *testing.T) docsync.Controller {
				docSync := docsyncmock.NewMockController(ctrl)
				posMapper := docsyncmock.NewMockPositionMapper(ctrl)

				// Position maps to itself (no changes in document)
				posMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).Return(protocol.Position{Line: 10, Character: 20}, nil)
				docSync.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(posMapper, nil)

				return docSync
			},
			doc:         protocol.TextDocumentIdentifier{URI: "file:///test.go"},
			pos:         protocol.Position{Line: 10, Character: 20},
			expectedPos: protocol.Position{Line: 10, Character: 20},
		},
		{
			name: "mapping error - returns error",
			setupMocks: func(t *testing.T) docsync.Controller {
				docSync := docsyncmock.NewMockController(ctrl)
				posMapper := docsyncmock.NewMockPositionMapper(ctrl)

				posMapper.EXPECT().MapBasePositionToCurrent(gomock.Any()).Return(protocol.Position{}, fmt.Errorf("mapping error"))
				docSync.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(posMapper, nil)

				return docSync
			},
			doc:            protocol.TextDocumentIdentifier{URI: "file:///test.go"},
			pos:            protocol.Position{Line: 10, Character: 20},
			expectedErrStr: "mapping position to current: mapping error",
		},
		{
			name: "document not tracked - returns original position",
			setupMocks: func(t *testing.T) docsync.Controller {
				docSync := docsyncmock.NewMockController(ctrl)
				docSync.EXPECT().GetPositionMapper(gomock.Any(), gomock.Any()).Return(nil, nil)
				return docSync
			},
			doc:         protocol.TextDocumentIdentifier{URI: "file:///untracked.go"},
			pos:         protocol.Position{Line: 30, Character: 40},
			expectedPos: protocol.Position{Line: 30, Character: 40},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &controller{
				documents: tt.setupMocks(t),
			}

			pos, err := c.getLatestPosition(context.Background(), tt.doc, tt.pos)

			if tt.expectedErrStr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErrStr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedPos, pos)
		})
	}
}
func TestController_DidOpen(t *testing.T) {
	tests := []struct {
		name          string
		setupMocks    func(mockSesh *repositorymock.MockRepository, mockReg *MockRegistry) (*entity.Session, error)
		params        *protocol.DidOpenTextDocumentParams
		expectedError string
	}{
		{
			name: "successful did open",
			setupMocks: func(mockSesh *repositorymock.MockRepository, mockReg *MockRegistry) (*entity.Session, error) {
				sesh := &entity.Session{
					WorkspaceRoot: "/test/workspace",
				}
				mockSesh.EXPECT().GetFromContext(gomock.Any()).Return(sesh, nil)
				mockReg.EXPECT().DidOpen(uri.URI("file:///test.go"), "test content").Return(nil)
				return sesh, nil
			},
			params: &protocol.DidOpenTextDocumentParams{
				TextDocument: protocol.TextDocumentItem{
					URI:  "file:///test.go",
					Text: "test content",
				},
			},
		},
		{
			name: "session error",
			setupMocks: func(mockSesh *repositorymock.MockRepository, mockReg *MockRegistry) (*entity.Session, error) {
				mockSesh.EXPECT().GetFromContext(gomock.Any()).Return(nil, assert.AnError)
				return nil, assert.AnError
			},
			params: &protocol.DidOpenTextDocumentParams{
				TextDocument: protocol.TextDocumentItem{
					URI:  "file:///test.go",
					Text: "test content",
				},
			},
			expectedError: assert.AnError.Error(),
		},
		{
			name: "no registry for workspace",
			setupMocks: func(mockSesh *repositorymock.MockRepository, mockReg *MockRegistry) (*entity.Session, error) {
				sesh := &entity.Session{
					WorkspaceRoot: "/different/workspace",
				}
				mockSesh.EXPECT().GetFromContext(gomock.Any()).Return(sesh, nil)
				return sesh, nil
			},
			params: &protocol.DidOpenTextDocumentParams{
				TextDocument: protocol.TextDocumentItem{
					URI:  "file:///test.go",
					Text: "test content",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockSesh := repositorymock.NewMockRepository(ctrl)
			mockReg := NewMockRegistry(ctrl)

			_, _ = tt.setupMocks(mockSesh, mockReg)

			c := &controller{
				sessions: mockSesh,
				registries: map[string]Registry{
					"/test/workspace": mockReg,
				},
			}
			err := c.didOpen(context.Background(), tt.params)

			if tt.expectedError != "" {
				assert.EqualError(t, err, tt.expectedError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
