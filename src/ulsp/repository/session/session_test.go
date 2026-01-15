package session

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tally "github.com/uber-go/tally/v4"
	"github.com/uber/scip-lsp/src/ulsp/entity"
	"github.com/uber/scip-lsp/src/ulsp/internal/errors"
	"go.uber.org/goleak"
)

func TestSessionRepository(t *testing.T) {
	testScope := tally.NewTestScope("testing", make(map[string]string, 0))
	t.Run("should Set and Get successfully", func(t *testing.T) {
		var uuid uuid.UUID
		model := &entity.Session{
			UUID: uuid,
		}

		repository := New(testScope)

		err := repository.Set(context.Background(), model)
		require.NoError(t, err)
		val, err := repository.Get(context.Background(), uuid)
		require.NoError(t, err)
		assert.Equal(t, uuid, val.UUID)
	})

	t.Run("should fail to get something that was not Set", func(t *testing.T) {
		repository := New(testScope)

		id := uuid.Must(uuid.NewV4())
		_, err := repository.Get(context.Background(), id)
		require.Error(t, err)
		var nf *errors.UUIDNotFoundError
		require.ErrorAs(t, err, &nf)
		assert.Equal(t, id, nf.UUID)
	})
}

func TestGetFromContext(t *testing.T) {
	testScope := tally.NewTestScope("testing", make(map[string]string, 0))
	t.Run("should get when uuid is in context", func(t *testing.T) {
		var uuid uuid.UUID
		model := &entity.Session{
			UUID: uuid,
		}

		repository := New(testScope)
		ctx := context.WithValue(context.Background(), entity.SessionContextKey, uuid)
		err := repository.Set(ctx, model)
		require.NoError(t, err)
		val, err := repository.GetFromContext(ctx)
		assert.NoError(t, err)
		assert.Equal(t, uuid, val.UUID)
	})

	t.Run("should fail when uuid is missing from context", func(t *testing.T) {
		repository := New(testScope)

		_, err := repository.GetFromContext(context.Background())
		require.Error(t, err)
	})

	t.Run("should fail in context is not set in repository", func(t *testing.T) {
		var uuid uuid.UUID
		repository := New(testScope)
		ctx := context.WithValue(context.Background(), entity.SessionContextKey, uuid)
		_, err := repository.GetFromContext(ctx)
		assert.Error(t, err)
	})
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	testScope := tally.NewTestScope("testing", make(map[string]string, 0))
	repository := New(testScope)

	session1 := &entity.Session{
		UUID: uuid.Must(uuid.NewV4()),
	}
	session2 := &entity.Session{
		UUID: uuid.Must(uuid.NewV4()),
	}

	repository.Set(ctx, session1)
	repository.Set(ctx, session2)

	// First deletion is successful. Multiple deletions return no error.
	assert.NoError(t, repository.Delete(ctx, session2.UUID))
	assert.NoError(t, repository.Delete(ctx, session2.UUID))
	_, err := repository.Get(ctx, session2.UUID)
	assert.Error(t, err)

	// Other session unaffected.
	result, err := repository.Get(ctx, session1.UUID)
	assert.NoError(t, err)
	assert.Equal(t, session1, result)
}

func TestSessionCount(t *testing.T) {
	ctx := context.Background()
	testScope := tally.NewTestScope("testing", make(map[string]string, 0))
	repository := New(testScope)

	session1 := &entity.Session{
		UUID: uuid.Must(uuid.NewV4()),
	}
	session2 := &entity.Session{
		UUID: uuid.Must(uuid.NewV4()),
	}

	// New empty repository
	count, err := repository.SessionCount(ctx)
	assert.Equal(t, 0, count)
	assert.NoError(t, err)

	repository.Set(ctx, session1)
	repository.Set(ctx, session2)

	// Count updated after adding/removing sessions
	count, _ = repository.SessionCount(ctx)
	assert.Equal(t, 2, count)
	assert.NoError(t, err)

	repository.Delete(ctx, session2.UUID)
	count, _ = repository.SessionCount(ctx)
	assert.Equal(t, 1, count)
	assert.NoError(t, err)

	repository.Delete(ctx, session1.UUID)
	count, _ = repository.SessionCount(ctx)
	assert.Equal(t, 0, count)
	assert.NoError(t, err)
}

func TestGetAllFromWorkspaceRoot(t *testing.T) {
	ctx := context.Background()
	testScope := tally.NewTestScope("testing", make(map[string]string, 0))
	repository := New(testScope)

	session1 := &entity.Session{
		UUID:          uuid.Must(uuid.NewV4()),
		WorkspaceRoot: "root1",
	}
	session2 := &entity.Session{
		UUID:          uuid.Must(uuid.NewV4()),
		WorkspaceRoot: "root2",
	}
	session3 := &entity.Session{
		UUID:          uuid.Must(uuid.NewV4()),
		WorkspaceRoot: "root1",
	}

	session1.WorkspaceRoot = "root1"
	session2.WorkspaceRoot = "root2"
	session3.WorkspaceRoot = "root1"

	repository.Set(ctx, session1)
	repository.Set(ctx, session2)
	repository.Set(ctx, session3)

	// Get all sessions from workspace root
	sessions, err := repository.GetAllFromWorkspaceRoot(ctx, "root1")
	assert.NoError(t, err)
	assert.Equal(t, 2, len(sessions))
	assert.Contains(t, sessions, session1)
	assert.Contains(t, sessions, session3)

	// No sessions from non-existent workspace root
	sessions, err = repository.GetAllFromWorkspaceRoot(ctx, "root3")
	assert.NoError(t, err)
	assert.Empty(t, sessions)
}

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
