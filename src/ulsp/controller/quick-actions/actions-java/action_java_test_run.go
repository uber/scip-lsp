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

var _regexpJavaTest = regexp.MustCompile(`@Test\s+.*\s+public void (\w+)|public class (\w+Test)\s+extends`)

const (
	_commandJavaTestRun      = "javatestrun"
	_titleJavaTestRun        = "Run Test"
	_startMessageJavaTestRun = "Running Java Test: %s"
	_ldaTestTagFormat        = "--tool_tag=%s:actions:test"
)

type argsJavaTestRun struct {
	Document   protocol.TextDocumentIdentifier `json:"document,omitempty"`
	ClassName  string                          `json:"className,omitempty"`
	MethodName string                          `json:"methodName,omitempty"`
}

// ActionJavaTestRun is an action that runs a Java test.
type ActionJavaTestRun struct{}

var _ action.Action = &ActionJavaTestRun{}

// Execute runs the action, using the given arguments. ExecuteParams will provide values from the controller.
func (a *ActionJavaTestRun) Execute(ctx context.Context, params *action.ExecuteParams, args json.RawMessage) error {
	s, err := params.Sessions.GetFromContext(ctx)
	if err != nil {
		return fmt.Errorf("getting session from context: %w", err)
	}

	resultArgs := argsJavaTestRun{}
	err = json.Unmarshal(args, &resultArgs)
	if err != nil {
		return fmt.Errorf("unmarshalling args: %w", err)
	}

	// Command preparations set stdout and stderr to writer
	ideOutWriter, err := params.IdeGateway.GetLogMessageWriter(ctx, _commandJavaTestRun)
	if err != nil {
		return fmt.Errorf("getting writer: %w", err)
	}

	testCaseName := a.getTestCaseName(resultArgs)
	target, err := javautils.GetJavaTarget(params.FileSystem, s.WorkspaceRoot, resultArgs.Document.URI)
	if err != nil {
		return err
	}
	cmd, env := a.prepareCommandAndEnv(ctx, ideOutWriter, s.WorkspaceRoot, target, testCaseName, s.InitializeParams.ClientInfo.Name)

	err = params.IdeGateway.LogMessage(ctx, &protocol.LogMessageParams{
		Message: fmt.Sprintf(_startMessageJavaTestRun, strings.Join(cmd.Args, " ")),
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

	return params.IdeGateway.ShowMessage(ctx, &protocol.ShowMessageParams{
		Message: fmt.Sprintf("Completed test %s", testCaseName),
		Type:    protocol.MessageTypeInfo,
	})
}

// ProcessDocument processes the given document, returning a list of CodeLens to be displayed.
func (a *ActionJavaTestRun) ProcessDocument(ctx context.Context, document protocol.TextDocumentItem) ([]interface{}, error) {
	matches := mapper.FindAllStringMatches(_regexpJavaTest, document.Text)
	result := []interface{}{}

	for _, match := range matches {
		args := argsJavaTestRun{
			Document:   protocol.TextDocumentIdentifier{URI: document.URI},
			MethodName: match.CapturingGroups[0],
			ClassName:  match.CapturingGroups[1],
		}

		result = append(result, mapper.NewCodeLens(match.Range, _titleJavaTestRun, a.CommandName(), args))
	}

	return result, nil
}

// CommandName returns the name of the command to be executed.
func (a *ActionJavaTestRun) CommandName() string {
	return fmt.Sprintf(action.CmdFormat, _commandJavaTestRun)
}

// ShouldEnable returns whether the action should be enabled for the given session.
func (a *ActionJavaTestRun) ShouldEnable(s *entity.Session, monorepo entity.MonorepoConfigEntry) bool {
	if s.InitializeParams == nil || s.InitializeParams.ClientInfo == nil {
		return false
	}
	// Users of the VS Code client will instead of use the Test Explorer.
	if monorepo.EnableJavaSupport() && !entity.ClientName(s.InitializeParams.ClientInfo.Name).IsVSCodeBased() {
		return true
	}

	return false
}

// IsRelevantDocument returns whether the action should be enabled for the given document.
func (a *ActionJavaTestRun) IsRelevantDocument(s *entity.Session, document protocol.TextDocumentItem) bool {
	if document.LanguageID == "java" {
		return true
	}

	return false
}

// ProvideWorkDoneProgressParams returns info to display on progress of this action during execution
func (a *ActionJavaTestRun) ProvideWorkDoneProgressParams(ctx context.Context, params *action.ExecuteParams, args json.RawMessage) (*action.ProgressInfoParams, error) {
	return nil, nil
}

func (a *ActionJavaTestRun) prepareCommandAndEnv(ctx context.Context, writer io.Writer, workspaceRoot string, target string, testCaseName string, ideClient string) (*exec.Cmd, []string) {
	normalizedClient := javautils.NormalizeIDEClient(ideClient)
	ldaTag := fmt.Sprintf(_ldaTestTagFormat, normalizedClient)
	bazelCmd := path.Join(workspaceRoot, _bazelRelPath)
	testFilter := fmt.Sprintf(_testFilterArg, testCaseName)
	cmdArgs := []string{_bazelTest, testFilter, target, ldaTag}
	cmd := exec.CommandContext(ctx, bazelCmd, cmdArgs...)
	cmd.Stderr = writer
	cmd.Stdout = writer
	cmd.Dir = workspaceRoot

	// Workspace and Project root is expected by bunch of java scripts
	env := os.Environ()
	env = append(env, _workspaceRoot+"="+workspaceRoot, _projectRoot+"="+workspaceRoot)
	return cmd, env
}

func (a *ActionJavaTestRun) getTestCaseName(args argsJavaTestRun) string {
	testCaseName := args.MethodName
	if args.ClassName != "" {
		testCaseName = args.ClassName
	}
	return testCaseName
}
