package quickactions

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/uber/scip-lsp/src/ulsp/internal/fs/fsmock"
	"github.com/uber/scip-lsp/src/ulsp/internal/fs/fsmock/helpers"

	"github.com/stretchr/testify/assert"
	action "github.com/uber/scip-lsp/src/ulsp/controller/quick-actions/action"
	"github.com/uber/scip-lsp/src/ulsp/entity"
	"github.com/uber/scip-lsp/src/ulsp/factory"
	"github.com/uber/scip-lsp/src/ulsp/gateway/ide-client/ideclientmock"
	"github.com/uber/scip-lsp/src/ulsp/internal/executor/executormock"
	"github.com/uber/scip-lsp/src/ulsp/repository/session/repositorymock"
	"go.lsp.dev/protocol"
	"go.uber.org/mock/gomock"
)

func TestJavaTestRunExecute(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()
	a := ActionJavaTestRun{}

	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
		InitializeParams: &protocol.InitializeParams{
			ClientInfo: &protocol.ClientInfo{
				Name: "Visual Studio Code",
			},
		},
	}
	s.WorkspaceRoot = "/home/user/fievel"
	s.Monorepo = "lm/fievel"

	t.Run("success", func(t *testing.T) {
		executorMock := executormock.NewMockExecutor(ctrl)
		ideGatewayMock := ideclientmock.NewMockGateway(ctrl)
		fs := fsmock.NewMockUlspFS(ctrl)

		fs.EXPECT().DirExists(gomock.Any()).Return(true, nil)
		fs.EXPECT().ReadDir("/home/user/fievel/roadrunner/application-dw").Return([]os.DirEntry{helpers.MockDirEntry("BUILD.bazel", false)}, nil)
		fs.EXPECT().ReadDir(gomock.Any()).Times(9).Return([]os.DirEntry{}, nil)

		c := &action.ExecuteParams{
			IdeGateway: ideGatewayMock,
			Sessions:   sessionRepository,
			Executor:   executorMock,
			FileSystem: fs,
		}

		var writer bytes.Buffer
		ideGatewayMock.EXPECT().ShowMessage(gomock.Any(), gomock.Any()).Return(nil)
		ideGatewayMock.EXPECT().GetLogMessageWriter(gomock.Any(), gomock.Any()).Return(&writer, nil)
		ideGatewayMock.EXPECT().LogMessage(gomock.Any(), gomock.Any()).Return(nil)
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		executorMock.EXPECT().RunCommand(gomock.Any(), gomock.Any()).Return(nil)

		assert.NoError(t, a.Execute(ctx, c, []byte(`{"interfaceName": "myInterface", "document": {"uri": "file:///home/user/fievel/roadrunner/application-dw/src/test/java/com/uber/roadrunner/application/exception/GatewayErrorExceptionMapperTest.java"}}`)))
	})

	t.Run("bad args", func(t *testing.T) {
		executorMock := executormock.NewMockExecutor(ctrl)
		ideGatewayMock := ideclientmock.NewMockGateway(ctrl)
		fs := fsmock.NewMockUlspFS(ctrl)

		fs.EXPECT().DirExists(gomock.Any()).Times(0).Return(true, nil)
		fs.EXPECT().ReadDir(gomock.Any()).Times(0).Return([]os.DirEntry{}, nil)

		c := &action.ExecuteParams{
			IdeGateway: ideGatewayMock,
			Sessions:   sessionRepository,
			Executor:   executorMock,
			FileSystem: fs,
		}
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		assert.Error(t, a.Execute(ctx, c, []byte(`{"brokenJSON`)))
	})

	t.Run("log message failure", func(t *testing.T) {
		executorMock := executormock.NewMockExecutor(ctrl)
		ideGatewayMock := ideclientmock.NewMockGateway(ctrl)
		fs := fsmock.NewMockUlspFS(ctrl)

		fs.EXPECT().DirExists(gomock.Any()).Return(true, nil)
		fs.EXPECT().ReadDir("/home/user/fievel/roadrunner/application-dw").Return([]os.DirEntry{helpers.MockDirEntry("BUILD.bazel", false)}, nil)
		fs.EXPECT().ReadDir(gomock.Any()).Times(9).Return([]os.DirEntry{}, nil)

		c := &action.ExecuteParams{
			IdeGateway: ideGatewayMock,
			Sessions:   sessionRepository,
			Executor:   executorMock,
			FileSystem: fs,
		}

		var writer bytes.Buffer
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		ideGatewayMock.EXPECT().GetLogMessageWriter(gomock.Any(), gomock.Any()).Return(&writer, nil)
		ideGatewayMock.EXPECT().LogMessage(gomock.Any(), gomock.Any()).Return(errors.New("error"))

		assert.Error(t, a.Execute(ctx, c, []byte(`{"interfaceName": "myInterface", "document": {"uri": "file:///home/user/fievel/roadrunner/application-dw/src/test/java/com/uber/roadrunner/application/exception/GatewayErrorExceptionMapperTest.java"}}`)))
	})

	t.Run("show message failure", func(t *testing.T) {
		executorMock := executormock.NewMockExecutor(ctrl)
		ideGatewayMock := ideclientmock.NewMockGateway(ctrl)
		fs := fsmock.NewMockUlspFS(ctrl)

		fs.EXPECT().DirExists(gomock.Any()).Return(true, nil)
		fs.EXPECT().ReadDir("/home/user/fievel/roadrunner/application-dw").Return([]os.DirEntry{helpers.MockDirEntry("BUILD.bazel", false)}, nil)
		fs.EXPECT().ReadDir(gomock.Any()).Times(9).Return([]os.DirEntry{}, nil)

		c := &action.ExecuteParams{
			IdeGateway: ideGatewayMock,
			Sessions:   sessionRepository,
			Executor:   executorMock,
			FileSystem: fs,
		}

		var writer bytes.Buffer
		executorMock.EXPECT().RunCommand(gomock.Any(), gomock.Any()).Return(nil)
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		ideGatewayMock.EXPECT().GetLogMessageWriter(gomock.Any(), gomock.Any()).Return(&writer, nil)
		ideGatewayMock.EXPECT().LogMessage(gomock.Any(), gomock.Any()).Return(nil)
		ideGatewayMock.EXPECT().ShowMessage(gomock.Any(), gomock.Any()).Return(errors.New("error"))

		assert.Error(t, a.Execute(ctx, c, []byte(`{"interfaceName": "myInterface", "document": {"uri": "file:///home/user/fievel/roadrunner/application-dw/src/test/java/com/uber/roadrunner/application/exception/GatewayErrorExceptionMapperTest.java"}}`)))
	})

	t.Run("writer failure", func(t *testing.T) {
		executorMock := executormock.NewMockExecutor(ctrl)
		ideGatewayMock := ideclientmock.NewMockGateway(ctrl)
		fs := fsmock.NewMockUlspFS(ctrl)

		fs.EXPECT().DirExists(gomock.Any()).Times(0).Return(true, nil)
		fs.EXPECT().ReadDir(gomock.Any()).Times(0).Return([]os.DirEntry{}, nil)

		c := &action.ExecuteParams{
			IdeGateway: ideGatewayMock,
			Sessions:   sessionRepository,
			Executor:   executorMock,
			FileSystem: fs,
		}

		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		ideGatewayMock.EXPECT().GetLogMessageWriter(gomock.Any(), gomock.Any()).Return(nil, errors.New("error"))
		assert.Error(t, a.Execute(ctx, c, []byte(`{"interfaceName": "myInterface", "document": {"uri": "file:///home/user/fievel/roadrunner/application-dw/src/test/java/com/uber/roadrunner/application/exception/GatewayErrorExceptionMapperTest.java"}}`)))
	})

	t.Run("execution failure", func(t *testing.T) {
		executorMock := executormock.NewMockExecutor(ctrl)
		ideGatewayMock := ideclientmock.NewMockGateway(ctrl)
		fs := fsmock.NewMockUlspFS(ctrl)

		fs.EXPECT().DirExists(gomock.Any()).Return(true, nil)
		fs.EXPECT().ReadDir("/home/user/fievel/roadrunner/application-dw").Return([]os.DirEntry{helpers.MockDirEntry("BUILD.bazel", false)}, nil)
		fs.EXPECT().ReadDir(gomock.Any()).Times(9).Return([]os.DirEntry{}, nil)

		c := &action.ExecuteParams{
			IdeGateway: ideGatewayMock,
			Sessions:   sessionRepository,
			Executor:   executorMock,
			FileSystem: fs,
		}

		var writer bytes.Buffer
		ideGatewayMock.EXPECT().GetLogMessageWriter(gomock.Any(), gomock.Any()).Return(&writer, nil)
		ideGatewayMock.EXPECT().LogMessage(gomock.Any(), gomock.Any()).Return(nil).Times(2)
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		executorMock.EXPECT().RunCommand(gomock.Any(), gomock.Any()).Return(errors.New("error"))

		assert.Error(t, a.Execute(ctx, c, []byte(`{"interfaceName": "myInterface", "document": {"uri": "file:///home/user/fievel/roadrunner/application-dw/src/test/java/com/uber/roadrunner/application/exception/GatewayErrorExceptionMapperTest.java"}}`)))
	})
}

