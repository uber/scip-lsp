package quickactions

import (
	"context"
	"encoding/json"
	"fmt"

	action "github.com/uber/scip-lsp/src/ulsp/controller/quick-actions/action"
	"github.com/uber/scip-lsp/src/ulsp/entity"
	"github.com/uber/scip-lsp/src/ulsp/mapper"
	"go.lsp.dev/protocol"
)

const (
	_commandJavaTestExplorer = "workbench.view.extension.test"
	_titleJavaTestExplorer   = "Show Test Explorer"
)

// ActionJavaTestExplorer is an action that calls VS Code's command to open the Testing panel.
type ActionJavaTestExplorer struct{}

var _ action.Action = &ActionJavaTestExplorer{}

// Execute is not implemented for this action, as it will be handled by the IDE.
func (a *ActionJavaTestExplorer) Execute(ctx context.Context, params *action.ExecuteParams, args json.RawMessage) error {
	// This action is not executable by the IDE.
	return fmt.Errorf("action %q should be handled by the IDE and is not implemented in uLSP", _commandJavaTestExplorer)
}

// ProcessDocument processes the given document, returning a list of CodeLens to be displayed.
func (a *ActionJavaTestExplorer) ProcessDocument(ctx context.Context, document protocol.TextDocumentItem) ([]interface{}, error) {
	matches := mapper.FindAllStringMatches(_regexpJavaTest, document.Text)
	result := []interface{}{}

	for _, match := range matches {
		result = append(result, mapper.NewCodeLens(match.Range, _titleJavaTestExplorer, _commandJavaTestExplorer, struct{}{}))
	}

	return result, nil
}

// CommandName returns an empty command, as uLSP will not need to register a command for this code action.
func (a *ActionJavaTestExplorer) CommandName() string {
	return ""
}

// ShouldEnable enables this in the Java monorepo and only when a VS Code client is connected.
func (a *ActionJavaTestExplorer) ShouldEnable(s *entity.Session, monorepo entity.MonorepoConfigEntry) bool {
	if s.InitializeParams == nil || s.InitializeParams.ClientInfo == nil {
		return false
	}
	if monorepo.EnableJavaSupport() && entity.ClientName(s.InitializeParams.ClientInfo.Name).IsVSCodeBased() {
		return true
	}

	return false
}

// IsRelevantDocument returns whether the action should be enabled for the given document.
func (a *ActionJavaTestExplorer) IsRelevantDocument(s *entity.Session, document protocol.TextDocumentItem) bool {
	if document.LanguageID == "java" {
		return true
	}

	return false
}

// ProvideWorkDoneProgressParams returns info to display on progress of this action during execution
func (a *ActionJavaTestExplorer) ProvideWorkDoneProgressParams(ctx context.Context, params *action.ExecuteParams, args json.RawMessage) (*action.ProgressInfoParams, error) {
	return nil, nil
}
