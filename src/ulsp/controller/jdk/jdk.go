package jdk

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/sourcegraph/scip/bindings/go/scip"
	tally "github.com/uber-go/tally/v4"
	"github.com/uber/scip-lsp/src/scip-lib/model"
	docsync "github.com/uber/scip-lsp/src/ulsp/controller/doc-sync"
	"github.com/uber/scip-lsp/src/ulsp/controller/jdk/types"
	scipctrl "github.com/uber/scip-lsp/src/ulsp/controller/scip"
	"github.com/uber/scip-lsp/src/ulsp/entity"
	ulspplugin "github.com/uber/scip-lsp/src/ulsp/entity/ulsp-plugin"
	ideclient "github.com/uber/scip-lsp/src/ulsp/gateway/ide-client"
	"github.com/uber/scip-lsp/src/ulsp/internal/fs"
	"github.com/uber/scip-lsp/src/ulsp/repository/session"
	"go.lsp.dev/protocol"
	"go.uber.org/config"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

const (
	_nameKey               = "jdk"
	_defaultPackageVersion = "."
)

// Controller defines the interface for a jdk controller.
type Controller interface {
	StartupInfo(ctx context.Context) (ulspplugin.PluginInfo, error)
	ResolveBreakpoints(ctx context.Context, req *types.ResolveBreakpoints) ([]*types.BreakpointLocation, error)
	ResolveClassToPath(ctx context.Context, req *types.ResolveClassToPath) (string, error)
}

// Params are inbound parameters to initialize a new plugin.
type Params struct {
	fx.In

	Sessions   session.Repository
	IdeGateway ideclient.Gateway
	Logger     *zap.SugaredLogger
	Config     config.Provider
	Stats      tally.Scope
	FS         fs.UlspFS

	PluginDocSync docsync.Controller
	PluginScip    scipctrl.Controller
}

type controller struct {
	configs    entity.MonorepoConfigs
	sessions   session.Repository
	ideGateway ideclient.Gateway
	logger     *zap.SugaredLogger
	stats      tally.Scope
	documents  docsync.Controller
	scip       scipctrl.Controller
	fs         fs.UlspFS
}

// New creates a new controller for lint.
func New(p Params) (Controller, error) {
	configs := entity.MonorepoConfigs{}
	if err := p.Config.Get(entity.MonorepoConfigKey).Populate(&configs); err != nil {
		panic(fmt.Sprintf("getting configuration for %q: %v", entity.MonorepoConfigKey, err))
	}
	return &controller{
		configs:    configs,
		sessions:   p.Sessions,
		ideGateway: p.IdeGateway,
		logger:     p.Logger.With("plugin", _nameKey),
		stats:      p.Stats.SubScope("jdk_ctrl"),
		documents:  p.PluginDocSync,
		scip:       p.PluginScip,
		fs:         fs.New(),
	}, nil
}

// StartupInfo returns PluginInfo for this controller.
func (c *controller) StartupInfo(ctx context.Context) (ulspplugin.PluginInfo, error) {
	// Set a priority for each method that this module provides.
	priorities := map[string]ulspplugin.Priority{
		protocol.MethodExit: ulspplugin.PriorityAsync,
	}

	// Assign method keys to implementations.
	methods := &ulspplugin.Methods{
		PluginNameKey: _nameKey,
		Exit:          c.exit,
	}

	return ulspplugin.PluginInfo{
		Priorities: priorities,
		Methods:    methods,
		NameKey:    _nameKey,
	}, nil
}

// ResolveBreakpoints resolves a list of breakpoints in a file to its class
func (c *controller) ResolveBreakpoints(ctx context.Context, req *types.ResolveBreakpoints) ([]*types.BreakpointLocation, error) {
	symbols, err := c.scip.GetDocumentSymbols(ctx, req.WorkspaceRoot, req.SourceURI)
	if err != nil {
		return nil, err
	}
	res := make([]*types.BreakpointLocation, 0, len(req.Breakpoints))
	if len(symbols) == 0 {
		c.logger.Warnf("path not found in scip %q", req.SourceURI)
		return res, nil
	}
	defs := c.getClassDefinitions(ctx, symbols)
	if len(defs) == 0 {
		c.logger.Debugf("no class definitions found in file %q (found %d symbol occurrences)", req.SourceURI, len(symbols))
		return res, nil
	}
	if len(defs) == 1 {
		c.logger.Warnf("found one class definition in file %q: %+v", req.SourceURI, defs[0])
		// Only one class in the file, return the breakpoints
		for _, rbp := range req.Breakpoints {
			res = append(res, &types.BreakpointLocation{
				Line:      rbp.Line,
				Column:    rbp.Character,
				ClassName: getFullClassName(defs[0].Symbol),
			})
		}
		return res, nil
	}

	for _, rbp := range req.Breakpoints {
		// Find the enclosing class for the line
		className, err := c.findEnclosingClass(ctx, defs, rbp)
		if err != nil {
			c.logger.Warnf("failed to find enclosing class for line %d: %v", rbp.Line, err)
			continue
		}
		res = append(res, &types.BreakpointLocation{
			Line:      rbp.Line,
			Column:    rbp.Character,
			ClassName: className,
		})
	}
	return res, nil
}

