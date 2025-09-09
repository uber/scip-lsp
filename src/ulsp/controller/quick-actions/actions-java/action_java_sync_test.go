package quickactions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

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

func TestJavaSyncExecute(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	a := ActionJavaSync{}

	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	s.WorkspaceRoot = "/home/user/fievel"
	s.Monorepo = "lm/fievel"

	t.Run("success", func(t *testing.T) {
		executorMock := executormock.NewMockExecutor(ctrl)
		ideGatewayMock := ideclientmock.NewMockGateway(ctrl)
		c := &action.ExecuteParams{
			IdeGateway: ideGatewayMock,
			Sessions:   sessionRepository,
			Executor:   executorMock,
		}

		var writer bytes.Buffer
		ideGatewayMock.EXPECT().LogMessage(gomock.Any(), gomock.Any()).Return(nil)
		ideGatewayMock.EXPECT().GetLogMessageWriter(gomock.Any(), gomock.Any()).Return(&writer, nil)
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		executorMock.EXPECT().RunCommand(gomock.Any(), gomock.Any()).Return(nil)

		assert.NoError(t, a.Execute(ctx, c, []byte(`{"interfaceName": "myInterface", "document": {"uri": "file:///home/user/fievel/tooling/intellij/uber-intellij-plugin-core/src/main/java/com/uber/intellij/bazel/BazelSyncListener.java"}}`)))
	})

	t.Run("bad args", func(t *testing.T) {
		executorMock := executormock.NewMockExecutor(ctrl)
		ideGatewayMock := ideclientmock.NewMockGateway(ctrl)
		c := &action.ExecuteParams{
			IdeGateway: ideGatewayMock,
			Sessions:   sessionRepository,
			Executor:   executorMock,
		}
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		assert.Error(t, a.Execute(ctx, c, []byte(`{"brokenJSON`)))
	})

	t.Run("log message failure", func(t *testing.T) {
		executorMock := executormock.NewMockExecutor(ctrl)
		ideGatewayMock := ideclientmock.NewMockGateway(ctrl)
		c := &action.ExecuteParams{
			IdeGateway: ideGatewayMock,
			Sessions:   sessionRepository,
			Executor:   executorMock,
		}

		var writer bytes.Buffer
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		ideGatewayMock.EXPECT().GetLogMessageWriter(gomock.Any(), gomock.Any()).Return(&writer, nil)
		ideGatewayMock.EXPECT().LogMessage(gomock.Any(), gomock.Any()).Return(errors.New("error"))

		assert.Error(t, a.Execute(ctx, c, []byte(`{"interfaceName": "myInterface", "document": {"uri": "file:///home/user/fievel/roadrunner/application-dw/src/test/java/com/uber/roadrunner/application/exception/GatewayErrorExceptionMapperTest.java"}}`)))
	})

	t.Run("writer failure", func(t *testing.T) {
		executorMock := executormock.NewMockExecutor(ctrl)
		ideGatewayMock := ideclientmock.NewMockGateway(ctrl)
		c := &action.ExecuteParams{
			IdeGateway: ideGatewayMock,
			Sessions:   sessionRepository,
			Executor:   executorMock,
		}

		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		ideGatewayMock.EXPECT().GetLogMessageWriter(gomock.Any(), gomock.Any()).Return(nil, errors.New("error"))
		assert.Error(t, a.Execute(ctx, c, []byte(`{"interfaceName": "myInterface", "document": {"uri": "file:///home/user/fievel/tooling/intellij/uber-intellij-plugin-core/src/main/java/com/uber/intellij/bazel/BazelSyncListener.java"}}`)))
	})

	t.Run("execution failure", func(t *testing.T) {
		executorMock := executormock.NewMockExecutor(ctrl)
		ideGatewayMock := ideclientmock.NewMockGateway(ctrl)
		c := &action.ExecuteParams{
			IdeGateway: ideGatewayMock,
			Sessions:   sessionRepository,
			Executor:   executorMock,
		}

		var writer bytes.Buffer
		ideGatewayMock.EXPECT().GetLogMessageWriter(gomock.Any(), gomock.Any()).Return(&writer, nil)
		ideGatewayMock.EXPECT().LogMessage(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		executorMock.EXPECT().RunCommand(gomock.Any(), gomock.Any()).Return(errors.New("error"))

		assert.Error(t, a.Execute(ctx, c, []byte(`{"interfaceName": "myInterface", "document": {"uri": "file:///home/user/fievel/tooling/intellij/uber-intellij-plugin-core/src/main/java/com/uber/intellij/bazel/BazelSyncListener.java"}}`)))
	})

}

func TestJavaSyncProcessDocument(t *testing.T) {
	a := ActionJavaSync{}
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
	assert.Equal(t, 1, len(results))
	args := results[0].(protocol.CodeLens).Command.Arguments[0].(argsJavaSync)
	assert.Equal(t, doc.URI, args.Document.URI)
}

func TestJavaSyncCommandName(t *testing.T) {
	a := ActionJavaSync{}
	cmd := a.CommandName()
	assert.True(t, len(cmd) > 0)
}

func TestJavaSyncShouldEnable(t *testing.T) {
	a := ActionJavaSync{}
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	mce := entity.MonorepoConfigEntry{
		Languages: []string{"java"},
	}

	assert.True(t, a.ShouldEnable(s, mce))
}

func TestJavaSyncIsRelevantDocument(t *testing.T) {
	a := ActionJavaSync{}

	relevantDoc := protocol.TextDocumentItem{URI: "file:///test.java", LanguageID: "java"}
	assert.True(t, a.IsRelevantDocument(nil, relevantDoc))

	irrelevantDoc := protocol.TextDocumentItem{URI: "file:///test.go", LanguageID: "go"}
	assert.False(t, a.IsRelevantDocument(nil, irrelevantDoc))
}

func TestJavaSyncGetFilePath(t *testing.T) {
	a := ActionJavaSync{}
	workspaceRoot := "/home/user/fievel"
	t.Run("success", func(t *testing.T) {
		validDoc := protocol.TextDocumentIdentifier{
			URI: "file:///home/user/fievel/tooling/intellij/uber-intellij-plugin-core/src/main/java/com/uber/intellij/bazel/BazelSyncListener.java",
		}
		result, err := a.getRelativeFilePath(workspaceRoot, validDoc)
		expectedPath := "tooling/intellij/uber-intellij-plugin-core/src/main/java/com/uber/intellij/bazel/BazelSyncListener.java"
		assert.NoError(t, err)
		assert.Equal(t, expectedPath, result)
	})

	t.Run("failure", func(t *testing.T) {
		invalidDoc := protocol.TextDocumentIdentifier{
			URI: "file:///home/user/go-code/sample.go",
		}
		_, err := a.getRelativeFilePath(workspaceRoot, invalidDoc)
		assert.Error(t, err)
	})

}

func TestJavaSyncProvideWorkDoneProgressParams(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()
	a := ActionJavaSync{}

	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	s.WorkspaceRoot = "/home/user/fievel"
	s.Monorepo = "lm/fievel"

	rawArgs := []byte(`{"interfaceName": "myInterface", "document": {"uri": "file:///home/user/fievel/tooling/src/main/java/com/uber/intellij/bazel/BazelSync.java"}}`)

	execParams := &action.ExecuteParams{
		Sessions: sessionRepository,
	}
	t.Run("Success", func(t *testing.T) {
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		params, err := a.ProvideWorkDoneProgressParams(ctx, execParams, rawArgs)
		assert.NoError(t, err, "success scenario no error should be thrown")
		assert.NotNil(t, params, "WorkDoneProgressParams should not be nil")
		assert.Equal(t, params.Title, fmt.Sprintf(_titleJavaSyncProgressBar, "BazelSync.java"), "Title should be Syncing Bazel")
		assert.Equal(t, params.Message, _messageJavaSyncProgressBar, "Message should be Syncing Bazel")
	})

	t.Run("Context Error", func(t *testing.T) {
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(nil, errors.New("ctx error"))
		_, err := a.ProvideWorkDoneProgressParams(ctx, execParams, rawArgs)
		assert.Error(t, err, "context error should be thrown")
	})

	t.Run("invalid path", func(t *testing.T) {
		invalidPathArgs := []byte(`{"interfaceName": "myInterface", "document": {"uri": "file:///home/user/src/..."}}`)
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		_, err := a.ProvideWorkDoneProgressParams(ctx, execParams, invalidPathArgs)
		assert.Error(t, err, "invalid path error should be thrown")

	})
}
