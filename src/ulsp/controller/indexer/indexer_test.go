package indexer

import (
	"context"
	"fmt"
	"os"
	"testing"

	"go.uber.org/config"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tally "github.com/uber-go/tally"
	"github.com/uber/scip-lsp/src/ulsp/controller/doc-sync/docsyncmock"
	"github.com/uber/scip-lsp/src/ulsp/entity"
	"github.com/uber/scip-lsp/src/ulsp/factory"
	"github.com/uber/scip-lsp/src/ulsp/gateway/ide-client/ideclientmock"
	"github.com/uber/scip-lsp/src/ulsp/internal/fs/fsmock"
	"github.com/uber/scip-lsp/src/ulsp/internal/logfilewriter"
	"github.com/uber/scip-lsp/src/ulsp/internal/serverinfofile/serverinfofilemock"
	"github.com/uber/scip-lsp/src/ulsp/repository/session/repositorymock"
	"go.lsp.dev/protocol"
	"go.uber.org/fx/fxtest"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
)

func TestNew(t *testing.T) {
	mockConfig, _ := config.NewStaticProvider(map[string]interface{}{})
	assert.NotPanics(t, func() {
		New(Params{
			Logger: zap.NewNop().Sugar(),
			Stats:  tally.NewTestScope("testing", make(map[string]string, 0)),
			Config: mockConfig,
		})
	})
}

func TestStartupInfo(t *testing.T) {
	ctx := context.Background()
	c := controller{}
	result, err := c.StartupInfo(ctx)

	assert.NoError(t, err)
	assert.NoError(t, result.Validate())
	assert.Equal(t, _nameKey, result.NameKey)
}

func TestInitialize(t *testing.T) {
	id := factory.UUID()
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	s.UUID = id
	s.WorkspaceRoot = "/home/fievel"

	tempFile, err := os.CreateTemp("", "test")
	require.NoError(t, err)
	defer os.Remove(tempFile.Name())

	infoFile := serverinfofilemock.NewMockServerInfoFile(ctrl)
	infoFile.EXPECT().UpdateField(gomock.Any(), gomock.Any()).Return(nil)

	fs := fsmock.NewMockUlspFS(ctrl)
	fs.EXPECT().MkdirAll(gomock.Any()).Return(nil)
	fs.EXPECT().TempFile(gomock.Any(), gomock.Any()).Return(tempFile, nil)

	monorepoConfig := entity.MonorepoConfigs{
		"": {
			Languages: []string{"java"},
		},
	}

	c := controller{
		sessions: sessionRepository,
		config:   monorepoConfig,
		outputWriterParams: logfilewriter.Params{
			Lifecycle:      fxtest.NewLifecycle(t),
			ServerInfoFile: infoFile,
			FS:             fs,
		},
	}

	initParams := &protocol.InitializeParams{}
	initResult := &protocol.InitializeResult{}

	t.Run("success", func(t *testing.T) {
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		err := c.initialize(ctx, initParams, initResult)
		assert.NoError(t, err)
		assert.NotNil(t, c.indexer[id])
	})

	t.Run("initialize error", func(t *testing.T) {
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(nil, fmt.Errorf("sample error"))
		err := c.initialize(ctx, initParams, initResult)
		assert.Error(t, err)
	})
}

func TestInitialized(t *testing.T) {
	ctx := context.Background()
	c := controller{}
	err := c.initialized(ctx, nil)
	assert.NoError(t, err)
}

func TestDidOpen(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	id := factory.UUID()
	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	s.UUID = id
	s.WorkspaceRoot = "/home/fievel"
	documents := docsyncmock.NewMockController(ctrl)
	indexerMock := NewMockIndexer(gomock.NewController(t))
	params := &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI: protocol.DocumentURI("file:///home/fievel/target/src/main.java"),
		},
	}
	key := s.UUID.String() + "_" + "target/..."
	ideGatewayMock := ideclientmock.NewMockGateway(ctrl)

	t.Run("sync index success", func(t *testing.T) {
		scope := tally.NewTestScope("testing", make(map[string]string, 0))
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		indexerMock.EXPECT().SyncIndex(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		indexerMock.EXPECT().IsRelevantDocument(gomock.Any()).Return(true)
		indexerMock.EXPECT().GetUniqueIndexKey(gomock.Any()).Return(key, nil)
		ideGatewayMock.EXPECT().WorkDoneProgressCreate(gomock.Any(), gomock.Any()).Return(nil)
		ideGatewayMock.EXPECT().Progress(gomock.Any(), gomock.Any()).Return(nil).Return(nil).AnyTimes()

		c := controller{
			sessions:  sessionRepository,
			documents: documents,
			indexer: map[uuid.UUID]Indexer{
				id: indexerMock,
			},
			ideGateway: ideGatewayMock,
			stats:      scope,
		}
		err := c.didOpen(ctx, params)
		assert.NoError(t, err)

		counters := scope.Snapshot().Counters()
		assert.Equal(t, int64(1), counters["testing.events+"].Value())
		assert.Equal(t, int64(1), counters["testing.runs+"].Value())
		assert.Equal(t, int64(1), counters["testing.success+"].Value())
		assert.Nil(t, counters["testing.cancelled+"])
		assert.Nil(t, counters["testing.failed+"])
	})

	t.Run("sync index failure", func(t *testing.T) {
		scope := tally.NewTestScope("testing", make(map[string]string, 0))
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(nil, fmt.Errorf("sample error"))
		c := controller{
			sessions:  sessionRepository,
			documents: documents,
			stats:     scope,
		}
		err := c.didOpen(ctx, params)
		assert.Error(t, err)
		assertSkippedIndexStats(t, scope)
	})
}

