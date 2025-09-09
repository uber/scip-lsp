package quickactions

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/uber/scip-lsp/src/ulsp/entity"
	"github.com/uber/scip-lsp/src/ulsp/internal/fs"
	"go.uber.org/config"

	"github.com/gofrs/uuid"
	docsync "github.com/uber/scip-lsp/src/ulsp/controller/doc-sync"
	action "github.com/uber/scip-lsp/src/ulsp/controller/quick-actions/action"
	actions_java "github.com/uber/scip-lsp/src/ulsp/controller/quick-actions/actions-java"
	ulspplugin "github.com/uber/scip-lsp/src/ulsp/entity/ulsp-plugin"
	ideclient "github.com/uber/scip-lsp/src/ulsp/gateway/ide-client"
	ulsperrors "github.com/uber/scip-lsp/src/ulsp/internal/errors"
	"github.com/uber/scip-lsp/src/ulsp/internal/executor"
	"github.com/uber/scip-lsp/src/ulsp/mapper"
	"github.com/uber/scip-lsp/src/ulsp/repository/session"
	"go.lsp.dev/protocol"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

var allActions = []action.Action{
	// Java
	&actions_java.ActionJavaTestRun{},
	&actions_java.ActionJavaTestRunCoverage{},
	&actions_java.ActionJavaBuild{},
	&actions_java.ActionJavaTestExplorer{},
	&actions_java.ActionJavaTestExplorerInfo{},
	&actions_java.ActionJavaSync{},
}

const (
	_nameKey        = "quick-actions"
	_codeActionKind = protocol.Refactor
)

// Params defines the dependencies that will be available ot this controller.
type Params struct {
	fx.In

	Config     config.Provider
	Executor   executor.Executor
	Documents  docsync.Controller
	IdeGateway ideclient.Gateway
	Sessions   session.Repository
	Logger     *zap.SugaredLogger
	FS         fs.UlspFS
}

// Controller defines the methods that this controller provides.
type Controller interface {
	StartupInfo(ctx context.Context) (ulspplugin.PluginInfo, error)
}

type controller struct {
	enabledActions      map[uuid.UUID][]action.Action
	pendingActionRuns   *pendingActionRunStore
	pendingCmds         map[protocol.ProgressToken]context.CancelFunc
	cmdMu               sync.Mutex
	currentActionRanges *actionRangeStore
	actionsMu           sync.Mutex
	documents           docsync.Controller
	executor            executor.Executor
	ideGateway          ideclient.Gateway
	sessions            session.Repository
	logger              *zap.SugaredLogger
	fs                  fs.UlspFS
	config              entity.MonorepoConfigs
}

// New creates a new controller for quick hints.
func New(p Params) Controller {
	configs := entity.MonorepoConfigs{}
	if err := p.Config.Get(entity.MonorepoConfigKey).Populate(&configs); err != nil {
		panic(fmt.Sprintf("getting configuration for %q: %v", entity.MonorepoConfigKey, err))
	}

	c := &controller{
		documents:  p.Documents,
		ideGateway: p.IdeGateway,
		sessions:   p.Sessions,
		executor:   p.Executor,
		logger:     p.Logger.With("plugin", _nameKey),
		fs:         p.FS,

		currentActionRanges: newActionRangeStore(),
		enabledActions:      make(map[uuid.UUID][]action.Action),
		pendingActionRuns:   newInProgressActionStore(),
		pendingCmds:         make(map[protocol.ProgressToken]context.CancelFunc),
		config:              configs,
	}
	return c
}

// StartupInfo returns PluginInfo for this controller.
func (c *controller) StartupInfo(ctx context.Context) (ulspplugin.PluginInfo, error) {
	// Set a priority for each method that this module provides.
	priorities := map[string]ulspplugin.Priority{
		protocol.MethodInitialize:           ulspplugin.PriorityRegular,
		protocol.MethodTextDocumentDidOpen:  ulspplugin.PriorityAsync,
		protocol.MethodTextDocumentDidSave:  ulspplugin.PriorityAsync,
		protocol.MethodTextDocumentDidClose: ulspplugin.PriorityAsync,
		ulspplugin.MethodEndSession:         ulspplugin.PriorityRegular,

		protocol.MethodTextDocumentCodeAction:  ulspplugin.PriorityRegular,
		protocol.MethodTextDocumentCodeLens:    ulspplugin.PriorityRegular,
		protocol.MethodWorkspaceExecuteCommand: ulspplugin.PriorityAsync,
		protocol.MethodWorkDoneProgressCancel:  ulspplugin.PriorityRegular,
	}

	// Assign method keys to implementations.
	methods := &ulspplugin.Methods{
		PluginNameKey: _nameKey,

		Initialize: c.initialize,
		DidSave:    c.didSave,
		DidOpen:    c.didOpen,
		DidClose:   c.didClose,
		EndSession: c.endSession,

		CodeAction:             c.codeAction,
		CodeLens:               c.codeLens,
		ExecuteCommand:         c.executeCommand,
		WorkDoneProgressCancel: c.workDoneProgressCancel,
	}

	return ulspplugin.PluginInfo{
		Priorities: priorities,
		Methods:    methods,
		NameKey:    _nameKey,
	}, nil
}

