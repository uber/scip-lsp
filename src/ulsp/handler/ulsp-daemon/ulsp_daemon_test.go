package ulspdaemon

import (
	"context"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	tally "github.com/uber-go/tally/v4"
	"github.com/uber/scip-lsp/idl/mock/jsonrpc2mock"
	pb "github.com/uber/scip-lsp/idl/ulsp/service"
	"github.com/uber/scip-lsp/src/ulsp/controller/ulsp-daemon/ulspdaemonmock"
	"github.com/uber/scip-lsp/src/ulsp/factory"
	"github.com/uber/scip-lsp/src/ulsp/internal/jsonrpcfx/jsonrpcfxmock"
	"github.com/uber/scip-lsp/src/ulsp/mapper"
	"go.lsp.dev/jsonrpc2"
	"go.uber.org/goleak"
	"go.uber.org/mock/gomock"
)

func TestSample(t *testing.T) {
	t.Run("sample success", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		jsonRPCMock := jsonrpcfxmock.NewMockJSONRPCModule(ctrl)
		jsonRPCMock.EXPECT().RegisterConnectionManager(gomock.Any())

		testScope := tally.NewTestScope("testing", make(map[string]string, 0))

		c := ulspdaemonmock.NewMockController(ctrl)
		h := New(c, jsonRPCMock, testScope)

		res, err := h.Sample(context.Background(), &pb.SampleRequest{Name: "You"})
		assert.NoError(t, err, "Unexpected Sample error.")
		assert.Equal(t, res.Name, "Hello You")
	})
}

func TestNewConnection(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	c := ulspdaemonmock.NewMockController(ctrl)
	testScope := tally.NewTestScope("testing", make(map[string]string, 0))

	mgr := jsonRPCConnectionManager{
		stats: testScope,
		ctrl:  c,
	}

	mockConn := jsonrpc2mock.NewMockConn(ctrl)
	var conn jsonrpc2.Conn = mockConn

	t.Run("create success", func(t *testing.T) {
		c.EXPECT().InitSession(gomock.Any(), gomock.Any()).Return(factory.UUID(), nil)
		router, err := mgr.NewConnection(ctx, &conn)
		assert.IsType(t, &jsonRPCRouter{}, router)
		assert.NoError(t, err)
	})

	t.Run("create failure", func(t *testing.T) {
		c.EXPECT().InitSession(gomock.Any(), gomock.Any()).Return(uuid.Nil, errors.New("error"))
		_, err := mgr.NewConnection(ctx, &conn)
		assert.Error(t, err)
	})
}

func TestRemoveConnection(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	c := ulspdaemonmock.NewMockController(ctrl)
	id := factory.UUID()
	c.EXPECT().InitSession(gomock.Any(), gomock.Any()).Return(id, nil)
	c.EXPECT().EndSession(gomock.Any(), gomock.Any()).Do(func(ctx context.Context, id uuid.UUID) error {
		resultID, err := mapper.ContextToSessionUUID(ctx)
		assert.NoError(t, err)
		assert.Equal(t, id, resultID)
		return nil
	})

	testScope := tally.NewTestScope("testing", make(map[string]string, 0))

	mgr := jsonRPCConnectionManager{
		stats: testScope,
		ctrl:  c,
	}

	mockConn := jsonrpc2mock.NewMockConn(ctrl)
	var conn jsonrpc2.Conn = mockConn
	router, err := mgr.NewConnection(ctx, &conn)

	mgr.RemoveConnection(ctx, router.UUID())
	assert.NoError(t, err)
}

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func newMockReplier() jsonrpc2.Replier {
	return func(ctx context.Context, result interface{}, err error) error {
		return err
	}
}