func TestDidSave(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	id := factory.UUID()
	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	s.UUID = id
	s.WorkspaceRoot = "/home/fievel"
	key := s.UUID.String() + "_" + "target/..."
	documents := docsyncmock.NewMockController(ctrl)
	indexerMock := NewMockIndexer(gomock.NewController(t))
	params := &protocol.DidSaveTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{
			URI: protocol.DocumentURI("file:///home/fievel/target/src/main.java"),
		},
	}
	doc := protocol.TextDocumentItem{
		URI: protocol.DocumentURI("file:///home/fievel/target/src/main.java"),
	}
	ideGatewayMock := ideclientmock.NewMockGateway(ctrl)

	t.Run("sync index success", func(t *testing.T) {
		scope := tally.NewTestScope("testing", make(map[string]string, 0))
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		documents.EXPECT().GetTextDocument(gomock.Any(), gomock.Any()).Return(doc, nil)
		indexerMock.EXPECT().SyncIndex(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		indexerMock.EXPECT().IsRelevantDocument(gomock.Any()).Return(true)
		indexerMock.EXPECT().GetUniqueIndexKey(gomock.Any()).Return(key, nil)
		ideGatewayMock.EXPECT().WorkDoneProgressCreate(gomock.Any(), gomock.Any()).Return(nil)
		ideGatewayMock.EXPECT().Progress(gomock.Any(), gomock.Any()).Return(nil).Return(nil).AnyTimes()

		c := controller{
			sessions:  sessionRepository,
			documents: documents,
			indexer: map[uuid.UUID]Indexer{
				id: indexerMock,
			},
			ideGateway: ideGatewayMock,
			stats:      scope,
		}
		err := c.didSave(ctx, params)
		assert.NoError(t, err)

		counters := scope.Snapshot().Counters()
		assert.Equal(t, int64(1), counters["testing.events+"].Value())
		assert.Equal(t, int64(1), counters["testing.runs+"].Value())
		assert.Equal(t, int64(1), counters["testing.success+"].Value())
		assert.Nil(t, counters["testing.cancelled+"])
		assert.Nil(t, counters["testing.failed+"])
	})

	t.Run("sync index failure", func(t *testing.T) {
		scope := tally.NewTestScope("testing", make(map[string]string, 0))
		documents.EXPECT().GetTextDocument(gomock.Any(), gomock.Any()).Return(doc, nil)
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(nil, fmt.Errorf("sample error"))
		c := controller{
			sessions:  sessionRepository,
			documents: documents,
			stats:     scope,
		}
		err := c.didSave(ctx, params)
		assert.Error(t, err)
		assertSkippedIndexStats(t, scope)
	})

	t.Run("get document error", func(t *testing.T) {
		documents.EXPECT().GetTextDocument(gomock.Any(), gomock.Any()).Return(doc, fmt.Errorf("sample error"))
		c := controller{
			documents: documents,
		}
		err := c.didSave(ctx, params)
		assert.Error(t, err)
	})
}

