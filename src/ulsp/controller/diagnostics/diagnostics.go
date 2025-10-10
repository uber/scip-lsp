package diagnostics

import (
	"context"
	"sync"

	"github.com/gofrs/uuid"
	tally "github.com/uber-go/tally/v4"
	"github.com/uber/scip-lsp/src/ulsp/entity"
	ulspplugin "github.com/uber/scip-lsp/src/ulsp/entity/ulsp-plugin"
	ideclient "github.com/uber/scip-lsp/src/ulsp/gateway/ide-client"
	"github.com/uber/scip-lsp/src/ulsp/repository/session"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
	"go.uber.org/config"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

const (
	_nameKey = "diagnostics"
)

// Controller defines the interface for a document sync controller.
type Controller interface {
	StartupInfo(ctx context.Context) (ulspplugin.PluginInfo, error)
	// ResetBase resets the base for a given document.
	ApplyDiagnostics(ctx context.Context, workspaceRoot string, URI uri.URI, diagnostic []*protocol.Diagnostic) error
}

// StartupInfo returns PluginInfo for this controller.
func (c *controller) StartupInfo(ctx context.Context) (ulspplugin.PluginInfo, error) {
	// Set a priority for each method that this module provides.
	priorities := map[string]ulspplugin.Priority{
		protocol.MethodInitialize: ulspplugin.PriorityHigh,
		protocol.MethodShutdown:   ulspplugin.PriorityAsync,

		ulspplugin.MethodEndSession: ulspplugin.PriorityRegular,
	}

	// Assign method keys to implementations.
	methods := &ulspplugin.Methods{
		PluginNameKey: _nameKey,

		Initialize: c.initialize,
		Shutdown:   c.shutdown,

		EndSession: c.endSession,
	}

	return ulspplugin.PluginInfo{
		Priorities: priorities,
		Methods:    methods,
		NameKey:    _nameKey,
	}, nil
}

// Params are inbound parameters to initialize a new plugin.
type Params struct {
	fx.In

	Sessions   session.Repository
	IdeGateway ideclient.Gateway
	Logger     *zap.SugaredLogger
	Stats      tally.Scope
	Config     config.Provider
}

type diagnosticStore map[uuid.UUID]map[uri.URI][]*protocol.Diagnostic

type controller struct {
	sessions      session.Repository
	ideGateway    ideclient.Gateway
	logger        *zap.SugaredLogger
	diagnostics   diagnosticStore
	diagnosticsMu sync.Mutex
	stats         tally.Scope
}

func (c *controller) ApplyDiagnostics(ctx context.Context, workspaceRoot string, docURI uri.URI, diagnostic []*protocol.Diagnostic) error {
	sessions, err := c.sessions.GetAllFromWorkspaceRoot(ctx, workspaceRoot)
	if err != nil {
		return err
	}

	c.diagnosticsMu.Lock()
	defer c.diagnosticsMu.Unlock()

	for _, s := range sessions {
		if _, ok := c.diagnostics[s.UUID]; !ok {
			c.diagnostics[s.UUID] = make(map[uri.URI][]*protocol.Diagnostic)
		}

		c.diagnostics[s.UUID][docURI] = diagnostic

		sCtx := context.WithValue(ctx, entity.SessionContextKey, s.UUID)

		// Convert pointers to values
		diagnostics := make([]protocol.Diagnostic, 0, len(diagnostic))
		for _, d := range diagnostic {
			diagnostics = append(diagnostics, *d)
		}

		c.logger.Debugf("Publishing %q for %s", diagnostics, docURI)

		pubErr := c.ideGateway.PublishDiagnostics(sCtx, &protocol.PublishDiagnosticsParams{
			URI:         docURI,
			Diagnostics: diagnostics,
		})

		if pubErr != nil {
			c.logger.Errorf("Error publishing diagnostics: %s", docURI)
		}
	}
	return nil
}

// New creates a new controller for document sync.
func New(p Params) Controller {
	c := &controller{
		sessions:    p.Sessions,
		ideGateway:  p.IdeGateway,
		logger:      p.Logger.With("plugin", _nameKey),
		diagnostics: make(diagnosticStore),
		stats:       p.Stats.SubScope(_nameKey),
	}
	return c
}

// initialize adds an entry to keep track of this session's documents.
func (c *controller) initialize(ctx context.Context, params *protocol.InitializeParams, result *protocol.InitializeResult) error {
	s, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return err
	}

	c.diagnosticsMu.Lock()
	defer c.diagnosticsMu.Unlock()
	c.diagnostics[s.UUID] = make(map[uri.URI][]*protocol.Diagnostic)
	return nil
}

// shutdown removes this session's documents.
func (c *controller) shutdown(ctx context.Context) error {
	defer c.updateMetrics(ctx, []*protocol.Diagnostic{})
	s, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return err
	}

	return c.disposeSession(ctx, s.UUID)
}

// endSession removes this session's documents in the event that no shutdown request is received.
func (c *controller) endSession(ctx context.Context, uuid uuid.UUID) error {
	defer c.updateMetrics(ctx, []*protocol.Diagnostic{})
	return c.disposeSession(ctx, uuid)
}

// disposeSession removes a session's documents based on the session UUID.
func (c *controller) disposeSession(ctx context.Context, uuid uuid.UUID) error {
	c.diagnosticsMu.Lock()
	defer c.diagnosticsMu.Unlock()
	delete(c.diagnostics, uuid)

	return nil
}

func (c *controller) updateMetrics(ctx context.Context, diagnostics []*protocol.Diagnostic) {
	// not sure if this is the right metric to use
	c.stats.Counter("reported").Inc(int64(len(diagnostics)))
	c.stats.Counter("runs").Inc(1)
}