func (c *controller) initialize(ctx context.Context, params *protocol.InitializeParams, result *protocol.InitializeResult) error {
	s, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return fmt.Errorf("getting session: %w", err)
	}

	supportedCodeActionKinds := []protocol.CodeActionKind{}
	for kind := range action.SupportedCodeActionKinds {
		supportedCodeActionKinds = append(supportedCodeActionKinds, kind)
	}

	if err := mapper.InitializeResultAppendCodeActionProvider(result, &protocol.CodeActionOptions{CodeActionKinds: supportedCodeActionKinds}); err != nil {
		return fmt.Errorf("failed to append CodeActionProvider: %w", err)
	}

	if err := mapper.InitializeResultEnsureCodeLensProvider(result, false); err != nil {
		return fmt.Errorf("failed to append CodeLensProvider: %w", err)
	}

	commands := []string{}
	for _, action := range allActions {
		if action.ShouldEnable(s, c.config[s.Monorepo]) {
			c.enabledActions[s.UUID] = append(c.enabledActions[s.UUID], action)
			if action.CommandName() != "" {
				commands = append(commands, action.CommandName())
			}
		}
	}

	if err := mapper.InitializeResultAppendExecuteCommandProvider(result, &protocol.ExecuteCommandOptions{Commands: commands}); err != nil {
		return fmt.Errorf("failed to append ExecuteCommandProvider: %w", err)
	}

	return nil
}

func (c *controller) didOpen(ctx context.Context, params *protocol.DidOpenTextDocumentParams) error {
	return c.refreshAvailableCodeActions(ctx, protocol.TextDocumentIdentifier{URI: params.TextDocument.URI})
}

func (c *controller) didSave(ctx context.Context, params *protocol.DidSaveTextDocumentParams) error {
	return c.refreshAvailableCodeActions(ctx, params.TextDocument)
}

func (c *controller) didClose(ctx context.Context, params *protocol.DidCloseTextDocumentParams) error {
	s, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return fmt.Errorf("getting session for code action: %w", err)
	}
	c.actionsMu.Lock()
	defer c.actionsMu.Unlock()
	c.currentActionRanges.ClearDocument(s.UUID, params.TextDocument)
	return nil
}

func (c *controller) endSession(ctx context.Context, uuid uuid.UUID) error {
	c.actionsMu.Lock()
	defer c.actionsMu.Unlock()

	c.currentActionRanges.DeleteSession(uuid)
	pendingTokens := c.pendingActionRuns.GetInProgressActionRunTokens(uuid)
	for _, token := range pendingTokens {
		c.cancelPendingCmd(*token)
	}
	c.pendingActionRuns.DeleteSession(uuid)
	return nil
}

func (c *controller) codeAction(ctx context.Context, params *protocol.CodeActionParams, result *[]protocol.CodeAction) error {
	s, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return fmt.Errorf("getting session for code action: %w", err)
	}

	err = c.refreshAvailableCodeActions(ctx, params.TextDocument)
	if err != nil {
		return fmt.Errorf("refreshing available code actions: %w", err)
	}

	matches, err := c.currentActionRanges.GetMatchingCodeActions(s.UUID, params.TextDocument, params.Range)
	if err != nil {
		return fmt.Errorf("getting code actions for this range: %w", err)
	}
	*result = append(*result, matches...)
	return nil
}

func (c *controller) codeLens(ctx context.Context, params *protocol.CodeLensParams, result *[]protocol.CodeLens) error {
	s, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return fmt.Errorf("getting session for code lens: %w", err)
	}

	err = c.refreshAvailableCodeActions(ctx, params.TextDocument)
	if err != nil {
		return fmt.Errorf("refreshing code lenses: %w", err)
	}

	codeLensMatches, err := c.currentActionRanges.GetCodeLenses(s.UUID, params.TextDocument)
	if err != nil {
		return fmt.Errorf("getting code lenses for this range: %w", err)
	}
	*result = append(*result, codeLensMatches...)
	return nil
}

