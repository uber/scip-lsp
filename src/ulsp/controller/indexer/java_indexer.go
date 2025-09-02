package indexer

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path"

	"github.com/uber/scip-lsp/src/ulsp/internal/fs"

	"github.com/uber/scip-lsp/src/ulsp/entity"
	ideclient "github.com/uber/scip-lsp/src/ulsp/gateway/ide-client"
	"github.com/uber/scip-lsp/src/ulsp/internal/executor"
	javautils "github.com/uber/scip-lsp/src/ulsp/internal/java-utils"
	"go.lsp.dev/protocol"
	"go.uber.org/zap"
)

const (
	_javaIndexer             = "javaIndexer"
	_javaIndexerRelativePath = "tools/scip/scip_sync.sh"
	_fpOpt                   = "--filepath"
	_logFileKey              = "indexer-logs"
)

type javaIndexer struct {
	path         string
	session      *entity.Session
	outputWriter io.Writer
	fs           fs.UlspFS
}

// NewJavaIndexer return a new java indexer
func NewJavaIndexer(fs fs.UlspFS, s *entity.Session, outputWriter io.Writer) Indexer {
	indexerPath := path.Join(s.WorkspaceRoot, _javaIndexerRelativePath)

	return &javaIndexer{
		session:      s,
		path:         indexerPath,
		outputWriter: outputWriter,
		fs:           fs,
	}
}

// SyncIndex executes the java indexer
func (j *javaIndexer) SyncIndex(ctx context.Context, executor executor.Executor, ideGateway ideclient.Gateway, logger *zap.SugaredLogger, doc protocol.TextDocumentItem) error {
	filepath := doc.URI.Filename()
	wsRoot := j.session.WorkspaceRoot
	logger.Infof("Executing java indexer for file: %s", filepath)

	cmdArgs := []string{_fpOpt, filepath}
	cmd := exec.CommandContext(ctx, j.path, cmdArgs...)
	cmd.Stderr = j.outputWriter
	cmd.Stdout = j.outputWriter
	cmd.Dir = wsRoot

	// Workspace and Project root is expected by bunch of java scripts
	env := UpdateEnv(j.session.Env, wsRoot)

	logger.Infof("Executing command exec: %s", cmd.String())
	err := executor.RunCommand(cmd, env)
	if err != nil {
		j.outputWriter.Write([]byte(fmt.Sprintf("Error during run: %s", err)))
	}
	return err
}

// IsRelevantDocument checks if the document is relevant for java indexer
func (j *javaIndexer) IsRelevantDocument(document protocol.TextDocumentItem) bool {
	if document.LanguageID == "java" || document.LanguageID == "scala" {
		return true
	}
	return false
}

// GetUniqueIndexKey returns a unique key for the document,
// this will be used to determine if additional relevant indexing is in progress for this document
func (j *javaIndexer) GetUniqueIndexKey(document protocol.TextDocumentItem) (string, error) {
	target, err := javautils.GetJavaTarget(j.fs, j.session.WorkspaceRoot, document.URI)
	if err != nil {
		return "", err
	}
	uniqueKey := j.session.UUID.String() + "_" + target
	return uniqueKey, nil
}
