package indexer

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"go.uber.org/config"

	"github.com/gofrs/uuid"
	tally "github.com/uber-go/tally/v4"
	docsync "github.com/uber/scip-lsp/src/ulsp/controller/doc-sync"
	userguidance "github.com/uber/scip-lsp/src/ulsp/controller/user-guidance"
	"github.com/uber/scip-lsp/src/ulsp/entity"
	ulspplugin "github.com/uber/scip-lsp/src/ulsp/entity/ulsp-plugin"
	ideclient "github.com/uber/scip-lsp/src/ulsp/gateway/ide-client"
	"github.com/uber/scip-lsp/src/ulsp/internal/executor"
	"github.com/uber/scip-lsp/src/ulsp/internal/fs"
	"github.com/uber/scip-lsp/src/ulsp/internal/logfilewriter"
	"github.com/uber/scip-lsp/src/ulsp/internal/serverinfofile"
	"github.com/uber/scip-lsp/src/ulsp/repository/session"
	"go.lsp.dev/protocol"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

const (
	_javaLang    = "java"
	_nameKey     = "indexer"
	_syncMessage = "Syncing index for file: %s"
)

// Indexer defines the interface for an indexer.
type Indexer interface {
	// SyncIndex syncs the index for the given document.
	SyncIndex(ctx context.Context, executor executor.Executor, ideGateway ideclient.Gateway, logger *zap.SugaredLogger, doc protocol.TextDocumentItem) error

	// IsRelevantDocument returns true if the document is relevant for this indexer.
	// This is where you specify which language to support for indexing
	IsRelevantDocument(document protocol.TextDocumentItem) bool

	// GetUniqueIndexKey returns a unique key for the document.
	// This will be used to check if any indexing is in progress
	GetUniqueIndexKey(document protocol.TextDocumentItem) (string, error)
}

// Params defines the dependencies that will be available ot this controller.
type Params struct {
	fx.In

	Sessions       session.Repository
	Logger         *zap.SugaredLogger
	Config         config.Provider
	Documents      docsync.Controller
	Guidance       userguidance.Controller
	Stats          tally.Scope
	Executor       executor.Executor
	IdeGateway     ideclient.Gateway
	FS             fs.UlspFS
	Lifecycle      fx.Lifecycle
	ServerInfoFile serverinfofile.ServerInfoFile
}

// Controller defines the methods that this controller provides.
type Controller interface {
	StartupInfo(ctx context.Context) (ulspplugin.PluginInfo, error)
}

type controller struct {
	sessions           session.Repository
	logger             *zap.SugaredLogger
	config             entity.MonorepoConfigs
	documents          docsync.Controller
	executor           executor.Executor
	ideGateway         ideclient.Gateway
	stats              tally.Scope
	outputWriterParams logfilewriter.Params

	indexerOutputWriter io.Writer
	pendingCmds         pendingCmdStore
	indexer             map[uuid.UUID]Indexer
}

// New controller for indexer
func New(p Params) Controller {
	configs := entity.MonorepoConfigs{}
	if err := p.Config.Get(entity.MonorepoConfigKey).Populate(&configs); err != nil {
		panic(fmt.Sprintf("getting configuration for %q: %v", entity.MonorepoConfigKey, err))
	}

	c := &controller{
		sessions:    p.Sessions,
		logger:      p.Logger.With("plugin", _nameKey),
		config:      configs,
		documents:   p.Documents,
		executor:    p.Executor,
		ideGateway:  p.IdeGateway,
		stats:       p.Stats.SubScope(_nameKey),
		indexer:     make(map[uuid.UUID]Indexer),
		pendingCmds: pendingCmdStore{},

		outputWriterParams: logfilewriter.Params{
			Lifecycle:      p.Lifecycle,
			ServerInfoFile: p.ServerInfoFile,
			FS:             p.FS,
		},
	}
	return c
}

