package ulspdaemon

import (
	"context"

	"github.com/gofrs/uuid"
	tally "github.com/uber-go/tally/v4"
	controller "github.com/uber/scip-lsp/src/ulsp/controller/ulsp-daemon"
	"github.com/uber/scip-lsp/src/ulsp/entity"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
)

// MethodRequestFullShutdown directs the server to shut down on the next JSON-RPC 'exit' method call.
const MethodRequestFullShutdown = "ulsp/requestFullShutdown"

type jsonRPCRouter struct {
	ulspdaemon controller.Controller
	uuid       uuid.UUID
	stats      tally.Scope
}

// HandleReq handles routing for a single request.
func (r *jsonRPCRouter) HandleReq(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	ctx = context.WithValue(ctx, entity.SessionContextKey, r.uuid)

	// Routing to each of the available methods in go.lsp.dev/protocol will occur here.
	// Results are passed back to reply to be returned to the client.
	switch req.Method() {
	// Lifecycle related methods.
	case protocol.MethodInitialize:
		return r.Initialize(ctx, reply, req)

	case protocol.MethodInitialized:
		return r.Initialized(ctx, reply, req)

	case protocol.MethodShutdown:
		return r.Shutdown(ctx, reply, req)

	case protocol.MethodExit:
		return r.Exit(ctx, reply, req)

	case MethodRequestFullShutdown:
		return r.RequestFullShutdown(ctx, reply, req)

	// Document related methods.
	case protocol.MethodTextDocumentDidChange:
		return r.DidChange(ctx, reply, req)

	case protocol.MethodWorkspaceDidChangeWatchedFiles:
		return r.DidChangeWatchedFiles(ctx, reply, req)

	case protocol.MethodTextDocumentDidOpen:
		return r.DidOpen(ctx, reply, req)

	case protocol.MethodTextDocumentDidClose:
		return r.DidClose(ctx, reply, req)

	case protocol.MethodTextDocumentWillSave:
		return r.WillSave(ctx, reply, req)

	case protocol.MethodTextDocumentWillSaveWaitUntil:
		return r.WillSaveWaitUntil(ctx, reply, req)

	case protocol.MethodTextDocumentDidSave:
		return r.DidSave(ctx, reply, req)

	case protocol.MethodWillRenameFiles:
		return r.WillRenameFiles(ctx, reply, req)

	case protocol.MethodDidRenameFiles:
		return r.DidRenameFiles(ctx, reply, req)

	case protocol.MethodWillCreateFiles:
		return r.WillCreateFiles(ctx, reply, req)

	case protocol.MethodDidCreateFiles:
		return r.DidCreateFiles(ctx, reply, req)

	case protocol.MethodWillDeleteFiles:
		return r.WillDeleteFiles(ctx, reply, req)

	case protocol.MethodDidDeleteFiles:
		return r.DidDeleteFiles(ctx, reply, req)

	// Code intel related methods
	case protocol.MethodTextDocumentCodeAction:
		return r.CodeAction(ctx, reply, req)

	case protocol.MethodTextDocumentCodeLens:
		return r.CodeLens(ctx, reply, req)

	case protocol.MethodCodeLensRefresh:
		return r.CodeLensRefresh(ctx, reply, req)

	case protocol.MethodCodeLensResolve:
		return r.CodeLensResolve(ctx, reply, req)

	case protocol.MethodTextDocumentDeclaration:
		return r.GotoDeclaration(ctx, reply, req)

	case protocol.MethodTextDocumentDefinition:
		return r.GotoDefinition(ctx, reply, req)

	case protocol.MethodTextDocumentTypeDefinition:
		return r.GotoTypeDefinition(ctx, reply, req)

	case protocol.MethodTextDocumentImplementation:
		return r.GotoImplementation(ctx, reply, req)

	case protocol.MethodTextDocumentReferences:
		return r.References(ctx, reply, req)

	case protocol.MethodTextDocumentHover:
		return r.Hover(ctx, reply, req)

	case protocol.MethodTextDocumentDocumentSymbol:
		return r.DocumentSymbol(ctx, reply, req)

	// Workspace methods
	case protocol.MethodWorkspaceExecuteCommand:
		return r.ExecuteCommand(ctx, reply, req)

	// Window methods
	case protocol.MethodWorkDoneProgressCancel:
		return r.WorkDoneProgressCancel(ctx, reply, req)

	default:
		return jsonrpc2.MethodNotFoundHandler(ctx, reply, req)
	}
}

func (r *jsonRPCRouter) UUID() uuid.UUID {
	return r.uuid
}
