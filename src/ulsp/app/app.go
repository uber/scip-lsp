package app

import (
	"context"
	"time"

	tally "github.com/uber-go/tally/v4"
	"github.com/uber/scip-lsp/src/ulsp/gateway"
	notifier "github.com/uber/scip-lsp/src/ulsp/gateway/ide-client"
	"github.com/uber/scip-lsp/src/ulsp/handler"
	"github.com/uber/scip-lsp/src/ulsp/internal/core"
	"github.com/uber/scip-lsp/src/ulsp/internal/executor"
	"github.com/uber/scip-lsp/src/ulsp/internal/fs"
	"github.com/uber/scip-lsp/src/ulsp/internal/jsonrpcfx"
	"github.com/uber/scip-lsp/src/ulsp/internal/serverinfofile"
	workspaceutils "github.com/uber/scip-lsp/src/ulsp/internal/workspace-utils"
	"go.uber.org/fx"
)

// Module defines the ulsp-daemon application module.
var Module = fx.Options(
	gateway.Module, // outbounds
	handler.Module, // inbounds
	jsonrpcfx.Module,
	fs.Module,
	executor.Module,
	serverinfofile.Module,
	workspaceutils.Module,
	core.ConfigModule,
	core.LoggerModule,
	fx.Provide(notifier.New),
	fx.Provide(func(lc fx.Lifecycle) tally.Scope {
		rs, closer := tally.NewRootScope(tally.ScopeOptions{
			Tags: map[string]string{
				"service": "ulsp-daemon",
			},
		}, 1*time.Second)

		lc.Append(fx.Hook{
			OnStop: func(ctx context.Context) error {
				return closer.Close()
			},
		})

		return rs
	}),
	fx.Decorate(decorateEnvContext),
	fx.Decorate(decorateConfigProvider),
	fx.Provide(func() Context {
		return Context{
			Environment:        "local",
			RuntimeEnvironment: "local",
		}
	}),
)