// StartupInfo returns PluginInfo for this controller.
func (c *controller) StartupInfo(ctx context.Context) (ulspplugin.PluginInfo, error) {
	// Set a priority for each method that this module provides.
	priorities := map[string]ulspplugin.Priority{
		protocol.MethodInitialize:           ulspplugin.PriorityRegular,
		protocol.MethodInitialized:          ulspplugin.PriorityAsync,
		protocol.MethodTextDocumentDidOpen:  ulspplugin.PriorityAsync,
		protocol.MethodTextDocumentDidSave:  ulspplugin.PriorityAsync,
		protocol.MethodTextDocumentDidClose: ulspplugin.PriorityAsync,
		ulspplugin.MethodEndSession:         ulspplugin.PriorityRegular,

		protocol.MethodWorkDoneProgressCancel: ulspplugin.PriorityRegular,
	}

	// Assign method keys to implementations.
	methods := &ulspplugin.Methods{
		PluginNameKey: _nameKey,

		Initialize:  c.initialize,
		Initialized: c.initialized,
		DidSave:     c.didSave,
		DidOpen:     c.didOpen,
		DidClose:    c.didClose,
		EndSession:  c.endSession,

		WorkDoneProgressCancel: c.workDoneProgressCancel,
	}

	return ulspplugin.PluginInfo{
		Priorities:    priorities,
		Methods:       methods,
		NameKey:       _nameKey,
		RelevantRepos: c.config.RelevantJavaRepos(),
	}, nil
}

func (c *controller) initialize(ctx context.Context, params *protocol.InitializeParams, result *protocol.InitializeResult) error {
	s, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}

	if c.indexer == nil {
		c.indexer = make(map[uuid.UUID]Indexer)
	}

	// Indexer is currently only supported in Java monorepositories
	if c.config[s.Monorepo].EnableJavaSupport() {
		if c.indexerOutputWriter == nil {
			var err error
			c.indexerOutputWriter, err = logfilewriter.SetupOutputWriter(c.outputWriterParams, _logFileKey)
			if err != nil {
				return fmt.Errorf("setting up log file: %w", err)
			}
		}
		c.indexer[s.UUID] = NewJavaIndexer(c.outputWriterParams.FS, s, c.indexerOutputWriter)
	}
	return nil
}

func (c *controller) initialized(ctx context.Context, params *protocol.InitializedParams) error {
	return nil
}

func (c *controller) didOpen(ctx context.Context, params *protocol.DidOpenTextDocumentParams) error {
	return c.syncIndex(ctx, params.TextDocument)
}

func (c *controller) didSave(ctx context.Context, params *protocol.DidSaveTextDocumentParams) error {
	doc, err := c.documents.GetTextDocument(ctx, params.TextDocument)
	if err != nil {
		return err
	}
	return c.syncIndex(ctx, doc)
}

func (c *controller) didClose(ctx context.Context, params *protocol.DidCloseTextDocumentParams) error {
	return nil
}

func (c *controller) endSession(ctx context.Context, uuid uuid.UUID) error {
	c.pendingCmds.cleanSession(uuid)
	return nil
}

func (c *controller) workDoneProgressCancel(ctx context.Context, params *protocol.WorkDoneProgressCancelParams) error {
	c.logger.Infof("Received cancel request for token: %s", params.Token)
	token := params.Token.String()
	key, err := c.pendingCmds.getContainingKey(token)
	if err != nil {
		c.logger.Infof("No containing key found for token: %s", token)
		return nil
	}

	c.stats.Counter("cancelled").Inc(1)
	cancelFunc, _, _ := c.pendingCmds.getPendingCmd(key)
	cancelFunc()

	c.pendingCmds.deletePendingCmd(key)
	return nil
}

