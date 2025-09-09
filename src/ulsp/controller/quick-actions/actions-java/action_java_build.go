package quickactions

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"

	action "github.com/uber/scip-lsp/src/ulsp/controller/quick-actions/action"
	"github.com/uber/scip-lsp/src/ulsp/entity"
	javautils "github.com/uber/scip-lsp/src/ulsp/internal/java-utils"
	"github.com/uber/scip-lsp/src/ulsp/mapper"
	"go.lsp.dev/protocol"
)

var _regexpJavaClassHeader = regexp.MustCompile(`class (\w+)`)

const (
	_commandJavaBuild            = "javabuild"
	_titleJavaBuild              = "Build"
	_startMessageJavaBuild       = "Building Java Target: %s"
	_titleJavaBuildProgressBar   = "Building File %s"
	_messageJavaBuildProgressBar = "Building target %s"
	_bazelBuild                  = "build"
	_ldaBuildTagFormat           = "--tool_tag=%s:actions:build"
	_endMessage                  = "Command completed"
	_bazelRelPath                = "tools/bazel"
	_workspaceRoot               = "WORKSPACE_ROOT"
	_projectRoot                 = "PROJECT_ROOT"
)

type argsJavaBuild struct {
	Document  protocol.TextDocumentIdentifier `json:"document,omitempty"`
	ClassName string                          `json:"className,omitempty"`
}

// ActionJavaBuild is an action that builds a java target.
type ActionJavaBuild struct{}

var _ action.Action = &ActionJavaBuild{}

// Execute runs the action, using the given arguments. ExecuteParams will provide values from the controller.
func (a *ActionJavaBuild) Execute(ctx context.Context, params *action.ExecuteParams, args json.RawMessage) error {
	s, err := params.Sessions.GetFromContext(ctx)
	if err != nil {
		return fmt.Errorf("getting session from context: %w", err)
	}

	resultArgs := argsJavaBuild{}
	err = json.Unmarshal(args, &resultArgs)
	if err != nil {
		return fmt.Errorf("unmarshalling args: %w", err)
	}

	// Command preparations set stdout and stderr to writer
	ideOutWriter, err := params.IdeGateway.GetLogMessageWriter(ctx, _commandJavaBuild)
	if err != nil {
		return fmt.Errorf("getting writer: %w", err)
	}
	target, err := javautils.GetJavaTarget(params.FileSystem, s.WorkspaceRoot, resultArgs.Document.URI)
	if err != nil {
		return err
	}
	cmd, env := a.prepareCommandAndEnv(ctx, ideOutWriter, s.WorkspaceRoot, target, s.InitializeParams.ClientInfo.Name)

	err = params.IdeGateway.LogMessage(ctx, &protocol.LogMessageParams{
		Message: fmt.Sprintf(_startMessageJavaBuild, strings.Join(cmd.Args, " ")),
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
	_, err = ideOutWriter.Write([]byte(_endMessage))
	if err != nil {
		return fmt.Errorf("logging message: %w", err)
	}

	return nil
}

// ProcessDocument processes the given document, returning a list of CodeLens to be displayed.
func (a *ActionJavaBuild) ProcessDocument(ctx context.Context, document protocol.TextDocumentItem) ([]interface{}, error) {
	matches := mapper.FindAllStringMatches(_regexpJavaClassHeader, document.Text)
	result := []interface{}{}

	for _, match := range matches {
		args := argsJavaBuild{
			Document:  protocol.TextDocumentIdentifier{URI: document.URI},
			ClassName: match.CapturingGroups[0],
		}

		result = append(result, mapper.NewCodeLens(match.Range, _titleJavaBuild, a.CommandName(), args))
	}

	return result, nil
}

// CommandName returns the name of the command that will be executed.
func (a *ActionJavaBuild) CommandName() string {
	return fmt.Sprintf(action.CmdFormat, _commandJavaBuild)
}

// ShouldEnable returns true if the action should be enabled for the given session.
func (a *ActionJavaBuild) ShouldEnable(s *entity.Session, config entity.MonorepoConfigEntry) bool {
	if config.EnableJavaSupport() {
		return true
	}

	return false
}

// IsRelevantDocument returns true if the action is relevant for the given document.
func (a *ActionJavaBuild) IsRelevantDocument(s *entity.Session, document protocol.TextDocumentItem) bool {
	if document.LanguageID == "java" {
		return true
	}

	return false
}

// ProvideWorkDoneProgressParams returns info to display on progress of this action during execution
func (a *ActionJavaBuild) ProvideWorkDoneProgressParams(ctx context.Context, params *action.ExecuteParams, args json.RawMessage) (*action.ProgressInfoParams, error) {

	s, err := params.Sessions.GetFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting session from context: %w", err)
	}

	resultArgs := argsJavaBuild{}
	err = json.Unmarshal(args, &resultArgs)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling args: %w", err)
	}

	file := path.Base(resultArgs.Document.URI.Filename())
	target, err := javautils.GetJavaTarget(params.FileSystem, s.WorkspaceRoot, resultArgs.Document.URI)
	if err != nil {
		return nil, err
	}

	return &action.ProgressInfoParams{
		Title:   fmt.Sprintf(_titleJavaBuildProgressBar, file),
		Message: fmt.Sprintf(_messageJavaBuildProgressBar, target),
	}, nil
}

func (a *ActionJavaBuild) prepareCommandAndEnv(ctx context.Context, writer io.Writer, workspaceRoot string, target string, ideClient string) (*exec.Cmd, []string) {
	normalizedClient := javautils.NormalizeIDEClient(ideClient)

	ldaTag := fmt.Sprintf(_ldaBuildTagFormat, normalizedClient)
	bazelCmd := path.Join(workspaceRoot, _bazelRelPath)
	cmdArgs := []string{_bazelBuild, target, ldaTag}
	cmd := exec.CommandContext(ctx, bazelCmd, cmdArgs...)
	cmd.Stderr = writer
	cmd.Stdout = writer
	cmd.Dir = workspaceRoot

	// Workspace and Project root is expected by bunch of java scripts
	env := os.Environ()
	env = append(env, _workspaceRoot+"="+workspaceRoot, _projectRoot+"="+workspaceRoot)

	return cmd, env
}