func TestSyncIndex(t *testing.T) {
	id := factory.UUID()
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	s.UUID = id
	s.WorkspaceRoot = "/home/fievel"
	key := s.UUID.String() + "_" + "target/..."
	doc := protocol.TextDocumentItem{
		URI: protocol.DocumentURI("file:///home/fievel/target/src/main.java"),
	}

	indexerMock := NewMockIndexer(gomock.NewController(t))
	ideGatewayMock := ideclientmock.NewMockGateway(ctrl)

	t.Run("success", func(t *testing.T) {
		scope := tally.NewTestScope("testing", make(map[string]string, 0))
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		indexerMock.EXPECT().SyncIndex(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		indexerMock.EXPECT().IsRelevantDocument(gomock.Any()).Return(true)
		indexerMock.EXPECT().GetUniqueIndexKey(gomock.Any()).Return(key, nil)
		ideGatewayMock.EXPECT().WorkDoneProgressCreate(gomock.Any(), gomock.Any()).Return(nil)
		ideGatewayMock.EXPECT().Progress(gomock.Any(), gomock.Any()).Return(nil).Return(nil).AnyTimes()

		c := controller{
			sessions: sessionRepository,
			indexer: map[uuid.UUID]Indexer{
				id: indexerMock,
			},
			ideGateway: ideGatewayMock,
			stats:      scope,
		}
		err := c.syncIndex(ctx, doc)
		assert.NoError(t, err)

		counters := scope.Snapshot().Counters()
		assert.Equal(t, int64(1), counters["testing.events+"].Value())
		assert.Equal(t, int64(1), counters["testing.runs+"].Value())
		assert.Equal(t, int64(1), counters["testing.success+"].Value())
		assert.Nil(t, counters["testing.cancelled+"])
		assert.Nil(t, counters["testing.failed+"])
	})

	t.Run("multiple indexing events", func(t *testing.T) {
		scope := tally.NewTestScope("testing", make(map[string]string, 0))
		sessionRepositoryLocal := repositorymock.NewMockRepository(ctrl)
		ideGatewayMockLocal := ideclientmock.NewMockGateway(ctrl)
		indexerMockLocal := NewMockIndexer(gomock.NewController(t))

		sessionRepositoryLocal.EXPECT().GetFromContext(gomock.Any()).Return(s, nil).AnyTimes()

		c := controller{
			sessions: sessionRepositoryLocal,
			indexer: map[uuid.UUID]Indexer{
				id: indexerMockLocal,
			},
			ideGateway:  ideGatewayMockLocal,
			stats:       scope,
			logger:      zap.NewNop().Sugar(),
			pendingCmds: pendingCmdStore{},
		}
		firstIndexStarted := make(chan struct{})
		firstIndexCanComplete := make(chan struct{})
		reindexStarted := make(chan struct{})
		firstIndexResult := make(chan error, 1) // Channel to safely communicate the error

		callCount := 0
		indexerMockLocal.EXPECT().SyncIndex(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, exec interface{}, gw interface{}, logger interface{}, doc interface{}) error {
			callCount++
			if callCount == 1 {
				close(firstIndexStarted)
				<-firstIndexCanComplete
			} else {
				close(reindexStarted)
			}
			return nil
		}).Times(2)

		indexerMockLocal.EXPECT().IsRelevantDocument(gomock.Any()).Return(true).Times(2)
		indexerMockLocal.EXPECT().GetUniqueIndexKey(gomock.Any()).Return(key, nil).Times(2)
		ideGatewayMockLocal.EXPECT().WorkDoneProgressCreate(gomock.Any(), gomock.Any()).Return(nil).Times(2)
		ideGatewayMockLocal.EXPECT().Progress(gomock.Any(), gomock.Any()).Return(nil).Times(4)

		go func() {
			err := c.syncIndex(ctx, doc)
			firstIndexResult <- err // Send result through channel
		}()

		<-firstIndexStarted

		err := c.syncIndex(ctx, doc)
		assert.NoError(t, err)

		counters := scope.Snapshot().Counters()
		assert.Equal(t, int64(2), counters["testing.events+"].Value())
		assert.Equal(t, int64(1), counters["testing.runs+"].Value())
		assert.Equal(t, int64(1), counters["testing.reindex_queued+"].Value())

		close(firstIndexCanComplete)

		<-reindexStarted

		// Safely read the error from the channel
		firstErr := <-firstIndexResult
		assert.NoError(t, firstErr)

		finalCounters := scope.Snapshot().Counters()
		assert.Equal(t, int64(2), finalCounters["testing.events+"].Value())
		assert.Equal(t, int64(2), finalCounters["testing.runs+"].Value())
		assert.Equal(t, int64(2), finalCounters["testing.success+"].Value())
		assert.Equal(t, int64(1), finalCounters["testing.reindex_queued+"].Value())
	})

	t.Run("irrelevant document", func(t *testing.T) {
		scope := tally.NewTestScope("testing", make(map[string]string, 0))
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		indexerMock.EXPECT().IsRelevantDocument(gomock.Any()).Return(false)

		c := controller{
			sessions: sessionRepository,
			indexer: map[uuid.UUID]Indexer{
				id: indexerMock,
			},
			stats: scope,
		}
		err := c.syncIndex(ctx, doc)
		assert.NoError(t, err)
		assertSkippedIndexStats(t, scope)
	})

	t.Run("key error", func(t *testing.T) {
		scope := tally.NewTestScope("testing", make(map[string]string, 0))
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		indexerMock.EXPECT().IsRelevantDocument(gomock.Any()).Return(true)
		indexerMock.EXPECT().GetUniqueIndexKey(gomock.Any()).Return("", fmt.Errorf("key error"))

		c := controller{
			sessions: sessionRepository,
			indexer: map[uuid.UUID]Indexer{
				id: indexerMock,
			},
			logger: zap.NewNop().Sugar(),
			stats:  scope,
		}
		err := c.syncIndex(ctx, doc)
		assert.Nil(t, err)
		assert.NoError(t, err)
		assertSkippedIndexStats(t, scope)
	})

	t.Run("indexing in progress", func(t *testing.T) {
		scope := tally.NewTestScope("testing", make(map[string]string, 0))
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		indexerMock.EXPECT().IsRelevantDocument(gomock.Any()).Return(true)
		indexerMock.EXPECT().GetUniqueIndexKey(gomock.Any()).Return(key, nil)

		c := controller{
			sessions:    sessionRepository,
			pendingCmds: pendingCmdStore{},
			ideGateway:  ideGatewayMock,
			indexer: map[uuid.UUID]Indexer{
				id: indexerMock,
			},
			logger: zap.NewNop().Sugar(),
			stats:  scope,
		}

		c.pendingCmds.setPendingCmd(key, func() {})
		err := c.syncIndex(ctx, doc)
		assert.Nil(t, err)
		assert.NoError(t, err)

		counters := scope.Snapshot().Counters()
		assert.Equal(t, int64(1), counters["testing.events+"].Value())
		assert.Equal(t, int64(1), counters["testing.reindex_queued+"].Value())
		assert.Nil(t, counters["testing.runs+"])

		// Verify the file was marked for reindexing
		assert.True(t, c.pendingCmds.needsReindexing(key))
	})

	t.Run("session error", func(t *testing.T) {
		scope := tally.NewTestScope("testing", make(map[string]string, 0))
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(nil, fmt.Errorf("sample error"))
		c := controller{
			sessions: sessionRepository,
			stats:    scope,
		}
		err := c.syncIndex(ctx, doc)
		assert.Error(t, err)
		assertSkippedIndexStats(t, scope)
	})

	t.Run("no indexer", func(t *testing.T) {
		scope := tally.NewTestScope("testing", make(map[string]string, 0))
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		c := controller{
			sessions: sessionRepository,
			stats:    scope,
		}
		err := c.syncIndex(ctx, doc)
		assert.NoError(t, err)
		assertSkippedIndexStats(t, scope)
	})

	t.Run("indexing error", func(t *testing.T) {
		scope := tally.NewTestScope("testing", make(map[string]string, 0))
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		indexerMock.EXPECT().SyncIndex(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(fmt.Errorf("sample error"))
		indexerMock.EXPECT().IsRelevantDocument(gomock.Any()).Return(true)
		indexerMock.EXPECT().GetUniqueIndexKey(gomock.Any()).Return(key, nil)

		ideGatewayMock.EXPECT().WorkDoneProgressCreate(gomock.Any(), gomock.Any()).Return(nil)
		ideGatewayMock.EXPECT().Progress(gomock.Any(), gomock.Any()).Return(nil).Return(nil).AnyTimes()

		c := controller{
			sessions: sessionRepository,
			indexer: map[uuid.UUID]Indexer{
				id: indexerMock,
			},
			stats:       scope,
			pendingCmds: pendingCmdStore{},
			ideGateway:  ideGatewayMock,
			logger:      zap.NewNop().Sugar(),
		}
		err := c.syncIndex(ctx, doc)
		assert.Error(t, err)

		counters := scope.Snapshot().Counters()
		assert.Equal(t, int64(1), counters["testing.events+"].Value())
		assert.Equal(t, int64(1), counters["testing.runs+"].Value())
		assert.Equal(t, int64(1), counters["testing.failed+"].Value())
		assert.Nil(t, counters["testing.cancelled+"])
		assert.Nil(t, counters["testing.success+"])
	})
}

func TestDidClose(t *testing.T) {
	ctx := context.Background()
	c := controller{}
	dcParams := &protocol.DidCloseTextDocumentParams{}
	err := c.didClose(ctx, dcParams)

	assert.NoError(t, err)
}

func TestEndSession(t *testing.T) {
	ctx := context.Background()
	c := controller{}
	u := uuid.Must(uuid.NewV4())
	err := c.endSession(ctx, u)

	assert.NoError(t, err)
}

func TestWorkDoneProgressCancel(t *testing.T) {
	ctx := context.Background()
	scope := tally.NewTestScope("testing", make(map[string]string, 0))

	t.Run("key not found", func(t *testing.T) {
		c := controller{
			pendingCmds: pendingCmdStore{},
			logger:      zap.NewNop().Sugar(),
			stats:       scope,
		}
		params := &protocol.WorkDoneProgressCancelParams{
			Token: *protocol.NewProgressToken("s1_file.java"),
		}
		err := c.workDoneProgressCancel(ctx, params)
		assert.NoError(t, err)
	})

	t.Run("success", func(t *testing.T) {
		c := controller{
			pendingCmds: pendingCmdStore{},
			logger:      zap.NewNop().Sugar(),
			stats:       scope,
		}
		token := c.pendingCmds.setPendingCmd("s1_file.java", func() {})
		params := &protocol.WorkDoneProgressCancelParams{
			Token: *protocol.NewProgressToken(token),
		}
		err := c.workDoneProgressCancel(ctx, params)
		assert.NoError(t, err)

		counters := scope.Snapshot().Counters()
		assert.Equal(t, int64(1), counters["testing.cancelled+"].Value())
	})
}

func TestSendIndexStartNotification(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	ideGatewayMock := ideclientmock.NewMockGateway(ctrl)
	c := controller{
		ideGateway: ideGatewayMock,
	}
	token := protocol.NewProgressToken("dummytoken")

	t.Run("success", func(t *testing.T) {
		ideGatewayMock.EXPECT().WorkDoneProgressCreate(gomock.Any(), gomock.Any()).Return(nil)
		ideGatewayMock.EXPECT().Progress(gomock.Any(), gomock.Any()).Return(nil)
		err := c.sendIndexStartNotification(ctx, *token, "dummy message")
		assert.NoError(t, err)
	})

	t.Run("error", func(t *testing.T) {
		ideGatewayMock.EXPECT().WorkDoneProgressCreate(gomock.Any(), gomock.Any()).Return(fmt.Errorf("sample error"))
		err := c.sendIndexStartNotification(ctx, *token, "dummy message")
		assert.Error(t, err)
	})
}

func TestSendIndexEndNotification(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	ideGatewayMock := ideclientmock.NewMockGateway(ctrl)
	c := controller{
		ideGateway: ideGatewayMock,
	}
	ideGatewayMock.EXPECT().Progress(gomock.Any(), gomock.Any()).Return(nil)
	token := protocol.NewProgressToken("dummytoken")
	err := c.sendIndexEndNotification(ctx, *token)
	assert.NoError(t, err)
}

func TestUpdateEnv(t *testing.T) {
	tests := []struct {
		name     string
		env      []string
		wsRoot   string
		expected []string
	}{
		{
			name:     "Append both WORKSPACE_ROOT and PROJECT_ROOT",
			env:      []string{"EXAMPLE_VAR=value"},
			wsRoot:   "/new/path",
			expected: []string{"EXAMPLE_VAR=value", "WORKSPACE_ROOT=/new/path", "PROJECT_ROOT=/new/path"},
		},
		{
			name:     "Update existing WORKSPACE_ROOT and PROJECT_ROOT",
			env:      []string{"WORKSPACE_ROOT=/old/path", "PROJECT_ROOT=/old/path"},
			wsRoot:   "/new/path",
			expected: []string{"WORKSPACE_ROOT=/new/path", "PROJECT_ROOT=/new/path"},
		},
		{
			name:     "Update WORKSPACE_ROOT and append PROJECT_ROOT",
			env:      []string{"WORKSPACE_ROOT=/old/path", "EXAMPLE_VAR=value"},
			wsRoot:   "/new/path",
			expected: []string{"WORKSPACE_ROOT=/new/path", "EXAMPLE_VAR=value", "PROJECT_ROOT=/new/path"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updatedEnv := UpdateEnv(tt.env, tt.wsRoot)
			assert.Equal(t, tt.expected, updatedEnv)
		})
	}
}

func assertSkippedIndexStats(t *testing.T, scope tally.TestScope) {
	counters := scope.Snapshot().Counters()
	assert.Equal(t, int64(1), counters["testing.events+"].Value())
	assert.Nil(t, counters["testing.runs+"])
	assert.Nil(t, counters["testing.cancelled+"])
	assert.Nil(t, counters["testing.failed+"])
	assert.Nil(t, counters["testing.success+"])
}
