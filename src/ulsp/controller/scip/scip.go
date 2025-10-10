package scip

import (
	"context"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	tally "github.com/uber-go/tally/v4"
	"github.com/uber/scip-lsp/src/scip-lib/mapper"
	"github.com/uber/scip-lsp/src/scip-lib/model"
	diagnostics "github.com/uber/scip-lsp/src/ulsp/controller/diagnostics"
	docsync "github.com/uber/scip-lsp/src/ulsp/controller/doc-sync"
	"github.com/uber/scip-lsp/src/ulsp/entity"
	ulspplugin "github.com/uber/scip-lsp/src/ulsp/entity/ulsp-plugin"
	ideclient "github.com/uber/scip-lsp/src/ulsp/gateway/ide-client"
	"github.com/uber/scip-lsp/src/ulsp/internal/fs"
	notifier "github.com/uber/scip-lsp/src/ulsp/internal/persistent-notifier"
	ulsp_mapper "github.com/uber/scip-lsp/src/ulsp/mapper"
	"github.com/uber/scip-lsp/src/ulsp/repository/session"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
	"go.uber.org/config"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

const (
	_nameKey          = "scip"
	_scipExt          = ".scip"
	_shaExt           = ".sha256"
	_noHash           = "nohash"
	_indexLoadTimeout = 30 * time.Second
	_debounceTimeout  = 10 * time.Millisecond
)

// Controller defines the interface for a scip controller.
type Controller interface {
	StartupInfo(ctx context.Context) (ulspplugin.PluginInfo, error)
	GetDocumentSymbols(ctx context.Context, workspaceRoot string, uri string) ([]*model.SymbolOccurrence, error)
	GetSymbolDefinitionOccurrence(ctx context.Context, workspaceRoot string, descriptors []model.Descriptor, version string) (*model.SymbolOccurrence, error)
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

	PluginDocSync     docsync.Controller
	PluginDiagnostics diagnostics.Controller
}

type controller struct {
	configs         entity.MonorepoConfigs
	sessions        session.Repository
	ideGateway      ideclient.Gateway
	logger          *zap.SugaredLogger
	stats           tally.Scope
	documents       docsync.Controller
	diagnostics     diagnostics.Controller
	registries      map[string]Registry
	registriesMu    sync.Mutex
	watcher         *fsnotify.Watcher
	once            sync.Once
	fs              fs.UlspFS
	initialLoad     chan bool
	watchCloser     chan bool
	loadedIndices   map[string]string
	newScipRegistry func(workspaceRoot, indexFolder string) Registry
	debounceTimers  map[string]*time.Timer
	debounceMu      sync.Mutex
	indexNotifier   *IndexNotifier
}

// New creates a new controller for lint.
func New(p Params) (Controller, error) {
	configs := entity.MonorepoConfigs{}
	if err := p.Config.Get(entity.MonorepoConfigKey).Populate(&configs); err != nil {
		panic(fmt.Sprintf("getting configuration for %q: %v", entity.MonorepoConfigKey, err))
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create fs watcher for scip: %w", err)
	}

	notificationManagerParams := notifier.NotificationManagerParams{
		Sessions:   p.Sessions,
		IdeGateway: p.IdeGateway,
		Logger:     p.Logger,
	}

	return &controller{
		configs:        configs,
		sessions:       p.Sessions,
		ideGateway:     p.IdeGateway,
		logger:         p.Logger.With("plugin", _nameKey),
		stats:          p.Stats.SubScope("scip"),
		documents:      p.PluginDocSync,
		diagnostics:    p.PluginDiagnostics,
		registries:     make(map[string]Registry),
		watcher:        watcher,
		fs:             p.FS,
		initialLoad:    make(chan bool, 1),
		watchCloser:    make(chan bool, 1),
		loadedIndices:  make(map[string]string),
		debounceTimers: make(map[string]*time.Timer),
		indexNotifier:  NewIndexNotifier(notifier.NewNotificationManager(notificationManagerParams)),
		newScipRegistry: func(workspaceRoot, indexFolder string) Registry {
			p.Logger.Infof("Creating new SCIP registry for %q, index folder %q", workspaceRoot, indexFolder)
			return NewPartialScipRegistry(workspaceRoot, indexFolder, p.Logger.Named("fast-loader"))
		},
	}, nil
}

func (c *controller) createNewScipRegistry(workspaceRoot string, monorepo entity.MonorepoName) Registry {
	indexFolder := path.Join(workspaceRoot, ".scip")
	if len(c.configs[monorepo].Scip.Directories) > 0 {
		indexFolder = path.Join(workspaceRoot, c.configs["_default"].Scip.Directories[0])
	}
	if len(c.configs[monorepo].Scip.Directories) > 0 {
		indexFolder = path.Join(workspaceRoot, c.configs[monorepo].Scip.Directories[0])
	}
	reg := c.newScipRegistry(workspaceRoot, indexFolder)

	reg.SetDocumentLoadedCallback(func(doc *model.Document) {
		docURI := reg.GetURI(doc.RelativePath)
		c.diagnostics.ApplyDiagnostics(context.Background(), workspaceRoot, docURI, doc.Diagnostics)
		c.documents.ResetBase(context.Background(), workspaceRoot, protocol.TextDocumentIdentifier{URI: docURI})
	})

	return reg
}

// StartupInfo returns PluginInfo for this controller.
func (c *controller) StartupInfo(ctx context.Context) (ulspplugin.PluginInfo, error) {
	// Set a priority for each method that this module provides.
	priorities := map[string]ulspplugin.Priority{
		protocol.MethodInitialize:            ulspplugin.PriorityRegular,
		protocol.MethodInitialized:           ulspplugin.PriorityAsync,
		protocol.MethodTextDocumentDidOpen:   ulspplugin.PriorityRegular,
		protocol.MethodTextDocumentDidChange: ulspplugin.PriorityRegular,

		protocol.MethodTextDocumentDefinition:     ulspplugin.PriorityHigh,
		protocol.MethodTextDocumentDeclaration:    ulspplugin.PriorityHigh,
		protocol.MethodTextDocumentTypeDefinition: ulspplugin.PriorityHigh,
		protocol.MethodTextDocumentImplementation: ulspplugin.PriorityHigh,
		protocol.MethodTextDocumentReferences:     ulspplugin.PriorityHigh,
		protocol.MethodTextDocumentHover:          ulspplugin.PriorityHigh,
		protocol.MethodTextDocumentDocumentSymbol: ulspplugin.PriorityHigh,
	}

	// Assign method keys to implementations.
	methods := &ulspplugin.Methods{
		PluginNameKey: _nameKey,

		Initialize:         c.initialize,
		Initialized:        c.initialized,
		DidChange:          c.didChange,
		DidOpen:            c.didOpen,
		GotoDeclaration:    c.gotoDeclaration,
		GotoDefinition:     c.gotoDefinition,
		GotoTypeDefinition: c.gotoTypeDefinition,
		GotoImplementation: c.gotoImplementation,
		References:         c.references,
		Hover:              c.hover,
		DocumentSymbol:     c.documentSymbol,
	}

	return ulspplugin.PluginInfo{
		Priorities: priorities,
		Methods:    methods,
		NameKey:    _nameKey,
	}, nil
}

// GetDocumentSymbols returns the document symbols for a given document
func (c *controller) GetDocumentSymbols(ctx context.Context, workspaceRoot string, uriStr string) ([]*model.SymbolOccurrence, error) {
	if workspaceRoot == "" {
		s, err := c.sessions.GetFromContext(ctx)
		if err != nil {
			return nil, err
		}
		workspaceRoot = s.WorkspaceRoot
	}

	reg := c.registries[workspaceRoot]
	if reg == nil {
		return nil, nil
	}

	return reg.DocumentSymbols(uri.URI(uriStr))
}

// GetSymbolDefinitionOccurrence uses the descriptors and version to get the definition occurrence for a given symbol
func (c *controller) GetSymbolDefinitionOccurrence(ctx context.Context, workspaceRoot string, descriptors []model.Descriptor, version string) (*model.SymbolOccurrence, error) {
	if workspaceRoot == "" {
		s, err := c.sessions.GetFromContext(ctx)
		if err != nil {
			return nil, err
		}
		workspaceRoot = s.WorkspaceRoot
	}

	reg := c.registries[workspaceRoot]
	if reg == nil {
		return nil, nil
	}

	return reg.GetSymbolDefinitionOccurrence(descriptors, version)
}

func (c *controller) loadFromDirectories(ctx context.Context, dirs []string) ([]string, error) {
	failed := make([]string, 0)
	s, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return failed, err
	}

	c.registriesMu.Lock()
	reg := c.registries[s.WorkspaceRoot]
	if reg == nil {
		c.logger.Infof("Initializing registry for %q", s.WorkspaceRoot)
		reg = c.createNewScipRegistry(s.WorkspaceRoot, s.Monorepo)
		c.registries[s.WorkspaceRoot] = reg
	}
	c.registriesMu.Unlock()

	for _, d := range dirs {
		fDir := path.Join(s.WorkspaceRoot, d)
		scips, err := c.fs.ReadDir(fDir)
		if err != nil {
			c.logger.Infof("%q is not a valid path for indices", d)
			continue
		}
		var (
			wg sync.WaitGroup
			mu sync.Mutex
		)

		scipFiles := make([]struct {
			filePath  string
			loadedSha string
		}, 0)
		for _, scip := range scips {
			if !strings.HasSuffix(scip.Name(), _scipExt) {
				continue
			}
			fPath := path.Join(fDir, scip.Name())
			scipFiles = append(scipFiles, struct {
				filePath  string
				loadedSha string
			}{
				filePath:  fPath,
				loadedSha: c.loadedIndices[fPath],
			})
			c.indexNotifier.TrackFile(context.Background(), s.WorkspaceRoot, fPath)
		}
		// Limit load concurrency to max supported for indexer
		sem := make(chan struct{}, reg.LoadConcurrency())

		for _, fileData := range scipFiles {
			wg.Add(1)
			sem <- struct{}{} // Acquire semaphore
			go func(fPath string, loadedSha string) {
				defer wg.Done()
				defer func() { <-sem }() // Release semaphore

				c.indexNotifier.NotifyStart(context.Background(), s.WorkspaceRoot, fPath)
				sha, err := c.loadScipFile(s.WorkspaceRoot, fPath, loadedSha)
				mu.Lock()
				var status IndexLoadStatus

				if err != nil {
					failed = append(failed, fPath)
					status = IndexLoadError
				} else {
					c.loadedIndices[fPath] = sha
					status = IndexLoadSuccess
				}
				mu.Unlock()
				c.indexNotifier.NotifyComplete(context.Background(), s.WorkspaceRoot, fPath, status)
			}(fileData.filePath, fileData.loadedSha)
		}

		wg.Wait()

		if c.watcher != nil {
			err = c.watcher.Add(fDir)
			if err != nil {
				c.logger.Warnf("Failed to watch for changes in %d: %v", d, err)
			}
		}
	}

	if len(failed) > 0 && c.ideGateway != nil {
		c.ideGateway.ShowMessage(ctx, &protocol.ShowMessageParams{
			Message: fmt.Sprintf("Failed to load %d indices: %v", len(failed), failed),
			Type:    protocol.MessageTypeInfo,
		})
		c.logger.Warnf("Failed to load %d indices", len(failed))
	}

	return failed, nil
}

