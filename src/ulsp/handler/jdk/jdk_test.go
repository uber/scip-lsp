package jdk

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	tally "github.com/uber-go/tally/v4"
	modelpb "github.com/uber/scip-lsp/idl/ulsp/model"
	pb "github.com/uber/scip-lsp/idl/ulsp/service"
	"github.com/uber/scip-lsp/src/ulsp/controller/jdk/jdkmock"
	"github.com/uber/scip-lsp/src/ulsp/controller/jdk/types"
	"go.uber.org/mock/gomock"
)

func TestNew(t *testing.T) {
	ctrl := gomock.NewController(t)
	registryMock := jdkmock.NewMockController(ctrl)
	assert.NotPanics(t, func() {
		New(registryMock, tally.NewTestScope("testing", make(map[string]string, 0)))
	})
}

func TestResolveBreakpoints(t *testing.T) {
	t.Run("resolve breakpoints success", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		jdkmockCtrl := jdkmock.NewMockController(ctrl)
		jdkmockCtrl.EXPECT().ResolveBreakpoints(gomock.Any(), gomock.Any()).Return([]*types.BreakpointLocation{
			&types.BreakpointLocation{Line: 1, Column: 1},
		}, nil)

		testScope := tally.NewTestScope("testing", make(map[string]string, 0))

		h := New(jdkmockCtrl, testScope)
		res, err := h.ResolveBreakpoints(context.Background(), &pb.ResolveBreakpointsRequest{})
		assert.NoError(t, err)
		assert.Equal(t, &pb.ResolveBreakpointsResponse{
			BreakpointLocations: []*modelpb.JavaBreakpointLocation{
				&modelpb.JavaBreakpointLocation{Line: 1, Column: 1},
			},
		}, res)
	})
}

func TestResolveClassToPath(t *testing.T) {
	t.Run("resolve class to path success", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		jdkmockCtrl := jdkmock.NewMockController(ctrl)
		jdkmockCtrl.EXPECT().ResolveClassToPath(gomock.Any(), gomock.Any()).Return("file:///path/to/file", nil)

		testScope := tally.NewTestScope("testing", make(map[string]string, 0))

		h := New(jdkmockCtrl, testScope)
		res, err := h.ResolveClassToPath(context.Background(), &pb.ResolveClassToPathRequest{})
		assert.NoError(t, err)
		assert.Equal(t, &pb.ResolveClassToPathResponse{SourceUri: "file:///path/to/file"}, res)
	})
}
