package quickactions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

func TestJavaBuildExecute(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()
	a := ActionJavaBuild{}

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
		fs.EXPECT().ReadDir("/home/user/fievel/tooling/intellij/uber-intellij-plugin-core").Return([]os.DirEntry{helpers.MockDirEntry("BUILD.bazel", false)}, nil)
		fs.EXPECT().ReadDir(gomock.Any()).Times(8).Return([]os.DirEntry{}, nil)

		params := &action.ExecuteParams{
			IdeGateway: ideGatewayMock,
			Sessions:   sessionRepository,
			Executor:   executorMock,
			FileSystem: fs,
		}

		var writer bytes.Buffer
		ideGatewayMock.EXPECT().LogMessage(gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
		ideGatewayMock.EXPECT().GetLogMessageWriter(gomock.Any(), gomock.Any()).Return(&writer, nil)
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		executorMock.EXPECT().RunCommand(gomock.Any(), gomock.Any()).Return(nil)

		assert.NoError(t, a.Execute(ctx, params, []byte(`{"interfaceName": "myInterface", "document": {"uri": "file:///home/user/fievel/tooling/intellij/uber-intellij-plugin-core/src/main/java/com/uber/intellij/bazel/BazelSyncListener.java"}}`)))
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
		fs.EXPECT().ReadDir("/home/user/fievel/tooling/intellij/uber-intellij-plugin-core").Return([]os.DirEntry{helpers.MockDirEntry("BUILD.bazel", false)}, nil)
		fs.EXPECT().ReadDir(gomock.Any()).Times(8).Return([]os.DirEntry{}, nil)

		c := &action.ExecuteParams{
			IdeGateway: ideGatewayMock,
			Sessions:   sessionRepository,
			Executor:   executorMock,
			FileSystem: fs,
		}

		var writer bytes.Buffer
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		ideGatewayMock.EXPECT().GetLogMessageWriter(gomock.Any(), gomock.Any()).Return(&writer, nil)
		ideGatewayMock.EXPECT().LogMessage(gomock.Any(), gomock.Any()).AnyTimes().Return(errors.New("error"))

		assert.Error(t, a.Execute(ctx, c, []byte(`{"interfaceName": "myInterface", "document": {"uri": "file:///home/user/fievel/tooling/intellij/uber-intellij-plugin-core/src/main/java/com/uber/intellij/bazel/BazelSyncListener.java"}}`)))
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
		assert.Error(t, a.Execute(ctx, c, []byte(`{"interfaceName": "myInterface", "document": {"uri": "file:///home/user/fievel/tooling/intellij/uber-intellij-plugin-core/src/main/java/com/uber/intellij/bazel/BazelSyncListener.java"}}`)))
	})

	t.Run("execution failure", func(t *testing.T) {
		executorMock := executormock.NewMockExecutor(ctrl)
		ideGatewayMock := ideclientmock.NewMockGateway(ctrl)
		fs := fsmock.NewMockUlspFS(ctrl)

		fs.EXPECT().DirExists(gomock.Any()).Return(true, nil)
		fs.EXPECT().ReadDir("/home/user/fievel/tooling/intellij/uber-intellij-plugin-core").Return([]os.DirEntry{helpers.MockDirEntry("BUILD.bazel", false)}, nil)
		fs.EXPECT().ReadDir(gomock.Any()).Times(8).Return([]os.DirEntry{}, nil)

		c := &action.ExecuteParams{
			IdeGateway: ideGatewayMock,
			Sessions:   sessionRepository,
			Executor:   executorMock,
			FileSystem: fs,
		}

		var writer bytes.Buffer
		ideGatewayMock.EXPECT().GetLogMessageWriter(gomock.Any(), gomock.Any()).Return(&writer, nil)
		ideGatewayMock.EXPECT().LogMessage(gomock.Any(), gomock.Any()).AnyTimes().Return(nil).AnyTimes()
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		executorMock.EXPECT().RunCommand(gomock.Any(), gomock.Any()).Return(errors.New("error"))

		assert.Error(t, a.Execute(ctx, c, []byte(`{"interfaceName": "myInterface", "document": {"uri": "file:///home/user/fievel/tooling/intellij/uber-intellij-plugin-core/src/main/java/com/uber/intellij/bazel/BazelSyncListener.java"}}`)))
	})
}

