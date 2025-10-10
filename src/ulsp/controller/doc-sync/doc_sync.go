package docsync

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sync"

	"github.com/gofrs/uuid"
	tally "github.com/uber-go/tally/v4"
	ulspplugin "github.com/uber/scip-lsp/src/ulsp/entity/ulsp-plugin"
	ideclient "github.com/uber/scip-lsp/src/ulsp/gateway/ide-client"
	ulsperrors "github.com/uber/scip-lsp/src/ulsp/internal/errors"
	"github.com/uber/scip-lsp/src/ulsp/internal/fs"
	"github.com/uber/scip-lsp/src/ulsp/mapper"
	"github.com/uber/scip-lsp/src/ulsp/repository/session"
	"go.lsp.dev/protocol"
	"go.uber.org/config"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

const (
	_nameKey        = "doc-sync"
	_maxFileSizeKey = "maxFileSizeBytes"
)

// DocumentState keeps track of the current state of a document.
type DocumentState int

const (
	// DocumentStateOpenClean indicates that the document is open and has no modifications in the editor.
	DocumentStateOpenClean DocumentState = iota
	// DocumentStateOpenDirty indicates that the document is open and has unsaved modifications in the editor.
	DocumentStateOpenDirty
	// DocumentStateClosed indicates that the document is closed.
	DocumentStateClosed
)

// Controller defines the interface for a document sync controller.
type Controller interface {
	StartupInfo(ctx context.Context) (ulspplugin.PluginInfo, error)

	// Returns the current version of the text document as of the last received DidChange event.
	GetTextDocument(ctx context.Context, doc protocol.TextDocumentIdentifier) (protocol.TextDocumentItem, error)

	// Returns the current status of a given document within a session.
	GetDocumentState(ctx context.Context, doc protocol.TextDocumentIdentifier) (DocumentState, error)

	// Adds edits to the pending edits for the current version of the text document.
	// Returns true if the edits were added, false if edits were ignored (e.g. due to outdated document version).
	AddPendingEdits(ctx context.Context, doc protocol.TextDocumentItem, edits []protocol.TextEdit) (bool, error)

	// Returns a position mapper that can be used to LSP positions between base and current versions of the document..
	GetPositionMapper(ctx context.Context, doc protocol.TextDocumentIdentifier) (PositionMapper, error)

	// ResetBase resets the base for a given document.
	ResetBase(ctx context.Context, workspaceRoot string, doc protocol.TextDocumentIdentifier) error
}

// Params are inbound parameters to initialize a new plugin.
type Params struct {
	fx.In

	Sessions   session.Repository
	IdeGateway ideclient.Gateway
	Logger     *zap.SugaredLogger
	Stats      tally.Scope
	Config     config.Provider
	FS         fs.UlspFS
}

type documentStoreEntry struct {
	Document            protocol.TextDocumentItem
	Edits               []protocol.TextEdit
	EditedSinceLastSave bool
	ProgressToken       *protocol.ProgressToken
	Base                *documentStoreEntry
	mu                  sync.Mutex
	positionMapper      PositionMapper
}

type documentStore map[uuid.UUID]map[protocol.TextDocumentIdentifier]*documentStoreEntry

type controller struct {
	sessions         session.Repository
	ideGateway       ideclient.Gateway
	logger           *zap.SugaredLogger
	documents        documentStore
	documentsMu      sync.RWMutex
	stats            tally.Scope
	maxFileSizeBytes int64
	fs               fs.UlspFS
}

// New creates a new controller for document sync.
func New(p Params) Controller {

	var maxFileSizeBytes int64
	if err := p.Config.Get(_maxFileSizeKey).Populate(&maxFileSizeBytes); err != nil || maxFileSizeBytes == 0 {
		panic(fmt.Errorf("unable to get maximum file size from config: %w", err))
	}

	c := &controller{
		sessions:         p.Sessions,
		ideGateway:       p.IdeGateway,
		logger:           p.Logger.With("plugin", _nameKey),
		documents:        make(documentStore),
		stats:            p.Stats.SubScope("doc_sync"),
		maxFileSizeBytes: maxFileSizeBytes,
		fs:               p.FS,
	}
	defer c.updateMetrics(context.Background())
	return c
}

