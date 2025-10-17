package mapper

import (
	"context"

	"github.com/gofrs/uuid"
	"github.com/uber/scip-lsp/src/ulsp/entity"
	"github.com/uber/scip-lsp/src/ulsp/internal/errors"
	"github.com/uber/scip-lsp/src/ulsp/model"
	"go.lsp.dev/jsonrpc2"
)

// UlspDaemonToModel maps an UlspDaemon entity to its model equvialent.
func UlspDaemonToModel(f *entity.UlspDaemon) *model.UlspDaemon {
	return &model.UlspDaemon{
		Name: f.Name,
		UUID: f.UUID,
	}
}

// ModelToUlspDaemon maps a model UlspDaemon to its entity equvialent.
func ModelToUlspDaemon(f *model.UlspDaemon) (*entity.UlspDaemon, error) {
	return &entity.UlspDaemon{
		Name: f.Name,
		UUID: f.UUID,
	}, nil
}

// UUIDToModel maps a UUID to a UlspDaemon model. This is used to
// populate a UlspDaemon entity.
func UUIDToModel(u uuid.UUID) *model.UlspDaemon {
	return &model.UlspDaemon{
		UUID: u,
	}
}

// SessionToModel maps a Session entity to its model equivalent.
func SessionToModel(f *entity.Session) *model.Session {
	return &model.Session{
		UUID:             f.UUID,
		InitializeParams: f.InitializeParams,
		Conn:             f.Conn,
		WorkspaceRoot:    f.WorkspaceRoot,
		Monorepo:         string(f.Monorepo),
		Env:              f.Env,
		UlspEnabled:      f.UlspEnabled,
	}
}

// ModelToSession maps a model Session to its entity equivalent.
func ModelToSession(f *model.Session) (*entity.Session, error) {
	return &entity.Session{
		UUID:             f.UUID,
		InitializeParams: f.InitializeParams,
		Conn:             f.Conn,
		WorkspaceRoot:    f.WorkspaceRoot,
		Monorepo:         entity.MonorepoName(f.Monorepo),
		Env:              f.Env,
		UlspEnabled:      f.UlspEnabled,
	}, nil
}

// UUIDToSession initializes a new Session entity with the assigned uuid and connection.
func UUIDToSession(u uuid.UUID, c *jsonrpc2.Conn) *entity.Session {
	return &entity.Session{
		UUID:        u,
		Conn:        c,
		UlspEnabled: true,
	}
}

// ContextToSessionUUID extracts the UUID from a context
func ContextToSessionUUID(c context.Context) (uuid.UUID, error) {
	s, ok := c.Value(entity.SessionContextKey).(uuid.UUID)
	if !ok {
		return uuid.Nil, &errors.NoSessionFoundError{}
	}
	return s, nil
}
