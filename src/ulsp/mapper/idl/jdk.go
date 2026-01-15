package idl

import (
	modelpb "github.com/uber/scip-lsp/idl/ulsp/model"
	pb "github.com/uber/scip-lsp/idl/ulsp/service"
	"github.com/uber/scip-lsp/src/ulsp/controller/jdk/types"
	"go.lsp.dev/protocol"
)

// ResolveBreakpointsRequestToResolveBreakpoints converts a resolve breakpoints request to a resolve breakpoints.
func ResolveBreakpointsRequestToResolveBreakpoints(req *pb.ResolveBreakpointsRequest, workspaceRoot string) *types.ResolveBreakpoints {
	return &types.ResolveBreakpoints{
		WorkspaceRoot: workspaceRoot,
		SourceURI:     req.SourceUri,
		Breakpoints:   SourceBreakpointsToBreakpointPositions(req.Breakpoints),
	}
}

// ResolveClassToPathRequestToResolveClassToPath converts a resolve class to path request to a resolve class to path.
func ResolveClassToPathRequestToResolveClassToPath(req *pb.ResolveClassToPathRequest, workspaceRoot string) *types.ResolveClassToPath {
	return &types.ResolveClassToPath{
		WorkspaceRoot:      workspaceRoot,
		FullyQualifiedName: req.FullyQualifiedName,
		SourceRelativePath: req.SourceRelativePath,
	}
}

// SourceBreakpointsToBreakpointPositions converts a list of source breakpoints to a list of breakpoint positions.
func SourceBreakpointsToBreakpointPositions(breakpoints []*modelpb.SourceBreakpoint) []*protocol.Position {
	var positions []*protocol.Position
	for _, bp := range breakpoints {
		positions = append(positions, &protocol.Position{
			Line:      bp.Line,
			Character: bp.Column,
		})
	}
	return positions
}

// BreakpointLocationsToBreakpointsResponse converts a list of breakpoint locations to a breakpoint response.
func BreakpointLocationsToBreakpointsResponse(breakpoints []*types.BreakpointLocation) *pb.ResolveBreakpointsResponse {
	var res []*modelpb.JavaBreakpointLocation
	for _, bp := range breakpoints {
		res = append(res, &modelpb.JavaBreakpointLocation{
			Line:   bp.Line,
			Column: bp.Column,

			ClassName:       bp.ClassName,
			MethodName:      bp.MethodName,
			MethodSignature: bp.MethodSignature,
		})
	}
	return &pb.ResolveBreakpointsResponse{
		BreakpointLocations: res,
	}
}

// SourceURIToClassToPathResponse converts a source URI to a class to path response.
func SourceURIToClassToPathResponse(uri string) *pb.ResolveClassToPathResponse {
	return &pb.ResolveClassToPathResponse{
		SourceUri: uri,
	}
}