// StartupInfo returns PluginInfo for this controller.
func (c *controller) StartupInfo(ctx context.Context) (ulspplugin.PluginInfo, error) {
	// Set a priority for each method that this module provides.
	priorities := map[string]ulspplugin.Priority{
		protocol.MethodInitialize: ulspplugin.PriorityHigh,
		protocol.MethodShutdown:   ulspplugin.PriorityAsync,

		protocol.MethodTextDocumentDidOpen:           ulspplugin.PriorityHigh,
		protocol.MethodTextDocumentDidChange:         ulspplugin.PriorityHigh,
		protocol.MethodTextDocumentDidClose:          ulspplugin.PriorityAsync,
		protocol.MethodTextDocumentDidSave:           ulspplugin.PriorityHigh,
		protocol.MethodTextDocumentWillSaveWaitUntil: ulspplugin.PriorityRegular,
		ulspplugin.MethodEndSession:                  ulspplugin.PriorityRegular,
	}

	// Assign method keys to implementations.
	methods := &ulspplugin.Methods{
		PluginNameKey: _nameKey,

		Initialize: c.initialize,
		Shutdown:   c.shutdown,

		DidOpen:           c.didOpen,
		DidChange:         c.didChange,
		DidClose:          c.didClose,
		DidSave:           c.didSave,
		WillSaveWaitUntil: c.willSaveWaitUntil,

		EndSession: c.endSession,
	}

	return ulspplugin.PluginInfo{
		Priorities: priorities,
		Methods:    methods,
		NameKey:    _nameKey,
	}, nil
}

func (c *controller) GetTextDocument(ctx context.Context, doc protocol.TextDocumentIdentifier) (protocol.TextDocumentItem, error) {
	entry, err := c.getDocumentStoreEntry(ctx, doc)
	if err != nil {
		return protocol.TextDocumentItem{}, err
	}
	return entry.Document, nil
}

func (c *controller) getDocumentStoreEntry(ctx context.Context, doc protocol.TextDocumentIdentifier) (*documentStoreEntry, error) {
	s, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return nil, err
	}

	c.documentsMu.RLock()
	defer c.documentsMu.RUnlock()

	if _, ok := c.documents[s.UUID]; !ok {
		return nil, &ulsperrors.UUIDNotFoundError{UUID: s.UUID}
	}

	entry, ok := c.documents[s.UUID][doc]
	if !ok {
		return nil, &ulsperrors.DocumentNotFoundError{Document: doc}
	}

	return entry, nil
}

func (c *controller) GetDocumentState(ctx context.Context, doc protocol.TextDocumentIdentifier) (DocumentState, error) {
	s, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return 0, err
	}

	c.documentsMu.RLock()
	defer c.documentsMu.RUnlock()

	if _, ok := c.documents[s.UUID]; !ok {
		return 0, &ulsperrors.UUIDNotFoundError{UUID: s.UUID}
	}

	entry, ok := c.documents[s.UUID][doc]
	if !ok {
		return DocumentStateClosed, nil
	}

	if entry.EditedSinceLastSave {
		contentOnDisk, err := c.fs.ReadFile(doc.URI.Filename())
		if err != nil {
			return 0, fmt.Errorf("unable to open file %q: %w", doc.URI.Filename(), err)
		}

		if string(contentOnDisk) != entry.Document.Text {
			return DocumentStateOpenDirty, nil
		}
	}
	return DocumentStateOpenClean, nil
}