func TestJavaTestRunProcessDocument(t *testing.T) {
	a := ActionJavaTestRun{}
	doc := protocol.TextDocumentItem{
		URI:        "file:///MyExampleTest.java",
		LanguageID: "java",
		Text: `package com.uber.rider.growth.jobs;

import com.uber.fievel.testing.base.FievelTestBase;
import org.junit.Test;

public class MyExampleTest extends FievelTestBase {

	@Test
	public void myTestMethod() throws Exception {}
}

	`}
	results, err := a.ProcessDocument(context.Background(), doc)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(results))
	for _, result := range results {
		args := result.(protocol.CodeLens).Command.Arguments[0].(argsJavaTestRun)
		assert.Equal(t, doc.URI, args.Document.URI)
		assert.True(t, args.ClassName == "MyExampleTest" || args.MethodName == "myTestMethod")
	}
}

func TestJavaTestRunProcessDocumentParameterizedTest(t *testing.T) {
	a := ActionJavaTestRun{}
	doc := protocol.TextDocumentItem{
		URI:        "file:///MyExampleTest.java",
		LanguageID: "java",
		Text: `package com.uber.rider.growth.jobs;

import com.uber.fievel.testing.base.FievelTestBase;
import org.junit.Test;

public class MyExampleParamTest extends FievelTestBase {

  @Test
  @Parameters(method = "testAcceptOfferHandlerParams")
  public void myTesParamMethod(GrpcTestCaseX testCase) throws IOException {
}

	`}
	results, err := a.ProcessDocument(context.Background(), doc)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(results))
	for _, result := range results {
		args := result.(protocol.CodeLens).Command.Arguments[0].(argsJavaTestRun)
		assert.Equal(t, doc.URI, args.Document.URI)
		assert.True(t, args.ClassName == "MyExampleParamTest" || args.MethodName == "myTesParamMethod")
	}
}

