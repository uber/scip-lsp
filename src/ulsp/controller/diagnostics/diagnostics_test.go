package diagnostics

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tally "github.com/uber-go/tally/v4"
	"github.com/uber/scip-lsp/src/ulsp/entity"
	"github.com/uber/scip-lsp/src/ulsp/factory"
	"github.com/uber/scip-lsp/src/ulsp/gateway/ide-client/ideclientmock"
	"github.com/uber/scip-lsp/src/ulsp/repository/session/repositorymock"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
)

func TestNew(t *testing.T) {
	assert.NotPanics(t, func() {
		New(Params{
			Stats:  tally.NewTestScope("testing", make(map[string]string, 0)),
			Logger: zap.NewNop().Sugar(),
		})
	})
}

func TestStartupInfo(t *testing.T) {
	ctx := context.Background()
	c := controller{}
	result, err := c.StartupInfo(ctx)

	assert.NoError(t, err)
	assert.NoError(t, result.Validate())
	assert.Equal(t, _nameKey, result.NameKey)
}

func TestInitialize(t *testing.T) {
	ctrl := gomock.NewController(t)
	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)

	ctx := context.WithValue(context.Background(), entity.SessionContextKey, s.UUID)

	c := controller{
		sessions:    sessionRepository,
		diagnostics: make(diagnosticStore),
		stats:       tally.NewTestScope("testing", make(map[string]string, 0)),
	}
	initParams := &protocol.InitializeParams{}
	initResult := &protocol.InitializeResult{}
	err := c.initialize(ctx, initParams, initResult)

	assert.NoError(t, err)
	_, ok := c.diagnostics[s.UUID]
	assert.True(t, ok)
	assert.Len(t, c.diagnostics, 1)
}

func TestShutdown(t *testing.T) {
	ctrl := gomock.NewController(t)
	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)

	ctx := context.WithValue(context.Background(), entity.SessionContextKey, s.UUID)

	c := controller{
		sessions:    sessionRepository,
		diagnostics: make(diagnosticStore),
		stats:       tally.NewTestScope("testing", make(map[string]string, 0)),
	}

	c.diagnostics[s.UUID] = make(map[uri.URI][]*protocol.Diagnostic)
	_, ok := c.diagnostics[s.UUID]
	require.True(t, ok)

	err := c.shutdown(ctx)
	assert.NoError(t, err)

	_, ok = c.diagnostics[s.UUID]
	assert.False(t, ok)
	assert.Len(t, c.diagnostics, 0)
}

func TestApplyDiagnosticsSuccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	sessionRepository := repositorymock.NewMockRepository(ctrl)
	ideGatewayMock := ideclientmock.NewMockGateway(ctrl)
	s1 := &entity.Session{
		UUID: factory.UUID(),
	}
	s2 := &entity.Session{
		UUID: factory.UUID(),
	}

	ctx := context.Background()
	docURI := uri.URI("file://my/path/file.go")

	sampleDiagnostics := []*protocol.Diagnostic{
		{
			Range: protocol.Range{
				Start: protocol.Position{Line: 1, Character: 1},
				End:   protocol.Position{Line: 1, Character: 5},
			},
			Severity: protocol.DiagnosticSeverityError,
			Message:  "test error",
		},
	}

	sessionRepository.EXPECT().GetAllFromWorkspaceRoot(gomock.Any(), gomock.Any()).Return([]*entity.Session{s1, s2}, nil)
	ideGatewayMock.EXPECT().PublishDiagnostics(gomock.Any(), gomock.Any()).Return(nil).Times(2) // Once for each session

	c := controller{
		sessions:    sessionRepository,
		ideGateway:  ideGatewayMock,
		logger:      zap.NewNop().Sugar(),
		diagnostics: make(diagnosticStore),
		stats:       tally.NewTestScope("testing", make(map[string]string, 0)),
	}

	err := c.ApplyDiagnostics(ctx, "/home/workspace", docURI, sampleDiagnostics)
	assert.NoError(t, err)

	// Verify diagnostics were stored for both sessions
	assert.Equal(t, sampleDiagnostics, c.diagnostics[s1.UUID][docURI])
	assert.Equal(t, sampleDiagnostics, c.diagnostics[s2.UUID][docURI])
}

