package jdk

import (
	"context"

	tally "github.com/uber-go/tally/v4"
	pb "github.com/uber/scip-lsp/idl/ulsp/service"
	"github.com/uber/scip-lsp/src/ulsp/controller/jdk"
	"github.com/uber/scip-lsp/src/ulsp/mapper/idl"
	"go.uber.org/yarpc"
)

const (
	_gpdWorkspaceRootHeader = "workspace-root"
)

// Handler represents the jdk service's gRPC API.
type Handler = pb.JDKServer

type handler struct {
	jdk   jdk.Controller
	stats tally.Scope
}

// New constructs a new jdk Handler.
func New(jdk jdk.Controller, stats tally.Scope) Handler {
	return &handler{
		jdk:   jdk,
		stats: stats.SubScope("jdk_handler"),
	}
}

// ResolveBreakpoints resolves a list of breakpoints in a file to its class
func (h *handler) ResolveBreakpoints(ctx context.Context, req *pb.ResolveBreakpointsRequest) (*pb.ResolveBreakpointsResponse, error) {
	call := yarpc.CallFromContext(ctx)
	workspaceRoot := call.Header(_gpdWorkspaceRootHeader)
	res, err := h.jdk.ResolveBreakpoints(ctx, idl.ResolveBreakpointsRequestToResolveBreakpoints(req, workspaceRoot))
	return idl.BreakpointLocationsToBreakpointsResponse(res), err
}

// ResolveClassToPath resolves a JDK class to a file on disk
func (h *handler) ResolveClassToPath(ctx context.Context, req *pb.ResolveClassToPathRequest) (*pb.ResolveClassToPathResponse, error) {
	call := yarpc.CallFromContext(ctx)
	workspaceRoot := call.Header(_gpdWorkspaceRootHeader)
	sourceURI, err := h.jdk.ResolveClassToPath(ctx, idl.ResolveClassToPathRequestToResolveClassToPath(req, workspaceRoot))
	return idl.SourceURIToClassToPathResponse(sourceURI), err
}