func (c *controller) AddPendingEdits(ctx context.Context, doc protocol.TextDocumentItem, edits []protocol.TextEdit) (bool, error) {
	if len(edits) == 0 {
		return false, nil
	}

	entry, err := c.addEdits(ctx, doc, edits)
	if err != nil {
		return false, err
	}

	entry, err = c.setProgressToken(ctx, protocol.TextDocumentIdentifier{URI: doc.URI})
	c.ideGateway.WorkDoneProgressCreate(ctx, &protocol.WorkDoneProgressCreateParams{
		Token: *entry.ProgressToken,
	})
	_, fileName := path.Split(doc.URI.Filename())
	c.ideGateway.Progress(ctx, &protocol.ProgressParams{
		Token: *entry.ProgressToken,
		Value: &protocol.WorkDoneProgressBegin{
			Kind:    protocol.WorkDoneProgressKindBegin,
			Title:   fmt.Sprintf("Auto-Fixes for %q", fileName),
			Message: "Save to apply available fixes.",
		},
	})

	return true, nil
}

func (c *controller) GetPositionMapper(ctx context.Context, doc protocol.TextDocumentIdentifier) (PositionMapper, error) {
	d, err := c.getDocumentStoreEntry(ctx, doc)
	if err != nil {
		var docNotFound *ulsperrors.DocumentNotFoundError
		if errors.As(err, &docNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("getting document: %w", err)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.positionMapper == nil && d.Base != nil {
		d.positionMapper = NewPositionMapper(d.Base.Document.Text, d.Document.Text)
	} else if d.positionMapper == nil {
		d.positionMapper = NewPositionMapper(d.Document.Text, d.Document.Text)
	}
	return d.positionMapper, nil
}

func (c *controller) ResetBase(ctx context.Context, workspaceRoot string, doc protocol.TextDocumentIdentifier) error {
	sessions, err := c.sessions.GetAllFromWorkspaceRoot(ctx, workspaceRoot)
	if err != nil {
		return err
	}

	c.documentsMu.Lock()
	defer c.documentsMu.Unlock()

	// Reset the base in any sessions where this document is present.
	for _, s := range sessions {
		if _, ok := c.documents[s.UUID]; !ok {
			continue
		}

		if doc, ok := c.documents[s.UUID][doc]; ok {
			doc.clearBase()
		}
	}

	return nil
}

// initialize adds an entry to keep track of this session's documents.
func (c *controller) initialize(ctx context.Context, params *protocol.InitializeParams, result *protocol.InitializeResult) error {
	defer c.updateMetrics(ctx)
	s, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return err
	}

	c.documentsMu.Lock()
	defer c.documentsMu.Unlock()
	c.documents[s.UUID] = make(map[protocol.TextDocumentIdentifier]*documentStoreEntry)
	return nil
}

// shutdown removes this session's documents.
func (c *controller) shutdown(ctx context.Context) error {
	defer c.updateMetrics(ctx)
	s, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return err
	}

	return c.disposeSession(ctx, s.UUID)
}

// endSession removes this session's documents in the event that no shutdown request is received.
func (c *controller) endSession(ctx context.Context, uuid uuid.UUID) error {
	defer c.updateMetrics(ctx)
	return c.disposeSession(ctx, uuid)
}

// didOpen adds an entry for a newly opened document and stores its initial contents.
func (c *controller) didOpen(ctx context.Context, params *protocol.DidOpenTextDocumentParams) error {
	defer c.updateMetrics(ctx)
	s, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return err
	}

	c.documentsMu.Lock()
	defer c.documentsMu.Unlock()
	if c.documents[s.UUID] == nil {
		return &ulsperrors.UUIDNotFoundError{UUID: s.UUID}
	}

	if err := c.validateSize(ctx, params.TextDocument.Text); err != nil {
		// It is expected that some documents will exceed configured size limit. Log a warning which can be used to monitor and adjust the threshold.
		// If there are future attempts to access this document, those will result in errors.
		c.logger.Warnf("unable to track open document %q: %w", params.TextDocument.URI, err)
		return nil
	}

	c.documents[s.UUID][protocol.TextDocumentIdentifier{URI: params.TextDocument.URI}] = newDocumentStoreEntry(params.TextDocument, nil)

	return nil
}

