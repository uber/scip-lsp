package main

import (
	"github.com/uber/scip-lsp/src/ulsp/app"
	"github.com/uber/scip-lsp/src/ulsp/generated"
	"go.uber.org/fx"
)

const _version = "(to be added by Bazel)"

func opts() fx.Option {
	return fx.Options(
		app.Module,
		generated.GeneratedModule(),
	)
}

func main() {
	// New to Fx? Brush up at t.uber.com/fx and https://uber-go.github.io/fx/.
	fx.New(opts()).Run()
}
