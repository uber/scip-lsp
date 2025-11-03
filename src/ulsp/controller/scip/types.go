package scip

import (
	"io"
	"path"
	"sync"

	"github.com/sourcegraph/scip/bindings/go/scip"
	"github.com/uber/scip-lsp/src/scip-lib/mapper"
	"github.com/uber/scip-lsp/src/scip-lib/model"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// PackageID is a unique identifier for a package
type PackageID string

var _unknownPkg = model.ScipPackage{
	Manager: "ulsp",
	Name:    "unknown",
	Version: "0.0.1",
}

// FileOccurences containes all the occurences of a particular symbol in a file
type FileOccurences struct {
	Occurrences []*model.Occurrence
	file        uri.URI
}

// Registry is an interface that abstracts SCIP data access
type Registry interface {
	LoadConcurrency() int
	SetDocumentLoadedCallback(func(*model.Document))
	LoadIndex(indexReader io.ReadSeeker) error
	LoadIndexFile(indexPath string) error
	DidOpen(uri uri.URI, text string) error
	DidClose(uri uri.URI) error
	// GetSymbolInformation returns the symbol information for a given position (does not require a full loaded index)
	GetSymbolOccurrence(uri uri.URI, loc protocol.Position) (*model.SymbolOccurrence, error)
	// GetSymbolDefinitionOccurrence returns the definition occurrence for a given symbol
	GetSymbolDefinitionOccurrence(descriptors []model.Descriptor, version string) (*model.SymbolOccurrence, error)
	// Definition returns the source occurence and the definition occurence for a given position
	Definition(uri uri.URI, loc protocol.Position) (*model.SymbolOccurrence, *model.SymbolOccurrence, error)
	// References returns the locations a symbol is referenced at in the entire index
	References(uri uri.URI, loc protocol.Position) ([]protocol.Location, error)
	// Implementation returns the locations where a symbol is implemented
	Implementation(uri uri.URI, loc protocol.Position) ([]protocol.Location, error)
	// Hover returns the hover information for a given position, as well as it's occurrence
	Hover(uri uri.URI, loc protocol.Position) (string, *model.Occurrence, error)
	// DocumentSymbols returns the document symbols for a given document
	DocumentSymbols(uri uri.URI) ([]*model.SymbolOccurrence, error)
	// Diagnostics returns the diagnostics for a given document
	Diagnostics(uri uri.URI) ([]*model.Diagnostic, error)
	GetSymbolForPosition(uri uri.URI, loc protocol.Position) (*model.Occurrence, *SymbolData, error)
	GetDocumentSymbolForFile(uri uri.URI) (*[]*SymbolData, error)
	GetFileInfo(uri uri.URI) *FileInfo
	GetPackageInfo(pkgID PackageID) *PackageMeta
	// GetURI gets the full path to a document as an LSP uri.
	GetURI(relPath string) uri.URI
}

// registryImpl keeps track of all the loaded indices
type registryImpl struct {
	WorkspaceRoot    string
	Packages         map[PackageID]*PackageMeta
	Documents        map[uri.URI]*FileInfo
	onDocumentLoaded func(*model.Document)
}

// GetURI gets the full path to a document as an LSP uri.
func (r *registryImpl) GetURI(relPath string) uri.URI {
	return uri.File(path.Join(r.WorkspaceRoot, relPath))
}

func (r *registryImpl) getOrCreatePackage(pkg *model.ScipPackage) *PackageMeta {
	_, ok := r.Packages[PackageID(pkg.Name)]
	if !ok {
		r.Packages[PackageID(pkg.Name)] = NewScipPackage(pkg)
	}

	return r.Packages[PackageID(pkg.Name)]
}

func (r *registryImpl) ClearReferences(docURI uri.URI) {
	doc, ok := r.Documents[docURI]
	if !ok {
		return
	}

	// Reset references from this doc to the remote packages
	for _, ref := range doc.ExternalRefs {
		ref.Occurrences = make([]*model.Occurrence, 0)
	}

	doc.Package.mu.Lock()
	defer doc.Package.mu.Unlock()
	for _, sd := range doc.Package.SymbolData {
		if sd.Location != nil && sd.Location.TargetURI == docURI {
			// remove the definition for the symbol that was defined here
			// We want to keep the SymbolData around so we can keep the references from other files to this symb available
			sd.mu.Lock()
			defer sd.mu.Unlock()
			sd.Definition = nil
			sd.Location = nil
		}
	}

	delete(r.Documents, docURI)
}

func (p *PackageMeta) getOrCreateSymbolData(symbol string) *SymbolData {
	_, ok := p.SymbolData[symbol]
	if !ok {
		p.SymbolData[symbol] = NewSymbolData(nil, nil, nil)
	}

	return p.SymbolData[symbol]
}

func (p *PackageMeta) storeLocalSymbols(localSymbs map[string]*model.SymbolInformation, localOccs map[string][]*model.Occurrence, docURI uri.URI) {
	storeLocalSymbols(localSymbs, localOccs, docURI, p.getOrCreateSymbolData)
}

func storeLocalSymbols(localSymbs map[string]*model.SymbolInformation, localOccs map[string][]*model.Occurrence, docURI uri.URI, symbolDataFn func(string) *SymbolData) {
	for symbol, info := range localSymbs {
		sd := symbolDataFn(symbol)
		sd.Info = info
	}
	for symbol, occs := range localOccs {
		sd := symbolDataFn(symbol)
		for _, occ := range occs {
			if occ.SymbolRoles&int32(scip.SymbolRole_Definition) > 0 {
				sd.Definition = occ
				sd.Location = mapper.ScipOccurrenceToLocationLink(docURI, occ, nil)
			} else {
				sd.AddReference(docURI, PackageID("local"), occ)
			}
		}
	}
}

// FileInfo stores information about a document, including its package
type FileInfo struct {
	URI          uri.URI
	Document     *model.Document
	Package      *PackageMeta
	Locals       map[string]*SymbolData
	Definitions  map[string]*SymbolData
	ExternalRefs []*FileOccurences
}

func (f *FileInfo) getOrCreateSymbolData(symbol string) *SymbolData {
	_, ok := f.Locals[symbol]
	if !ok {
		f.Locals[symbol] = NewSymbolData(nil, nil, nil)
	}

	return f.Locals[symbol]
}

func (f *FileInfo) storeLocalSymbols(localSymbs map[string]*model.SymbolInformation, localOccs map[string][]*model.Occurrence) {
	storeLocalSymbols(localSymbs, localOccs, f.URI, f.getOrCreateSymbolData)
}

// PackageMeta stores symbol information across an entire package
type PackageMeta struct {
	mu  *sync.Mutex
	Pkg *model.ScipPackage
	// SymbolData maps from symbol moniker to a data provider
	SymbolData map[string]*SymbolData
}

// SymbolData stores references to the definition, location, and any references to this symbol
type SymbolData struct {
	mu         *sync.Mutex
	Info       *model.SymbolInformation
	Definition *model.Occurrence
	Location   *protocol.LocationLink
	// References stores all the locations this symbol is referenced.
	// This is indexed by package that References it for easy updating when a package gets a new index.
	// This is further aligned as an array of files so we can return an organized result of References when requested.
	References map[PackageID][]*FileOccurences
}

// GetSymbolInformation returns the symbol information for a symbol
func (sd *SymbolData) GetSymbolInformation() *model.SymbolInformation {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	return sd.Info
}

// AddReference adds an occurence that's defined in a package+file into the symboldata of the package that defines the symbol.
func (sd *SymbolData) AddReference(docURI uri.URI, pkgID PackageID, occ *model.Occurrence) *FileOccurences {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	if _, ok := sd.References[pkgID]; !ok {
		sd.References[pkgID] = make([]*FileOccurences, 0)
	}
	for _, fo := range sd.References[pkgID] {
		if fo.file == docURI {
			fo.Occurrences = append(fo.Occurrences, occ)
			return fo
		}
	}

	fo := &FileOccurences{
		file:        docURI,
		Occurrences: append([]*model.Occurrence{}, occ),
	}
	sd.References[pkgID] = append(sd.References[pkgID], fo)

	return fo
}

// NewSymbolData initializes a SymbolData struct
func NewSymbolData(info *model.SymbolInformation, definition *model.Occurrence, location *protocol.LocationLink) *SymbolData {
	return &SymbolData{
		&sync.Mutex{},
		info,
		definition,
		location,
		make(map[PackageID][]*FileOccurences),
	}
}

// NewScipPackage instantiates a new ScipPackage
func NewScipPackage(pkg *model.ScipPackage) *PackageMeta {
	return &PackageMeta{
		mu:         &sync.Mutex{},
		Pkg:        pkg,
		SymbolData: make(map[string]*SymbolData),
	}
}

// NewFileInfo instantiates a new FileInfo
func NewFileInfo(uri uri.URI, doc *model.Document, pkg *PackageMeta, definitions map[string]*SymbolData, externalRefs []*FileOccurences) *FileInfo {
	if externalRefs == nil {
		externalRefs = make([]*FileOccurences, 0)
	}
	return &FileInfo{
		URI:          uri,
		Document:     doc,
		Package:      pkg,
		Definitions:  definitions,
		Locals:       make(map[string]*SymbolData),
		ExternalRefs: externalRefs,
	}
}