func (c *controller) syncIndex(ctx context.Context, doc protocol.TextDocumentItem) error {
	c.stats.Counter("events").Inc(1)
	s, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}
	indexer := c.indexer[s.UUID]
	if indexer == nil {
		return nil
	}

	if !indexer.IsRelevantDocument(doc) {
		return nil
	}

	cmdKey, err := indexer.GetUniqueIndexKey(doc)
	if err != nil {
		c.logger.Infof("Error getting unique key for document %s: %v", filepath.Base(doc.URI.Filename()), err)
		return nil
	}

	// Check if an operation is already in progress
	if _, _, ok := c.pendingCmds.getPendingCmd(cmdKey); ok {
		// Instead of canceling immediately, mark the file as needing reindexing
		c.pendingCmds.markForReindexing(cmdKey)
		c.logger.Infof("Indexing already in progress for %s, marked for reindexing", filepath.Base(doc.URI.Filename()))
		c.stats.Counter("reindex_queued").Inc(1)
		return nil
	}
	return c.startIndexingOperation(ctx, cmdKey, doc, indexer)
}

// startIndexingOperation starts a new indexing operation.
func (c *controller) startIndexingOperation(ctx context.Context, cmdKey string, doc protocol.TextDocumentItem, indexer Indexer) error {
	ctx, cancelFunc := context.WithCancel(ctx)
	defer cancelFunc()

	defer func() {
		// Check if reindexing is needed after this operation completes
		if c.pendingCmds.needsReindexing(cmdKey) {
			c.logger.Infof("Changes detected during indexing, reindexing %s", filepath.Base(doc.URI.Filename()))
			c.pendingCmds.deletePendingCmd(cmdKey)

			newCtx := context.Background()
			if err := c.startIndexingOperation(newCtx, cmdKey, doc, indexer); err != nil {
				c.logger.Errorf("Error during reindexing: %v", err)
			}
		} else {
			c.pendingCmds.deletePendingCmd(cmdKey)
		}
	}()

	uniqueTokenStr := c.pendingCmds.setPendingCmd(cmdKey, cancelFunc)

	// start progress message
	token := protocol.NewProgressToken(uniqueTokenStr)
	c.sendIndexStartNotification(ctx, *token, filepath.Base(doc.URI.Filename()))
	defer c.sendIndexEndNotification(ctx, *token)

	c.stats.Counter("runs").Inc(1)
	err := indexer.SyncIndex(ctx, c.executor, c.ideGateway, c.logger, doc)
	if err != nil {
		c.stats.Counter("failed").Inc(1)
		c.logger.Errorf("Error indexing document %s: %v", filepath.Base(doc.URI.Filename()), err)
	} else {
		c.stats.Counter("success").Inc(1)
	}

	return err
}

func (c *controller) sendIndexStartNotification(ctx context.Context, token protocol.ProgressToken, filename string) error {
	err := c.ideGateway.WorkDoneProgressCreate(ctx, &protocol.WorkDoneProgressCreateParams{Token: token})
	if err != nil {
		return err
	}
	return c.ideGateway.Progress(ctx, &protocol.ProgressParams{
		Token: token,
		Value: protocol.WorkDoneProgressBegin{
			Kind:        protocol.WorkDoneProgressKindBegin,
			Title:       fmt.Sprintf(_syncMessage, filename),
			Cancellable: true,
		},
	})
}

func (c *controller) sendIndexEndNotification(ctx context.Context, token protocol.ProgressToken) error {
	return c.ideGateway.Progress(ctx, &protocol.ProgressParams{
		Token: token,
		Value: protocol.WorkDoneProgressEnd{
			Kind: protocol.WorkDoneProgressKindEnd,
		},
	})
}

// UpdateEnv updates the environment variables with the workspace root.
func UpdateEnv(env []string, wsRoot string) []string {
	workspaceUpdated := false
	projectUpdated := false

	for i, v := range env {
		if strings.HasPrefix(v, "WORKSPACE_ROOT=") {
			env[i] = "WORKSPACE_ROOT=" + wsRoot
			workspaceUpdated = true
		}
		if strings.HasPrefix(v, "PROJECT_ROOT=") {
			env[i] = "PROJECT_ROOT=" + wsRoot
			projectUpdated = true
		}
	}

	if !workspaceUpdated {
		env = append(env, "WORKSPACE_ROOT="+wsRoot)
	}
	if !projectUpdated {
		env = append(env, "PROJECT_ROOT="+wsRoot)
	}

	return env
}