func (c *controller) loadScipFile(workspaceRoot string, scipPath string, loadedSha string) (string, error) {
	reg := c.registries[workspaceRoot]

	curSha, _ := c.getSha(scipPath)

	if curSha == loadedSha {
		c.logger.Infof("Skipping loading SCIP index %q, already loaded", scipPath)
		return curSha, nil
	}

	c.logger.Infof("Loading SCIP index %q", scipPath)
	defer c.logger.Infof("Finished loading SCIP index %q", scipPath)

	// TODO(IDE-678): add statistics for scip loading performance

	return curSha, reg.LoadIndexFile(scipPath)
}

func (c *controller) initialize(ctx context.Context, params *protocol.InitializeParams, result *protocol.InitializeResult) error {
	ulsp_mapper.InitializeResultEnsureDefinitionProvider(result, false)
	ulsp_mapper.InitializeResultEnsureDeclarationProvider(result, false)
	ulsp_mapper.InitializeResultEnsureImplementationProvider(result, false)
	ulsp_mapper.InitializeResultEnsureReferencesProvider(result, false)
	ulsp_mapper.InitializeResultEnsureTypeDefinitionProvider(result, false)
	ulsp_mapper.InitializeResultEnsureHoverProvider(result, false)
	ulsp_mapper.InitializeResultEnsureDocumentSymbolProvider(result, false)

	return nil
}

