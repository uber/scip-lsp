package quickactions

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"

	action "github.com/uber/scip-lsp/src/ulsp/controller/quick-actions/action"
	"github.com/uber/scip-lsp/src/ulsp/entity"
	javautils "github.com/uber/scip-lsp/src/ulsp/internal/java-utils"
	"github.com/uber/scip-lsp/src/ulsp/mapper"
	"go.lsp.dev/protocol"
)

const (
	_commandJavaTestRunCoverage      = "javatestcoverage"
	_titleJavaTestRunCoverage        = "Run Java Test Coverage: %s"
	_startMessageJavaTestRunCoverage = "Running Java Test Coverage: %s"
	_bazelTest                       = "test"
	_testFilterArg                   = "--test_filter=%s"
	_collectCodecovFlag              = "--collect_code_coverage"
	_lcovReportFlag                  = "--combined_report=lcov"
	_ldaCodecovTagFormat             = "--tool_tag=%s:actions:codecov"
)

// ActionJavaTestRunCoverage is an action that runs Java test coverage for a given target.
type ActionJavaTestRunCoverage struct{}

var _ action.Action = &ActionJavaTestRunCoverage{}

// Execute runs the action, using the given arguments. ExecuteParams will provide values from the controller.
func (a *ActionJavaTestRunCoverage) Execute(ctx context.Context, params *action.ExecuteParams, args json.RawMessage) error {
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
	ideOutWriter, err := params.IdeGateway.GetLogMessageWriter(ctx, _commandJavaTestRunCoverage)
	if err != nil {
		return fmt.Errorf("getting writer: %w", err)
	}
	target, err := javautils.GetJavaTarget(params.FileSystem, s.WorkspaceRoot, resultArgs.Document.URI)
	if err != nil {
		return err
	}
	testCaseName := a.getTestCaseName(resultArgs)
	cmd, env := a.prepareCommandAndEnv(ctx, ideOutWriter, s.WorkspaceRoot, target, testCaseName, s.InitializeParams.ClientInfo.Name)

	err = params.IdeGateway.LogMessage(ctx, &protocol.LogMessageParams{
		Message: fmt.Sprintf(_startMessageJavaTestRunCoverage, strings.Join(cmd.Args, " ")),
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
		Message: fmt.Sprintf("Completed code coverage for %s", testCaseName),
		Type:    protocol.MessageTypeInfo,
	})
}

// ProcessDocument processes the given document, returning a list of CodeActions.
func (a *ActionJavaTestRunCoverage) ProcessDocument(ctx context.Context, document protocol.TextDocumentItem) ([]interface{}, error) {
	matches := mapper.FindAllStringMatches(_regexpJavaTest, document.Text)
	result := []interface{}{}

	for _, match := range matches {
		args := argsJavaTestRun{
			Document:   protocol.TextDocumentIdentifier{URI: document.URI},
			MethodName: match.CapturingGroups[0],
			ClassName:  match.CapturingGroups[1],
		}

		title := fmt.Sprintf(_titleJavaTestRunCoverage, args.MethodName)
		if args.MethodName == "" {
			title = fmt.Sprintf(_titleJavaTestRunCoverage, args.ClassName)
		}

		result = append(result, mapper.NewCodeActionWithRange(match.Range, title, a.CommandName(), protocol.Refactor, args))
	}

	return result, nil
}

// CommandName returns the name of the command that this action will execute.
func (a *ActionJavaTestRunCoverage) CommandName() string {
	return fmt.Sprintf(action.CmdFormat, _commandJavaTestRunCoverage)
}

// ShouldEnable returns true if the action should be enabled for the given session.
func (a *ActionJavaTestRunCoverage) ShouldEnable(s *entity.Session, monorepo entity.MonorepoConfigEntry) bool {
	return monorepo.EnableJavaSupport()
}

// IsRelevantDocument returns true if the action is relevant for the given document.
func (a *ActionJavaTestRunCoverage) IsRelevantDocument(s *entity.Session, document protocol.TextDocumentItem) bool {
	if document.LanguageID == "java" {
		return true
	}

	return false
}

// ProvideWorkDoneProgressParams returns info to display on progress of this action during execution
func (a *ActionJavaTestRunCoverage) ProvideWorkDoneProgressParams(ctx context.Context, params *action.ExecuteParams, args json.RawMessage) (*action.ProgressInfoParams, error) {
	return nil, nil
}

func (a *ActionJavaTestRunCoverage) prepareCommandAndEnv(ctx context.Context, writer io.Writer, workspaceRoot string, target string, testCaseName string, ideClient string) (*exec.Cmd, []string) {
	normalizedClient := javautils.NormalizeIDEClient(ideClient)
	ldaTag := fmt.Sprintf(_ldaCodecovTagFormat, normalizedClient)
	bazelCmd := path.Join(workspaceRoot, _bazelRelPath)
	testFilter := fmt.Sprintf(_testFilterArg, testCaseName)
	cmdArgs := []string{_bazelTest, testFilter, _collectCodecovFlag, _lcovReportFlag, target, ldaTag}
	cmd := exec.CommandContext(ctx, bazelCmd, cmdArgs...)
	cmd.Stderr = writer
	cmd.Stdout = writer
	cmd.Dir = workspaceRoot

	// Workspace and Project root is expected by bunch of java scripts
	env := os.Environ()
	env = append(env, _workspaceRoot+"="+workspaceRoot, _projectRoot+"="+workspaceRoot)
	return cmd, env
}

func (a *ActionJavaTestRunCoverage) getTestCaseName(args argsJavaTestRun) string {
	testCaseName := args.MethodName
	if args.ClassName != "" {
		testCaseName = args.ClassName
	}
	return testCaseName
}