// didClose deletes the entry for a closed document.
func (c *controller) didClose(ctx context.Context, params *protocol.DidCloseTextDocumentParams) error {
	defer c.updateMetrics(ctx)

	deletedEntry, err := c.deleteDocument(ctx, protocol.TextDocumentIdentifier{URI: params.TextDocument.URI})
	if err != nil {
		return err
	}

	if deletedEntry == nil {
		return nil
	}

	if deletedEntry.ProgressToken != nil {
		return c.ideGateway.Progress(ctx, &protocol.ProgressParams{
			Token: *deletedEntry.ProgressToken,
			Value: &protocol.WorkDoneProgressEnd{Kind: protocol.WorkDoneProgressKindEnd},
		})
	}
	return nil
}

// didChange updates the document with the latest incoming changes.
func (c *controller) didChange(ctx context.Context, params *protocol.DidChangeTextDocumentParams) error {
	oldEntry, _, err := c.updateDocumentText(ctx, params)
	if err != nil {
		return fmt.Errorf("adding changes to document: %w", err)
	}

	if oldEntry.ProgressToken != nil {
		err = c.ideGateway.Progress(ctx, &protocol.ProgressParams{
			Token: *oldEntry.ProgressToken,
			Value: &protocol.WorkDoneProgressEnd{Kind: protocol.WorkDoneProgressKindEnd},
		})
		if err != nil {
			c.logger.Errorf("unable to end progress for document %q: %v", oldEntry.Document.URI, err)
		}
	}

	return nil
}

func (c *controller) didSave(ctx context.Context, params *protocol.DidSaveTextDocumentParams) error {
	defer c.updateMetrics(ctx)
	s, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return err
	}

	c.documentsMu.Lock()
	defer c.documentsMu.Unlock()

	docEntry, ok := c.documents[s.UUID][params.TextDocument]
	if !ok {
		return &ulsperrors.DocumentNotFoundError{Document: params.TextDocument}
	}

	newEntry := newDocumentStoreEntry(docEntry.Document, docEntry.Base)
	// Document text should already be updated by didChange, but this reconciles it in case something got out of sync.
	newEntry.Document.Text = params.Text
	c.documents[s.UUID][params.TextDocument] = newEntry

	return nil
}

// willSaveWaitUntil contributes pending edits for the current document version to the request result.
func (c *controller) willSaveWaitUntil(ctx context.Context, params *protocol.WillSaveTextDocumentParams, result *[]protocol.TextEdit) error {
	s, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return err
	}

	c.documentsMu.Lock()
	defer c.documentsMu.Unlock()

	entry, ok := c.documents[s.UUID][params.TextDocument]
	if !ok {
		return &ulsperrors.DocumentNotFoundError{Document: params.TextDocument}
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()
	*result = append(*result, entry.Edits...)

	return nil
}

// disposeSession removes a session's documents based on the session UUID.
func (c *controller) disposeSession(ctx context.Context, uuid uuid.UUID) error {
	c.documentsMu.Lock()
	defer c.documentsMu.Unlock()
	delete(c.documents, uuid)

	return nil
}

func (c *controller) updateMetrics(ctx context.Context) {
	c.documentsMu.RLock()
	defer c.documentsMu.RUnlock()

	openDocs := 0
	openBytes := 0
	for _, sessionDocs := range c.documents {
		openDocs += len(sessionDocs)
		for _, entry := range sessionDocs {
			openBytes += len([]byte(entry.Document.Text))
		}
	}
	c.stats.Gauge("open_docs").Update(float64(openDocs))
	c.stats.Gauge("open_bytes").Update(float64(openBytes))
}

func (c *controller) validateSize(ctx context.Context, text string) error {
	if c.maxFileSizeBytes == 0 {
		return fmt.Errorf("max file size is not set")
	}

	size := int64(len([]byte(text)))
	if size > c.maxFileSizeBytes {
		return &ulsperrors.DocumentSizeLimitError{Size: size}
	}
	return nil
}