func (c *controller) initialized(ctx context.Context, params *protocol.InitializedParams) error {
	sesh, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return err
	}
	c.registriesMu.Lock()
	_, hasIndex := c.registries[sesh.WorkspaceRoot]
	c.registriesMu.Unlock()

	c.once.Do(func() {
		go c.handleChanges(c.watchCloser)
	})

	if c.watcher != nil {
		if _, ok := c.configs[sesh.Monorepo]; !ok {
			return nil
		}
		scipDirs := c.configs[sesh.Monorepo].Scip.Directories
		for _, d := range scipDirs {
			scipDirPath := path.Join(sesh.WorkspaceRoot, d)
			err = c.fs.MkdirAll(scipDirPath)
			if err != nil {
				c.logger.Warnf("Failed to create directory %q: %v", scipDirPath, err)
				return err
			}
			err = c.watcher.Add(scipDirPath)
			if err != nil {
				c.logger.Warnf("Failed to watch for changes in %d: %v", sesh.WorkspaceRoot, err)
				return err
			}
		}
	}

	if !hasIndex {
		return c.loadIndices(ctx, sesh)
	}
	return nil
}

func (c *controller) loadIndices(ctx context.Context, session *entity.Session) error {
	mCfg, ok := c.configs[session.Monorepo]
	defaultCfg, defOk := c.configs["_default"]
	if !ok && defOk {
		mCfg = defaultCfg
		c.logger.Infof("No SCIP configuration found for repo, using default %q", session.Monorepo)
	} else if !ok {
		c.logger.Infof("No SCIP configuration found for repo, skipping SCIP loading")
		return nil
	}

	if mCfg.Scip.LoadFromDirectories {
		_, err := c.loadFromDirectories(ctx, mCfg.Scip.Directories)
		if err != nil {
			return err
		}
	}

	return nil
}