func (c *controller) executeCommand(ctx context.Context, params *protocol.ExecuteCommandParams) error {
	s, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return fmt.Errorf("getting session for code action: %w", err)
	}

	for _, currentAction := range c.enabledActions[s.UUID] {
		if currentAction.CommandName() != params.Command {
			continue
		}

		if len(params.Arguments) != 1 {
			return fmt.Errorf("invalid args format, expected all args data as a raw json message in the first entry")
		}
		args, ok := params.Arguments[0].([]uint8)
		if !ok {
			return fmt.Errorf("invalid args type, should be provided as raw json")
		}

		commandName := currentAction.CommandName()
		// Do nothing if token exists
		if c.pendingActionRuns.TokenExists(s.UUID, commandName, string(args)) {
			return nil
		}

		progressToken := c.pendingActionRuns.AddInProgressAction(s.UUID, commandName, string(args))
		defer c.pendingActionRuns.DeleteInProgressAction(s.UUID, commandName, string(args))

		params := &action.ExecuteParams{
			Sessions:      c.sessions,
			Executor:      c.executor,
			IdeGateway:    c.ideGateway,
			ProgressToken: progressToken,
			FileSystem:    c.fs,
		}

		progressInfoParams, err := currentAction.ProvideWorkDoneProgressParams(ctx, params, args)
		if err != nil {
			return err
		}
		c.startWorkDoneProgressMessage(ctx, params, progressInfoParams)
		defer c.endWorkDoneProgressMessage(ctx, params, progressInfoParams)

		ctx, cancelFunc := context.WithCancel(ctx)
		defer cancelFunc() // call to avoid context leak

		c.pendingCmds[*progressToken] = cancelFunc

		return currentAction.Execute(ctx, params, args)
	}

	return nil
}

func (c *controller) refreshAvailableCodeActions(ctx context.Context, documentIdentifier protocol.TextDocumentIdentifier) error {
	s, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return fmt.Errorf("getting session for code action: %w", err)
	}

	c.actionsMu.Lock()
	defer c.actionsMu.Unlock()

	doc, err := c.documents.GetTextDocument(ctx, documentIdentifier)
	if err != nil {
		var notFoundErr *ulsperrors.DocumentNotFoundError
		if errors.As(err, &notFoundErr) {
			return nil
		}
		return err
	}

	// If the last processed version is current, no need to refresh.
	if !c.currentActionRanges.SetVersion(s.UUID, documentIdentifier, doc.Version) {
		return nil
	}

	// If the document version is new, delete the old actions and regenerate them.
	c.currentActionRanges.DeleteExistingDocumentRanges(s.UUID, documentIdentifier)
	for _, currentAction := range c.enabledActions[s.UUID] {
		isRelevantDoc := currentAction.IsRelevantDocument(s, doc)
		if !isRelevantDoc {
			continue
		}

		actionMatches, err := currentAction.ProcessDocument(ctx, doc)
		if err != nil {
			return fmt.Errorf("processing document for code action: %w", err)
		}

		for _, result := range actionMatches {
			if currentCodeAction, ok := result.(mapper.CodeActionWithRange); ok {
				if _, ok := action.SupportedCodeActionKinds[currentCodeAction.CodeAction.Kind]; !ok {
					return fmt.Errorf("action returned unsupported kind: %s", currentCodeAction.CodeAction.Kind)
				}
				c.currentActionRanges.AddCodeAction(s.UUID, documentIdentifier, currentCodeAction.Range, currentCodeAction.CodeAction)
			} else if currentCodeLens, ok := result.(protocol.CodeLens); ok {
				c.currentActionRanges.AddCodeLens(s.UUID, documentIdentifier, currentCodeLens)
			} else {
				return fmt.Errorf("action returned invalid type: %T", result)
			}
		}
	}

	return nil
}

func (c *controller) workDoneProgressCancel(ctx context.Context, params *protocol.WorkDoneProgressCancelParams) error {
	c.logger.Infof("Received cancel request for token: %s", params.Token)
	return c.cancelPendingCmd(params.Token)
}

// Start a progress message for the run. Token will tracked and used throughout the rest of the run.
func (c *controller) startWorkDoneProgressMessage(ctx context.Context, params *action.ExecuteParams, progressInfoParams *action.ProgressInfoParams) error {
	if progressInfoParams == nil {
		return nil
	}

	params.IdeGateway.WorkDoneProgressCreate(ctx, &protocol.WorkDoneProgressCreateParams{Token: *params.ProgressToken})
	params.IdeGateway.Progress(ctx, &protocol.ProgressParams{
		Token: *params.ProgressToken,
		Value: protocol.WorkDoneProgressBegin{
			Kind:        protocol.WorkDoneProgressKindBegin,
			Title:       progressInfoParams.Title,
			Message:     progressInfoParams.Message,
			Cancellable: true,
		},
	})
	return nil
}

// End a progress message for the run.
func (c *controller) endWorkDoneProgressMessage(ctx context.Context, params *action.ExecuteParams, progressInfoParams *action.ProgressInfoParams) error {
	if progressInfoParams == nil {
		return nil
	}

	params.IdeGateway.Progress(ctx, &protocol.ProgressParams{
		Token: *params.ProgressToken,
		Value: protocol.WorkDoneProgressEnd{
			Kind: protocol.WorkDoneProgressKindEnd,
		},
	})
	return nil
}

func (c *controller) cancelPendingCmd(token protocol.ProgressToken) error {
	c.cmdMu.Lock()
	defer c.cmdMu.Unlock()

	cancelCmd, ok := c.pendingCmds[token]
	if !ok {
		c.logger.Info("no pending command found for token: %s", token)
		return nil
	}
	delete(c.pendingCmds, token)
	cancelCmd()
	return nil
}
