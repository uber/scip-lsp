package session

import (
	"context"
	"sync"

	"github.com/gofrs/uuid"
	tally "github.com/uber-go/tally/v4"
	"github.com/uber/scip-lsp/src/ulsp/entity"
	"github.com/uber/scip-lsp/src/ulsp/internal/errors"
	"github.com/uber/scip-lsp/src/ulsp/mapper"
	"github.com/uber/scip-lsp/src/ulsp/model"
)

// Repository is an entity-scoped repository.
type Repository interface {
	Get(context.Context, uuid.UUID) (*entity.Session, error)
	GetFromContext(ctx context.Context) (*entity.Session, error)
	GetAllFromWorkspaceRoot(ctx context.Context, workspaceRoot string) ([]*entity.Session, error)
	Set(context.Context, *entity.Session) error
	Delete(ctx context.Context, id uuid.UUID) error
	SessionCount(ctx context.Context) (int, error)
}

type repository struct {
	mu       sync.Mutex
	memstore map[uuid.UUID]*model.Session
	stats    tally.Scope
}

// New returns a repository to a key-value Session data store.
func New(stats tally.Scope) Repository {
	return &repository{
		memstore: make(map[uuid.UUID]*model.Session),
		stats:    stats,
	}
}

// Get returns the Session associated with the given id.
func (r *repository) Get(ctx context.Context, id uuid.UUID) (*entity.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	f, ok := r.memstore[id]
	if !ok {
		return nil, &errors.UUIDNotFoundError{UUID: id}
	}
	return mapper.ModelToSession(f)
}

// GetFromContext returns the Session associated with the given context.
func (r *repository) GetFromContext(ctx context.Context) (*entity.Session, error) {
	uuid, err := mapper.ContextToSessionUUID(ctx)
	if err != nil {
		return nil, err
	}
	s, err := r.Get(ctx, uuid)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// Set sets the Session to its associated uuid.
func (r *repository) Set(ctx context.Context, f *entity.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if f == nil {
		return errors.New("can't save nil session")
	}
	r.memstore[f.UUID] = mapper.SessionToModel(f)
	r.stats.Gauge("active_connections").Update(float64(len(r.memstore)))
	return nil
}

// Delete removes the Session associated with the given id.
func (r *repository) Delete(ctx context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.memstore, id)
	r.stats.Gauge("active_connections").Update(float64(len(r.memstore)))
	return nil
}

// SessionCount returns the total count of active sessions.
func (r *repository) SessionCount(ctx context.Context) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.memstore), nil
}

// GetAllFromWorkspaceRoot returns all sessions for a specific workspaceRoot.
func (r *repository) GetAllFromWorkspaceRoot(ctx context.Context, workspaceRoot string) ([]*entity.Session, error) {
	found := make([]*entity.Session, 0)
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.memstore {
		if s.WorkspaceRoot == workspaceRoot {
			sess, err := mapper.ModelToSession(s)
			if err == nil {
				found = append(found, sess)
			}
		}
	}

	return found, nil
}