// ResolveClassToPath resolves a JDK class to a file on disk
func (c *controller) ResolveClassToPath(ctx context.Context, req *types.ResolveClassToPath) (string, error) {
	descriptors, err := getDescriptors(req.FullyQualifiedName)
	if err != nil {
		return "", err
	}

	s, err := c.scip.GetSymbolDefinitionOccurrence(ctx, req.WorkspaceRoot, descriptors, _defaultPackageVersion)
	if err != nil {
		return "", err
	}

	return string(s.Location), nil
}

// exit handler to satisfy the StartupInfo validation
func (c *controller) exit(ctx context.Context) error {
	return nil
}

func (c *controller) getClassDefinitions(ctx context.Context, symbols []*model.SymbolOccurrence) []*model.SymbolInformation {
	defs := make([]*model.SymbolInformation, 0)
	for _, sym := range symbols {
		info := sym.Info
		if info == nil {
			continue
		}
		if info.Kind == scip.SymbolInformation_Class {
			defs = append(defs, info)
		}
	}
	// TODO: once IDE-1353 implemented this can be removed
	slices.SortFunc[[]*model.SymbolInformation](defs, func(a, b *model.SymbolInformation) int {
		return strings.Compare(a.Symbol, b.Symbol)
	})
	return defs
}

func (c *controller) findEnclosingClass(ctx context.Context, defs []*model.SymbolInformation, rbp *protocol.Position) (string, error) {
	// TODO: IDE-1353 implement enclosing class
	// 1. query the tree for any classes
	// 2. match the proper inner class
	// 3. match the position to the item in defs

	return getFullClassName(defs[0].Symbol), nil
}

// getPackageAndClass splits a fully qualified class name into package and class name
func getPackageAndClass(fqn string) (string, string) {
	parts := strings.Split(fqn, ".")
	if len(parts) == 1 {
		return "", parts[0]
	}
	return strings.Join(parts[:len(parts)-1], "."), parts[len(parts)-1]
}

func getDescriptors(fqn string) ([]model.Descriptor, error) {
	pkgID, class := getPackageAndClass(fqn)
	if len(pkgID) == 0 || len(class) == 0 {
		return nil, fmt.Errorf("invalid class name %q", fqn)
	}

	descriptors := make([]model.Descriptor, 0)
	for _, segment := range strings.Split(pkgID, ".") {
		descriptors = append(descriptors, model.Descriptor{
			Suffix: scip.Descriptor_Namespace,
			Name:   segment,
		})
	}

	descriptors = append(descriptors, model.Descriptor{
		Suffix: scip.Descriptor_Type,
		Name:   strings.Split(class, "$")[0],
	})
	return descriptors, nil
}

// getFullClassName converts from scip symbol to Java class identifier. eg:
// descriptor: com/example/service/MyClass# -> com.example.service.MyClass
// descriptor: com/example/service/MyClass#InnerClass# -> com.example.service.MyClass$InnerClass
func getFullClassName(symbol string) string {
	symb, err := scip.ParseSymbol(symbol)
	if err != nil {
		return ""
	}
	namespace := make([]string, 0)
	className := ""
	for _, d := range symb.Descriptors {
		if d.Suffix == scip.Descriptor_Namespace {
			namespace = append(namespace, d.Name)
		} else if d.Suffix == scip.Descriptor_Type {
			if len(className) == 0 {
				className = d.Name
			} else {
				className += "$" + d.Name
			}
		}
	}

	return strings.Join(namespace, ".") + "." + className
}
