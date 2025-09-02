package quickactions

import (
	"context"
	"encoding/json"

	"github.com/uber/scip-lsp/src/ulsp/internal/fs"

	"github.com/uber/scip-lsp/src/ulsp/entity"
	ideclient "github.com/uber/scip-lsp/src/ulsp/gateway/ide-client"
	"github.com/uber/scip-lsp/src/ulsp/internal/executor"
	"github.com/uber/scip-lsp/src/ulsp/repository/session"
	"go.lsp.dev/protocol"
)

// CmdFormat is the format string for the command name of the action.
const CmdFormat = "ulsp.quick-actions.%s"

// SupportedCodeActionKinds includes enabled code action kinds within this plugin. To support a new action, it must be added here.
var SupportedCodeActionKinds = map[protocol.CodeActionKind]struct{}{
	protocol.Refactor: {},
}

// ExecuteParams provides values from the controller to the action.
type ExecuteParams struct {
	Sessions      session.Repository
	IdeGateway    ideclient.Gateway
	Executor      executor.Executor
	ProgressToken *protocol.ProgressToken
	FileSystem    fs.UlspFS
}

// ProgressInfoParams provides parameters for display during progress of action in status bar
type ProgressInfoParams struct {
	Title   string // Title to display in the work done progress bar
	Message string // Message to display in work done progress bar
}

// Action defines an action that can be executed by the quick actions plugin.
// create new actions by implementing this interface and adding an instance of the new action to allActions.
// actions should not store internal state across calls, as they are created once and reused.
type Action interface {
	// These methods should be implemented using simple conditionals, should not be expensive or depend on outside calls, and cannot return an error.
	// ShouldEnable determines if the action should be enabled for the given session.
	ShouldEnable(s *entity.Session, monorepo entity.MonorepoConfigEntry) bool
	// CommandName returns the name of the command which will be executed when clicked.
	CommandName() string
	// IsRelevantDocument determines if the action is relevant to the given document.
	IsRelevantDocument(s *entity.Session, document protocol.TextDocumentItem) bool

	// These methods may contain calls to the IDE or other other parts of uLSP via the controller.
	// ProcessDocument processes the document and returns a slice of CodeLens or CodeActionWithRange values for the full document.
	ProcessDocument(ctx context.Context, document protocol.TextDocumentItem) ([]interface{}, error)
	// Execute runs the action, using the given arguments. ExecuteParams will provide values from the controller.
	Execute(ctx context.Context, params *ExecuteParams, args json.RawMessage) error

	// ProvideWorkDoneProgressParams returns info to display on progress of this action during execution
	ProvideWorkDoneProgressParams(ctx context.Context, params *ExecuteParams, args json.RawMessage) (*ProgressInfoParams, error)
}