// handleDebounce manages debouncing of file events
func (c *controller) handleDebounce(event fsnotify.Event) {
	if !strings.HasSuffix(event.Name, _scipExt) {
		return
	}

	c.debounceMu.Lock()
	defer c.debounceMu.Unlock()

	for ws := range c.registries {
		if strings.HasPrefix(event.Name, ws) {
			c.indexNotifier.TrackFile(context.Background(), ws, event.Name)
			break
		}
	}
	// Cancel existing timer if any
	if timer, exists := c.debounceTimers[event.Name]; exists {
		timer.Stop()
	}

	c.debounceTimers[event.Name] = time.AfterFunc(_debounceTimeout, func() {
		c.debounceMu.Lock()
		delete(c.debounceTimers, event.Name)
		c.debounceMu.Unlock()

		if err := c.reloadIndex(event); err != nil {
			c.logger.Warnf("Failed to reload index: %v", err)
			if c.ideGateway != nil {
				c.ideGateway.ShowMessage(context.Background(), &protocol.ShowMessageParams{
					Message: fmt.Sprintf("Failed to reload index %q: %v", event.Name, err),
					Type:    protocol.MessageTypeError,
				})
			}
		}
	})
}

func (c *controller) handleChanges(closer chan bool) {
	if c.watcher == nil {
		c.logger.Warn("File watcher unavailable, continuing without watching for changes")
		return
	}
	for {
		select {
		case event := <-c.watcher.Events:
			if !event.Has(fsnotify.Create) && !event.Has(fsnotify.Write) {
				continue
			}
			c.handleDebounce(event)

		case err := <-c.watcher.Errors:
			c.logger.Warnf("Failure in index change watcher: %v", err)
		case <-closer:
			// Cancel any pending debounce timers
			c.debounceMu.Lock()
			for _, timer := range c.debounceTimers {
				timer.Stop()
			}
			c.debounceTimers = make(map[string]*time.Timer)
			c.debounceMu.Unlock()

			err := c.watcher.Close()
			if err != nil {
				c.logger.Warnf("Failed to close index change watcher: %v", err)
			}
			return
		}
	}
}

func (c *controller) reloadIndex(event fsnotify.Event) error {
	c.registriesMu.Lock()
	defer c.registriesMu.Unlock()

	for ws := range c.registries {
		if !strings.HasPrefix(event.Name, ws) ||
			!strings.HasSuffix(event.Name, _scipExt) {
			continue
		}
		ctx := context.Background()
		c.indexNotifier.NotifyStart(ctx, ws, event.Name)

		status := IndexLoadSuccess
		sha, err := c.loadScipFile(ws, event.Name, c.loadedIndices[event.Name])
		if err == nil {
			c.loadedIndices[event.Name] = sha
		} else {
			status = IndexLoadError
		}
		c.indexNotifier.NotifyComplete(ctx, ws, event.Name, status)
		return err
	}
	return nil
}