func (c *controller) updateDocumentText(ctx context.Context, params *protocol.DidChangeTextDocumentParams) (oldEntry *documentStoreEntry, newEntry *documentStoreEntry, err error) {
	s, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return nil, nil, err
	}

	c.documentsMu.Lock()
	defer c.documentsMu.Unlock()
	initialEntry, ok := c.documents[s.UUID][params.TextDocument.TextDocumentIdentifier]
	if !ok {
		return nil, nil, &ulsperrors.DocumentNotFoundError{Document: params.TextDocument.TextDocumentIdentifier}
	}

	doc := initialEntry.Document
	doc.Text, err = mapper.ApplyContentChanges(doc.Text, params.ContentChanges)
	if err != nil {
		return nil, nil, err
	}

	if err := c.validateSize(ctx, doc.Text); err != nil {
		return nil, nil, fmt.Errorf("unable to add changes to document %q: %w", doc.URI, err)
	}

	doc.Version = params.TextDocument.Version

	// If the document already has a base set, propagate it to the new entry.
	base := initialEntry.Base
	if base == nil {
		// If the document has no base, the initial entry becomes the base.
		base = initialEntry
	}

	result := newDocumentStoreEntry(doc, base)
	result.EditedSinceLastSave = true
	c.documents[s.UUID][params.TextDocument.TextDocumentIdentifier] = result
	return initialEntry, result, nil
}

func (c *controller) addEdits(ctx context.Context, doc protocol.TextDocumentItem, edits []protocol.TextEdit) (*documentStoreEntry, error) {
	s, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return nil, err
	}

	if c.documents[s.UUID] == nil {
		return nil, &ulsperrors.UUIDNotFoundError{UUID: s.UUID}
	}

	c.documentsMu.Lock()
	defer c.documentsMu.Unlock()
	entry, ok := c.documents[s.UUID][protocol.TextDocumentIdentifier{URI: doc.URI}]
	if !ok {
		return nil, &ulsperrors.DocumentNotFoundError{Document: protocol.TextDocumentIdentifier{URI: doc.URI}}
	}

	if entry.Document.Version != doc.Version {
		c.logger.Infof("ignoring edits for outdated document %q. Received edits for version %q, Current version is %q", doc.URI, doc.Version, entry.Document.Version)
		return nil, &ulsperrors.DocumentOutdatedError{CurrentDocument: entry.Document, OutdatedDocument: doc}
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()
	entry.Edits = append(entry.Edits, edits...)

	c.documents[s.UUID][protocol.TextDocumentIdentifier{URI: doc.URI}] = entry
	return entry, nil
}

func (c *controller) setProgressToken(ctx context.Context, doc protocol.TextDocumentIdentifier) (*documentStoreEntry, error) {
	s, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return nil, err
	}

	c.documentsMu.Lock()
	defer c.documentsMu.Unlock()

	if c.documents[s.UUID] == nil {
		return nil, &ulsperrors.UUIDNotFoundError{UUID: s.UUID}
	}

	entry, ok := c.documents[s.UUID][protocol.TextDocumentIdentifier{URI: doc.URI}]
	if !ok {
		return nil, &ulsperrors.DocumentNotFoundError{Document: protocol.TextDocumentIdentifier{URI: doc.URI}}
	}
	entry.ProgressToken = protocol.NewProgressToken(uuid.Must(uuid.NewV4()).String())
	c.documents[s.UUID][protocol.TextDocumentIdentifier{URI: doc.URI}] = entry
	return entry, nil
}

func (c *controller) deleteDocument(ctx context.Context, doc protocol.TextDocumentIdentifier) (*documentStoreEntry, error) {
	s, err := c.sessions.GetFromContext(ctx)
	if err != nil {
		return nil, err
	}

	c.documentsMu.Lock()
	defer c.documentsMu.Unlock()

	if entry, ok := c.documents[s.UUID][doc]; ok {
		delete(c.documents[s.UUID], doc)
		return entry, nil
	}
	return nil, nil
}

func newDocumentStoreEntry(doc protocol.TextDocumentItem, base *documentStoreEntry) *documentStoreEntry {
	e := documentStoreEntry{
		Document:            doc,
		Edits:               make([]protocol.TextEdit, 0),
		EditedSinceLastSave: false,
		Base:                base,
	}
	return &e
}

// clearBase clears any stored base version for the document, and resets the position mapper.
func (d *documentStoreEntry) clearBase() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Base = nil
	d.positionMapper = nil
}
