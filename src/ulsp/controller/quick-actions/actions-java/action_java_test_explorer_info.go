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
	_commandJavaTestExplorerInfo = "javatestexplorerinfo"
	_titleJavaTestExplorerInfo   = "Learn More"
)

const _messageTestExplorerDetails = `
	Now Available: Testing via VS Code's Test Explorer panel

	This will allow you to browse a tree of available test cases, see code coverage in the editor, and view run arrows/errors overlaid on code.

	To get started:
	- Open VS Code's Testing panel
	- Click the file icon on the "Bazel Test Targets" line to open the .bazelproject configuration
	- Add your preferred target paths and sync. Expand targets as needed to view individual test cases and run arrows on code.

   For a detailed guide, see: https://p.uber.com/bsp-test-explorer
`

// ActionJavaTestExplorerInfo is an action that displays basic getting started information to transition Java code lens users to the Test Explorer.
type ActionJavaTestExplorerInfo struct{}

var _ action.Action = &ActionJavaTestExplorerInfo{}

// Execute outputs a static information message to the client's output channel.
func (a *ActionJavaTestExplorerInfo) Execute(ctx context.Context, params *action.ExecuteParams, args json.RawMessage) error {
	params.IdeGateway.LogMessage(ctx, &protocol.LogMessageParams{
		Message: _messageTestExplorerDetails,
		Type:    protocol.MessageTypeWarning,
	})
	return nil
}

// ProcessDocument processes the given document, returning a list of CodeLens to be displayed.
func (a *ActionJavaTestExplorerInfo) ProcessDocument(ctx context.Context, document protocol.TextDocumentItem) ([]interface{}, error) {
	matches := mapper.FindAllStringMatches(_regexpJavaTest, document.Text)
	result := []interface{}{}

	for _, match := range matches {
		result = append(result, mapper.NewCodeLens(match.Range, _titleJavaTestExplorerInfo, a.CommandName(), struct{}{}))
	}

	return result, nil
}

// CommandName returns the name of the command to be executed.
func (a *ActionJavaTestExplorerInfo) CommandName() string {
	return fmt.Sprintf(action.CmdFormat, _commandJavaTestExplorerInfo)
}

// ShouldEnable enables this in the Java monorepo and only when a VS Code client is connected.
func (a *ActionJavaTestExplorerInfo) ShouldEnable(s *entity.Session, monorepo entity.MonorepoConfigEntry) bool {
	if s.InitializeParams == nil || s.InitializeParams.ClientInfo == nil {
		return false
	}
	if monorepo.EnableJavaSupport() && entity.ClientName(s.InitializeParams.ClientInfo.Name).IsVSCodeBased() {
		return true
	}

	return false
}

// IsRelevantDocument returns whether the action should be enabled for the given document.
func (a *ActionJavaTestExplorerInfo) IsRelevantDocument(s *entity.Session, document protocol.TextDocumentItem) bool {
	if document.LanguageID == "java" {
		return true
	}

	return false
}

// ProvideWorkDoneProgressParams returns info to display on progress of this action during execution
func (a *ActionJavaTestExplorerInfo) ProvideWorkDoneProgressParams(ctx context.Context, params *action.ExecuteParams, args json.RawMessage) (*action.ProgressInfoParams, error) {
	return nil, nil
}