func (c *controller) didOpen(ctx context.Context, params *protocol.DidOpenTextDocumentParams) error {
	sesh, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return err
	}
	reg := c.registries[sesh.WorkspaceRoot]
	if reg == nil {
		return nil
	}

	reg.DidOpen(params.TextDocument.URI, params.TextDocument.Text)

	return nil
}

func (c *controller) didChange(ctx context.Context, params *protocol.DidChangeTextDocumentParams) error {
	// TODO: update tree sitter attached nodes for file in index
	// https://t3.uberinternal.com/browse/IDE-641
	return nil
}

func (c *controller) gotoDeclaration(ctx context.Context, params *protocol.DeclarationParams, result *[]protocol.LocationLink) error {
	// TODO: Implement code navigation
	// https://t3.uberinternal.com/browse/IDE-642
	return nil
}

func (c *controller) gotoDefinition(ctx context.Context, params *protocol.DefinitionParams, result *[]protocol.LocationLink) error {
	sesh, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return err
	}
	reg := c.registries[sesh.WorkspaceRoot]
	if reg == nil {
		return nil
	}

	mappedPosition, err := c.getBasePosition(ctx, params.TextDocument, params.Position)
	if err != nil {
		return err
	} else if mappedPosition == nil {
		return nil
	}

	originOcc, definitionOcc, err := reg.Definition(params.TextDocument.URI, *mappedPosition)
	if err != nil {
		return fmt.Errorf("failed to get definition: %w", err)
	}
	if definitionOcc != nil {
		l := protocol.LocationLink{
			TargetURI: definitionOcc.Location,
		}

		if definitionOcc.Occurrence != nil {
			rng := c.getLatestRange(ctx, protocol.TextDocumentIdentifier{URI: definitionOcc.Location}, mapper.ScipToProtocolRange(definitionOcc.Occurrence.Range))
			l.TargetRange = rng
			l.TargetSelectionRange = rng
		}

		if originOcc != nil {
			rng := c.getLatestRange(ctx, protocol.TextDocumentIdentifier{URI: params.TextDocument.URI}, mapper.ScipToProtocolRange(originOcc.Occurrence.Range))
			l.OriginSelectionRange = &rng
		}
		*result = append(*result, l)
	}

	return nil
}

func (c *controller) documentSymbol(ctx context.Context, params *protocol.DocumentSymbolParams, result *[]protocol.DocumentSymbol) error {
	sesh, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return err
	}
	reg := c.registries[sesh.WorkspaceRoot]
	if reg == nil {
		return nil
	}

	symbols, err := reg.DocumentSymbols(params.TextDocument.URI)
	if err != nil {
		return fmt.Errorf("failed to get document symbol info: %w", err)
	}

	for _, sym := range symbols {
		protocolSymbol := mapper.ScipSymbolInformationToDocumentSymbol(sym.Info, sym.Occurrence)
		protocolSymbol.Range = c.getLatestRange(ctx, params.TextDocument, protocolSymbol.Range)
		protocolSymbol.SelectionRange = c.getLatestRange(ctx, params.TextDocument, protocolSymbol.SelectionRange)
		*result = append(*result, *protocolSymbol)
	}

	return nil
}

func (c *controller) hover(ctx context.Context, params *protocol.HoverParams, result *protocol.Hover) error {
	sesh, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return err
	}
	reg := c.registries[sesh.WorkspaceRoot]
	if reg == nil {
		return nil
	}

	mappedPosition, err := c.getBasePosition(ctx, params.TextDocument, params.Position)
	if err != nil {
		return err
	} else if mappedPosition == nil {
		return nil
	}

	docs, occ, err := reg.Hover(params.TextDocument.URI, *mappedPosition)
	if err != nil {
		return fmt.Errorf("failed to get hover info: %w", err)
	}

	if len(docs) > 0 {
		rng := mapper.ScipToProtocolRange(occ.Range)
		if result == nil {
			*result = protocol.Hover{}
		}
		if result.Range == nil {
			mappedRange := c.getLatestRange(ctx, params.TextDocument, rng)
			result.Range = &mappedRange
		}
		if result.Contents.Value != "" {
			result.Contents.Value += "\n"
		}
		result.Contents.Value += docs
		result.Contents.Kind = protocol.Markdown
	}

	return nil
}