func TestJavaBuildProvideWorkDoneProgressParams(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	a := ActionJavaBuild{}

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

	t.Run("Success", func(t *testing.T) {
		executorMock := executormock.NewMockExecutor(ctrl)
		ideGatewayMock := ideclientmock.NewMockGateway(ctrl)
		fs := fsmock.NewMockUlspFS(ctrl)

		fs.EXPECT().DirExists(gomock.Any()).Return(true, nil)
		fs.EXPECT().ReadDir("/home/user/fievel/tooling").Return([]os.DirEntry{helpers.MockDirEntry("BUILD.bazel", false)}, nil)
		fs.EXPECT().ReadDir(gomock.Any()).Times(8).Return([]os.DirEntry{}, nil)

		params := &action.ExecuteParams{
			IdeGateway: ideGatewayMock,
			Sessions:   sessionRepository,
			Executor:   executorMock,
			FileSystem: fs,
		}

		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)

		providedParams, err := a.ProvideWorkDoneProgressParams(ctx, params, []byte(`{"interfaceName": "myInterface", "document": {"uri": "file:///home/user/fievel/tooling/src/main/java/com/uber/intellij/bazel/BazelSync.java"}}`))

		assert.NoError(t, err, "No error should be reported")
		assert.NotNil(t, providedParams)
		assert.Contains(t, fmt.Sprintf("%v", providedParams.Title), "Build")
	})
}

func TestJavaBuildCommandName(t *testing.T) {
	a := ActionJavaBuild{}
	cmd := a.CommandName()
	assert.True(t, len(cmd) > 0)
}

func TestJavaBuildShouldEnable(t *testing.T) {
	a := ActionJavaBuild{}
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

func TestJavaBuildIsRelevantDocument(t *testing.T) {
	a := ActionJavaBuild{}

	relevantDoc := protocol.TextDocumentItem{URI: "file:///test.java", LanguageID: "java"}
	assert.True(t, a.IsRelevantDocument(nil, relevantDoc))

	irrelevantDoc := protocol.TextDocumentItem{URI: "file:///test.go", LanguageID: "go"}
	assert.False(t, a.IsRelevantDocument(nil, irrelevantDoc))
}

func TestJavaBuildPrepareCommandAndEnv(t *testing.T) {
	a := ActionJavaBuild{}
	ctx := context.Background()
	var writer bytes.Buffer
	workspaceRoot := "/home/user/fievel"
	target := "//a/b/c:src_main"

	testCases := []struct {
		name            string
		ideClient       string
		expectedToolTag string
	}{
		{
			name:            "vscode",
			ideClient:       "Visual Studio Code",
			expectedToolTag: "--tool_tag=vscode:actions:build",
		},
		{
			name:            "cursor",
			ideClient:       "Cursor",
			expectedToolTag: "--tool_tag=cursor:actions:build",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, env := a.prepareCommandAndEnv(ctx, &writer, workspaceRoot, target, tc.ideClient)

			assert.Equal(t, "/home/user/fievel/tools/bazel", cmd.Path)
			assert.Equal(t, []string{_bazelBuild, target, tc.expectedToolTag}, cmd.Args[1:])
			assert.Equal(t, workspaceRoot, cmd.Dir)

			// Verify environment variables
			assert.Contains(t, env, "WORKSPACE_ROOT="+workspaceRoot)
			assert.Contains(t, env, "PROJECT_ROOT="+workspaceRoot)
		})
	}
}
