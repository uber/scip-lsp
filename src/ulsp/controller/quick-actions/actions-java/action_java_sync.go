package quickactions

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	action "github.com/uber/scip-lsp/src/ulsp/controller/quick-actions/action"
	"github.com/uber/scip-lsp/src/ulsp/entity"
	"github.com/uber/scip-lsp/src/ulsp/mapper"
	"go.lsp.dev/protocol"
)

const (
	_commandJavaSync            = "javasync"
	_titleJavaSync              = "Sync File"
	_startMessageJavaSync       = "Syncing Index for file: %s"
	_endMessageJavaSync         = "Command completed"
	_titleJavaSyncProgressBar   = "Syncing file %s"
	_messageJavaSyncProgressBar = "..."
	_syncScriptRelPath          = "tools/scip/scip_sync.sh"
	_syncScriptfilepathOpt      = "--filepath"
)

type argsJavaSync struct {
	Document protocol.TextDocumentIdentifier `json:"document,omitempty"`
}

// ActionJavaSync is an action that builds a java target.
type ActionJavaSync struct{}

var _ action.Action = &ActionJavaSync{}

// Execute runs the action, using the given arguments. ExecuteParams will provide values from the controller.
func (a *ActionJavaSync) Execute(ctx context.Context, params *action.ExecuteParams, args json.RawMessage) error {
	s, err := params.Sessions.GetFromContext(ctx)
	if err != nil {
		return fmt.Errorf("getting session from context: %w", err)
	}

	resultArgs := argsJavaSync{}
	err = json.Unmarshal(args, &resultArgs)
	if err != nil {
		return fmt.Errorf("unmarshalling args: %w", err)
	}

	// Command preparations set stdout and stderr to writer
	ideOutWriter, err := params.IdeGateway.GetLogMessageWriter(ctx, _commandJavaSync)
	if err != nil {
		return fmt.Errorf("getting writer: %w", err)
	}

	filepath, err := a.getRelativeFilePath(s.WorkspaceRoot, resultArgs.Document)
	if err != nil {
		return err
	}
	cmd, env := a.prepareCommandAndEnv(ctx, ideOutWriter, s.WorkspaceRoot, filepath)

	err = params.IdeGateway.LogMessage(ctx, &protocol.LogMessageParams{
		Message: fmt.Sprintf(_startMessageJavaSync, strings.Join(cmd.Args, " ")),
		Type:    protocol.MessageTypeInfo,
	})
	if err != nil {
		return fmt.Errorf("logging message: %w", err)
	}

	// Command execution will go here
	err = params.Executor.RunCommand(cmd, env)
	if err != nil {
		// Log message even if context is already done.
		params.IdeGateway.LogMessage(context.WithoutCancel(ctx), &protocol.LogMessageParams{
			Message: fmt.Errorf("Error during run: %w", err).Error(),
			Type:    protocol.MessageTypeError,
		})
		return err
	}
	_, err = ideOutWriter.Write([]byte(_endMessageJavaSync))
	if err != nil {
		return fmt.Errorf("logging message: %w", err)
	}

	return nil
}

// ProcessDocument processes the given document, returning a list of CodeLens to be displayed.
func (a *ActionJavaSync) ProcessDocument(ctx context.Context, document protocol.TextDocumentItem) ([]interface{}, error) {
	matches := mapper.FindAllStringMatches(_regexpJavaClassHeader, document.Text)
	result := []interface{}{}

	for _, match := range matches {
		args := argsJavaSync{
			Document: protocol.TextDocumentIdentifier{URI: document.URI},
		}

		result = append(result, mapper.NewCodeLens(match.Range, _titleJavaSync, a.CommandName(), args))
	}

	return result, nil
}

// CommandName returns the name of the command that will be executed.
func (a *ActionJavaSync) CommandName() string {
	return fmt.Sprintf(action.CmdFormat, _commandJavaSync)
}

// ShouldEnable returns true if the action should be enabled for the given session.
func (a *ActionJavaSync) ShouldEnable(s *entity.Session, monorepo entity.MonorepoConfigEntry) bool {
	// Removed monorepo check as it is not used in OS
	return monorepo.EnableJavaSupport()
}

// IsRelevantDocument returns true if the action is relevant for the given document.
func (a *ActionJavaSync) IsRelevantDocument(s *entity.Session, document protocol.TextDocumentItem) bool {
	if document.LanguageID == "java" {
		return true
	}

	return false
}

// ProvideWorkDoneProgressParams returns info to display on progress of this action during execution
func (a *ActionJavaSync) ProvideWorkDoneProgressParams(ctx context.Context, params *action.ExecuteParams, args json.RawMessage) (*action.ProgressInfoParams, error) {

	s, err := params.Sessions.GetFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting session from context: %w", err)
	}

	resultArgs := argsJavaSync{}
	err = json.Unmarshal(args, &resultArgs)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling args: %w", err)
	}

	filepath, err := a.getRelativeFilePath(s.WorkspaceRoot, resultArgs.Document)
	if err != nil {
		return nil, err
	}

	return &action.ProgressInfoParams{
		Title:   fmt.Sprintf(_titleJavaSyncProgressBar, path.Base(filepath)),
		Message: _messageJavaSyncProgressBar,
	}, nil
}

func (a *ActionJavaSync) getRelativeFilePath(workspaceRoot string, document protocol.TextDocumentIdentifier) (string, error) {
	curFilePath := document.URI.Filename()
	if !strings.HasPrefix(curFilePath, workspaceRoot) {
		return "", fmt.Errorf("invalid filepath to process")
	}
	relativePath := strings.TrimPrefix(curFilePath, workspaceRoot+string(filepath.Separator))
	return relativePath, nil
}

func (a *ActionJavaSync) prepareCommandAndEnv(ctx context.Context, writer io.Writer, workspaceRoot string, filepath string) (*exec.Cmd, []string) {
	syncCmd := path.Join(workspaceRoot, _syncScriptRelPath)
	cmdArgs := []string{_syncScriptfilepathOpt, filepath}
	cmd := exec.CommandContext(ctx, syncCmd, cmdArgs...)
	cmd.Stderr = writer
	cmd.Stdout = writer
	cmd.Dir = workspaceRoot

	// Workspace and Project root is expected by bunch of java scripts
	env := os.Environ()
	env = append(env, _workspaceRoot+"="+workspaceRoot, _projectRoot+"="+workspaceRoot)

	return cmd, env
}