func (c *controller) gotoTypeDefinition(ctx context.Context, params *protocol.TypeDefinitionParams, result *[]protocol.LocationLink) error {
	// TODO: Implement code navigation
	// https://t3.uberinternal.com/browse/IDE-642
	return nil
}

func (c *controller) gotoImplementation(ctx context.Context, params *protocol.ImplementationParams, result *[]protocol.LocationLink) error {
	// TODO: Implement code navigation
	// https://t3.uberinternal.com/browse/IDE-642
	return nil
}

func appendPackageRefs(pkgRefs []*FileOccurences, result *[]protocol.Location, filter func(occ *model.Occurrence) bool) {
	for _, ref := range pkgRefs {
		for _, occ := range ref.Occurrences {
			if filter != nil && !filter(occ) {
				continue
			}
			*result = append(*result, *mapper.ScipOccurrenceToLocation(ref.file, occ))
		}
	}
}

func (c *controller) references(ctx context.Context, params *protocol.ReferenceParams, result *[]protocol.Location) error {
	sesh, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return err
	}
	reg := c.registries[sesh.WorkspaceRoot]
	if reg == nil {
		return nil
	}

	mappedPosition, err := c.getBasePosition(ctx, params.TextDocument, params.Position)
	if err != nil {
		return err
	} else if mappedPosition == nil {
		return nil
	}

	baseLocations, err := reg.References(params.TextDocument.URI, *mappedPosition)
	if err != nil {
		return fmt.Errorf("failed to get references: %w", err)
	}

	// Convert each location in the base to its latest location
	for _, ref := range baseLocations {
		r := c.getLatestRange(ctx, protocol.TextDocumentIdentifier{URI: ref.URI}, ref.Range)
		*result = append(*result, protocol.Location{
			URI:   ref.URI,
			Range: r,
		})
	}

	return nil
}

func (c *controller) getSha(scipFile string) (string, error) {
	shaFile := scipFile + _shaExt
	sha, err := c.fs.ReadFile(shaFile)
	if err != nil {
		c.logger.Infof("Failed to read sha file %q: %v", shaFile, err)
		return _noHash, err
	}
	return string(sha), nil
}

// getBasePosition maps a position to its base version.
// If no position mapper exists (e.g. document not found), returns the original position.
func (c *controller) getBasePosition(ctx context.Context, doc protocol.TextDocumentIdentifier, pos protocol.Position) (*protocol.Position, error) {
	mapper, err := c.documents.GetPositionMapper(ctx, doc)
	if err != nil {
		return nil, fmt.Errorf("getting position mapper: %w", err)
	}

	// Documents not open/being tracked use the original position.
	if mapper == nil {
		return nil, nil
	}

	// Map position using the available mapper
	basePos, isNew, err := mapper.MapCurrentPositionToBase(pos)
	if err != nil {
		return nil, fmt.Errorf("mapping position to base: %w", err)
	} else if isNew {
		return nil, nil
	}

	return &basePos, nil
}

// getLatestPosition maps a base position to its current location in the document.
// If no position mapper exists (e.g. document not open), returns the original position.
func (c *controller) getLatestPosition(ctx context.Context, doc protocol.TextDocumentIdentifier, pos protocol.Position) (protocol.Position, error) {
	mapper, err := c.documents.GetPositionMapper(ctx, doc)
	if err != nil {
		return protocol.Position{}, fmt.Errorf("getting position mapper: %w", err)
	}

	// No mapper available (e.g. document not open) - use original position
	if mapper == nil {
		return pos, nil
	}

	// Map position using the available mapper
	latestPos, err := mapper.MapBasePositionToCurrent(pos)
	if err != nil {
		return protocol.Position{}, fmt.Errorf("mapping position to current: %w", err)
	}

	return latestPos, nil
}

// getLatestRange returns the latest range for a given range.
// If the new range cannot be computed, returns the original range.
func (c *controller) getLatestRange(ctx context.Context, doc protocol.TextDocumentIdentifier, rng protocol.Range) protocol.Range {
	latestStart, err := c.getLatestPosition(ctx, doc, rng.Start)
	if err != nil {
		c.logger.Errorf("failed to get range start: %s", err)
		return rng
	}

	latestEnd, err := c.getLatestPosition(ctx, doc, rng.End)
	if err != nil {
		c.logger.Errorf("failed to get range end: %s", err)
		return rng
	}

	return protocol.Range{Start: latestStart, End: latestEnd}
}