func TestJavaTestCommandName(t *testing.T) {
	a := ActionJavaTestRun{}
	cmd := a.CommandName()
	assert.True(t, len(cmd) > 0)
}

func TestJavaTestRunShouldEnable(t *testing.T) {
	a := ActionJavaTestRun{}
	s := &entity.Session{
		UUID: factory.UUID(),
		InitializeParams: &protocol.InitializeParams{
			ClientInfo: &protocol.ClientInfo{
				Name: "Something else",
			},
		},
	}
	mce := entity.MonorepoConfigEntry{}

	assert.False(t, a.ShouldEnable(s, mce))

	s.InitializeParams.ClientInfo.Name = string(entity.ClientNameVSCode)
	assert.False(t, a.ShouldEnable(s, mce))
}

func TestJavaTestRunIsRelevantDocument(t *testing.T) {
	a := ActionJavaTestRun{}

	relevantDoc := protocol.TextDocumentItem{URI: "file:///test.java", LanguageID: "java"}
	assert.True(t, a.IsRelevantDocument(nil, relevantDoc))

	irrelevantDoc := protocol.TextDocumentItem{URI: "file:///test.go", LanguageID: "go"}
	assert.False(t, a.IsRelevantDocument(nil, irrelevantDoc))
}

func TestJavaTestRunProvideWorkDoneProgressParams(t *testing.T) {
	a := ActionJavaTestRun{}

	providedParams, err := a.ProvideWorkDoneProgressParams(context.Background(), nil, nil)

	assert.NoError(t, err, "No error should be reported")
	assert.Nil(t, providedParams)
}

func TestJavaTestRunGetTestCaseName(t *testing.T) {
	a := ActionJavaTestRun{}
	t.Run("method name", func(t *testing.T) {
		args := argsJavaTestRun{
			ClassName:  "",
			MethodName: "TestMethod",
		}
		assert.Equal(t, "TestMethod", a.getTestCaseName(args))
	})

	t.Run("class name", func(t *testing.T) {
		args := argsJavaTestRun{
			ClassName:  "DummyClass",
			MethodName: "",
		}
		assert.Equal(t, "DummyClass", a.getTestCaseName(args))
	})
}

func TestJavaTestRunPrepareCommandAndEnv(t *testing.T) {
	a := ActionJavaTestRun{}
	ctx := context.Background()
	var writer bytes.Buffer
	workspaceRoot := "/home/user/fievel"
	target := "//a/b/c:src_main"
	testCaseName := "TestMethod"

	testCases := []struct {
		name            string
		ideClient       string
		expectedToolTag string
	}{
		{
			name:            "vscode",
			ideClient:       "Visual Studio Code",
			expectedToolTag: "--tool_tag=vscode:actions:test",
		},
		{
			name:            "cursor",
			ideClient:       "Cursor",
			expectedToolTag: "--tool_tag=cursor:actions:test",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, env := a.prepareCommandAndEnv(ctx, &writer, workspaceRoot, target, testCaseName, tc.ideClient)

			assert.Equal(t, "/home/user/fievel/tools/bazel", cmd.Path)
			assert.Equal(t, []string{_bazelTest, "--test_filter=" + testCaseName, target, tc.expectedToolTag}, cmd.Args[1:])
			assert.Equal(t, workspaceRoot, cmd.Dir)

			// Verify environment variables
			assert.Contains(t, env, "WORKSPACE_ROOT="+workspaceRoot)
			assert.Contains(t, env, "PROJECT_ROOT="+workspaceRoot)
		})
	}
}
