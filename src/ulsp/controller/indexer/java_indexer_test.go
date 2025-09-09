package indexer

import (
	"context"
	"fmt"
	"io"
	"os"
	"testing"

	ulspfs "github.com/uber/scip-lsp/src/ulsp/internal/fs"
	"github.com/uber/scip-lsp/src/ulsp/internal/fs/fsmock"
	"github.com/uber/scip-lsp/src/ulsp/internal/fs/fsmock/helpers"

	"github.com/stretchr/testify/assert"
	"github.com/uber/scip-lsp/src/ulsp/entity"
	"github.com/uber/scip-lsp/src/ulsp/factory"
	"github.com/uber/scip-lsp/src/ulsp/gateway/ide-client/ideclientmock"
	"github.com/uber/scip-lsp/src/ulsp/internal/executor/executormock"
	"go.lsp.dev/protocol"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
)

func TestJavaIndexerSyncIndex(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	s := &entity.Session{}
	s.WorkspaceRoot = "/home/fievel"

	logger := zap.NewNop().Sugar()
	doc := protocol.TextDocumentItem{
		URI: protocol.DocumentURI("file:///home/fievel/sample.java"),
	}
	indexer := NewJavaIndexer(ulspfs.New(), s, io.Discard)

	t.Run("success", func(t *testing.T) {
		executorMock := executormock.NewMockExecutor(ctrl)
		ideGatewayMock := ideclientmock.NewMockGateway(ctrl)

		executorMock.EXPECT().RunCommand(gomock.Any(), gomock.Any()).Return(nil)

		err := indexer.SyncIndex(ctx, executorMock, ideGatewayMock, logger, doc)
		assert.NoError(t, err)
	})

	t.Run("execution failure", func(t *testing.T) {
		executorMock := executormock.NewMockExecutor(ctrl)
		ideGatewayMock := ideclientmock.NewMockGateway(ctrl)

		executorMock.EXPECT().RunCommand(gomock.Any(), gomock.Any()).Return(fmt.Errorf("run error"))
		err := indexer.SyncIndex(ctx, executorMock, ideGatewayMock, logger, doc)
		assert.Error(t, err)
	})
}

func TestJavaIndexerNewJavaIndexer(t *testing.T) {
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	s.WorkspaceRoot = "/home/fievel"
	indexer := NewJavaIndexer(ulspfs.New(), s, io.Discard)
	assert.NotNil(t, indexer)
}

func TestJavaIndexerIsRelevantDocument(t *testing.T) {
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	s.WorkspaceRoot = "/home/fievel"
	indexer := NewJavaIndexer(ulspfs.New(), s, io.Discard)

	t.Run("success java", func(t *testing.T) {
		doc := protocol.TextDocumentItem{
			URI:        "file:///home/fievel/sample.java",
			LanguageID: "java",
		}
		assert.True(t, indexer.IsRelevantDocument(doc))
	})

	t.Run("success scala", func(t *testing.T) {
		doc := protocol.TextDocumentItem{
			URI:        "file:///home/fievel/sample.scala",
			LanguageID: "scala",
		}
		assert.True(t, indexer.IsRelevantDocument(doc))
	})

	t.Run("failure", func(t *testing.T) {
		doc := protocol.TextDocumentItem{
			URI:        "file:///home/fievel/sample.bzl",
			LanguageID: "starlark",
		}
		assert.False(t, indexer.IsRelevantDocument(doc))
	})
}

func TestJavaIndexerGetUniqueIndexKey(t *testing.T) {
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	s.WorkspaceRoot = "/home/user/fievel"
	s.UUID = factory.UUID()

	t.Run("success", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		fs := fsmock.NewMockUlspFS(ctrl)

		fs.EXPECT().DirExists(gomock.Any()).Return(true, nil)
		fs.EXPECT().ReadDir("/home/user/fievel/tooling/intellij").Return([]os.DirEntry{helpers.MockDirEntry("BUILD.bazel", false)}, nil)
		fs.EXPECT().ReadDir(gomock.Any()).Times(4).Return([]os.DirEntry{}, nil)

		indexer := NewJavaIndexer(fs, s, io.Discard)

		validDoc := protocol.TextDocumentItem{
			URI: "file:///home/user/fievel/tooling/intellij/src/intellij/bazel/BazelSyncListener.java",
		}
		expectedKey := s.UUID.String() + "_" + "tooling/intellij/..."
		uniqueKey, err := indexer.GetUniqueIndexKey(validDoc)
		assert.NoError(t, err)
		assert.Equal(t, expectedKey, uniqueKey)
	})

	t.Run("failure", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		fs := fsmock.NewMockUlspFS(ctrl)

		indexer := NewJavaIndexer(fs, s, io.Discard)

		invalidDoc := protocol.TextDocumentItem{
			URI: "file:///home/user/BazelSyncListener.java",
		}
		_, err := indexer.GetUniqueIndexKey(invalidDoc)
		assert.Error(t, err)
	})
}