func TestApplyDiagnosticsFailedSession(t *testing.T) {
	ctrl := gomock.NewController(t)
	sessionRepository := repositorymock.NewMockRepository(ctrl)
	ideGatewayMock := ideclientmock.NewMockGateway(ctrl)
	ctx := context.Background()
	docURI := uri.URI("file://my/path/file.go")

	sampleDiagnostics := []*protocol.Diagnostic{
		{
			Range: protocol.Range{
				Start: protocol.Position{Line: 1, Character: 1},
				End:   protocol.Position{Line: 1, Character: 5},
			},
			Severity: protocol.DiagnosticSeverityError,
			Message:  "test error",
		},
	}

	sessionRepository.EXPECT().GetAllFromWorkspaceRoot(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("failed to get sessions"))

	c := controller{
		sessions:    sessionRepository,
		ideGateway:  ideGatewayMock,
		logger:      zap.NewNop().Sugar(),
		diagnostics: make(diagnosticStore),
		stats:       tally.NewTestScope("testing", make(map[string]string, 0)),
	}

	err := c.ApplyDiagnostics(ctx, "/home/workspace", docURI, sampleDiagnostics)
	assert.Error(t, err)
}

func TestApplyDiagnosticsPublishError(t *testing.T) {
	ctrl := gomock.NewController(t)
	sessionRepository := repositorymock.NewMockRepository(ctrl)
	ideGatewayMock := ideclientmock.NewMockGateway(ctrl)
	s1 := &entity.Session{
		UUID: factory.UUID(),
	}

	ctx := context.Background()
	docURI := uri.URI("file://my/path/file.go")

	sampleDiagnostics := []*protocol.Diagnostic{
		{
			Range: protocol.Range{
				Start: protocol.Position{Line: 1, Character: 1},
				End:   protocol.Position{Line: 1, Character: 5},
			},
			Severity: protocol.DiagnosticSeverityError,
			Message:  "test error",
		},
	}

	sessionRepository.EXPECT().GetAllFromWorkspaceRoot(gomock.Any(), gomock.Any()).Return([]*entity.Session{s1}, nil)
	ideGatewayMock.EXPECT().PublishDiagnostics(gomock.Any(), gomock.Any()).Return(fmt.Errorf("publish failed"))

	c := controller{
		sessions:    sessionRepository,
		ideGateway:  ideGatewayMock,
		logger:      zap.NewNop().Sugar(),
		diagnostics: make(diagnosticStore),
		stats:       tally.NewTestScope("testing", make(map[string]string, 0)),
	}

	// Should not return error even if publishing fails
	err := c.ApplyDiagnostics(ctx, "/home/workspace", docURI, sampleDiagnostics)
	assert.NoError(t, err)

	// Verify diagnostics were still stored
	assert.Equal(t, sampleDiagnostics, c.diagnostics[s1.UUID][docURI])
}

func TestApplyDiagnosticsNoSessions(t *testing.T) {
	ctrl := gomock.NewController(t)
	sessionRepository := repositorymock.NewMockRepository(ctrl)
	ideGatewayMock := ideclientmock.NewMockGateway(ctrl)

	ctx := context.Background()
	docURI := uri.URI("file://my/path/file.go")

	sampleDiagnostics := []*protocol.Diagnostic{
		{
			Range: protocol.Range{
				Start: protocol.Position{Line: 1, Character: 1},
				End:   protocol.Position{Line: 1, Character: 5},
			},
			Severity: protocol.DiagnosticSeverityError,
			Message:  "test error",
		},
	}

	sessionRepository.EXPECT().GetAllFromWorkspaceRoot(gomock.Any(), gomock.Any()).Return([]*entity.Session{}, nil)

	c := controller{
		sessions:    sessionRepository,
		ideGateway:  ideGatewayMock,
		logger:      zap.NewNop().Sugar(),
		diagnostics: make(diagnosticStore),
		stats:       tally.NewTestScope("testing", make(map[string]string, 0)),
	}

	err := c.ApplyDiagnostics(ctx, "/home/workspace", docURI, sampleDiagnostics)
	assert.NoError(t, err)
}
