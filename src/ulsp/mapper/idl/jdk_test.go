package idl

import (
	"testing"

	"github.com/stretchr/testify/assert"
	modelpb "github.com/uber/scip-lsp/idl/ulsp/model"
	pb "github.com/uber/scip-lsp/idl/ulsp/service"
	"github.com/uber/scip-lsp/src/ulsp/controller/jdk/types"
	"go.lsp.dev/protocol"
)

func TestSourceBreakpointsToBreakpointPositions(t *testing.T) {
	breakpoints := []*modelpb.SourceBreakpoint{
		&modelpb.SourceBreakpoint{
			Line:   10,
			Column: 5,
		},
		&modelpb.SourceBreakpoint{
			Line:   20,
			Column: 15,
		},
	}

	expectedPositions := []*protocol.Position{
		&protocol.Position{
			Line:      10,
			Character: 5,
		},
		&protocol.Position{
			Line:      20,
			Character: 15,
		},
	}

	positions := SourceBreakpointsToBreakpointPositions(breakpoints)

	assert.ElementsMatch(t, expectedPositions, positions)
}

func TestBreakpointLocationsToBreakpointsResponse(t *testing.T) {
	breakpoints := []*types.BreakpointLocation{
		{
			Line:            10,
			Column:          5,
			ClassName:       "MyClass",
			MethodName:      "myMethod",
			MethodSignature: "func()",
		},
		{
			Line:            20,
			Column:          15,
			ClassName:       "AnotherClass",
			MethodName:      "anotherMethod",
			MethodSignature: "func(int)",
		},
	}

	expectedResponse := &pb.ResolveBreakpointsResponse{
		BreakpointLocations: []*modelpb.JavaBreakpointLocation{
			{
				Line:            10,
				Column:          5,
				ClassName:       "MyClass",
				MethodName:      "myMethod",
				MethodSignature: "func()",
			},
			{
				Line:            20,
				Column:          15,
				ClassName:       "AnotherClass",
				MethodName:      "anotherMethod",
				MethodSignature: "func(int)",
			},
		},
	}

	response := BreakpointLocationsToBreakpointsResponse(breakpoints)

	assert.Equal(t, expectedResponse, response)
}

func TestSourceURIToClassToPathResponse(t *testing.T) {
	uri := "test_uri"
	expectedResponse := &pb.ResolveClassToPathResponse{
		SourceUri: uri,
	}

	response := SourceURIToClassToPathResponse(uri)
	assert.Equal(t, expectedResponse, response)
}

func TestResolveBreakpointsRequestToResolveBreakpoints(t *testing.T) {
	wsr := "/some/path"
	req := &pb.ResolveBreakpointsRequest{
		SourceUri: "file:///path/to/file",
		Breakpoints: []*modelpb.SourceBreakpoint{
			&modelpb.SourceBreakpoint{Line: 1, Column: 1},
			&modelpb.SourceBreakpoint{Line: 2, Column: 3},
		},
	}

	expected := &types.ResolveBreakpoints{
		WorkspaceRoot: wsr,
		SourceURI:     "file:///path/to/file",
		Breakpoints: []*protocol.Position{
			&protocol.Position{Line: 1, Character: 1},
			&protocol.Position{Line: 2, Character: 3},
		},
	}

	result := ResolveBreakpointsRequestToResolveBreakpoints(req, wsr)

	assert.Equal(t, expected, result)
}

func TestResolveClassToPathRequestToResolveClassToPath(t *testing.T) {
	wsr := "/some/path"
	req := &pb.ResolveClassToPathRequest{
		FullyQualifiedName: "com.example.MyClass",
		SourceRelativePath: "path/to/source",
	}
	expected := &types.ResolveClassToPath{
		WorkspaceRoot:      wsr,
		FullyQualifiedName: "com.example.MyClass",
		SourceRelativePath: "path/to/source",
	}

	result := ResolveClassToPathRequestToResolveClassToPath(req, wsr)

	assert.Equal(t, expected, result)
}
