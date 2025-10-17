// Package ulspdaemon implements the ulsp-daemon service's gRPC handlers.
package ulspdaemon

import (
	"context"
	"fmt"

	"github.com/gofrs/uuid"
	tally "github.com/uber-go/tally/v4"
	pb "github.com/uber/scip-lsp/idl/ulsp/service"
	controller "github.com/uber/scip-lsp/src/ulsp/controller/ulsp-daemon"
	"github.com/uber/scip-lsp/src/ulsp/entity"
	"github.com/uber/scip-lsp/src/ulsp/internal/jsonrpcfx"
	"go.lsp.dev/jsonrpc2"
)

// Handler represents the ulsp-daemon service's gRPC API.
type Handler = pb.UlspDaemonServer

type handler struct {
	ulspdaemon        controller.Controller
	connectionManager jsonrpcfx.ConnectionManager
	stats             tally.Scope
}

// New constructs a new ulsp-daemon Handler.
func New(ctrl controller.Controller, jsonrpcmod jsonrpcfx.JSONRPCModule, stats tally.Scope) Handler {
	c := jsonRPCConnectionManager{
		ctrl:  ctrl,
		stats: stats.SubScope("json_rpc"),
	}
	jsonrpcmod.RegisterConnectionManager(&c)

	return &handler{
		ulspdaemon:        ctrl,
		connectionManager: &c,
		stats:             stats,
	}
}

// Create synchronously creates and saves a new UlspDaemon.
func (h *handler) Sample(ctx context.Context, r *pb.SampleRequest) (*pb.SampleResponse, error) {
	return &pb.SampleResponse{
		Name: "Hello " + r.Name,
	}, nil
}

type jsonRPCConnectionManager struct {
	ctrl  controller.Controller
	stats tally.Scope
}

// NewConnection will store a new connection and return a router that includes its UUID.
func (c *jsonRPCConnectionManager) NewConnection(ctx context.Context, conn *jsonrpc2.Conn) (router jsonrpcfx.Router, err error) {
	id, err := c.ctrl.InitSession(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("error while creating new connection: %w", err)
	}

	r := jsonRPCRouter{
		ulspdaemon: c.ctrl,
		uuid:       id,
		stats:      c.stats,
	}

	return &r, nil
}

// RemoveConnection cleans up a closed connection.
func (c *jsonRPCConnectionManager) RemoveConnection(ctx context.Context, id uuid.UUID) {
	// Ensure session is removed even if no Exit call has been received.
	ctx = context.WithValue(ctx, entity.SessionContextKey, id)
	c.ctrl.EndSession(ctx, id)
}
