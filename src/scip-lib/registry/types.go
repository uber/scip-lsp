package registry

import (
	"io"

	"github.com/uber/scip-lsp/src/scip-lib/model"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// PackageID is a unique identifier for a package
type PackageID string

// Registry is an interface that abstracts SCIP data access
type Registry interface {
	LoadConcurrency() int
	SetDocumentLoadedCallback(func(*model.Document))
	LoadIndex(indexReader io.ReadSeeker) error
	LoadIndexFile(indexPath string) error
	DidOpen(uri uri.URI, text string) error
	DidClose(uri uri.URI) error

	// GetSymbolDefinitionOccurrence returns the definition occurrence for a given symbol
	GetSymbolDefinitionOccurrence(descriptors []model.Descriptor, version string) (*model.SymbolOccurrence, error)
	// Definition returns the source occurence and the definition occurence for a given position
	Definition(uri uri.URI, loc protocol.Position) (*model.SymbolOccurrence, *model.SymbolOccurrence, error)
	// References returns the locations a symbol is referenced at in the entire index
	References(uri uri.URI, loc protocol.Position) ([]protocol.Location, error)
	// Implementations returns the implementing symbols for a given (typically abstract/interface) symbol.
	// If the underlying registry does not support this capability, it should return an empty slice and nil error.
	Implementations(symbol string) ([]string, error)
	// Hover returns the hover information for a given position, as well as it's occurrence
	Hover(uri uri.URI, loc protocol.Position) (string, *model.Occurrence, error)
	// DocumentSymbols returns the document symbols for a given document
	DocumentSymbols(uri uri.URI) ([]*model.SymbolOccurrence, error)
	// Diagnostics returns the diagnostics for a given document
	Diagnostics(uri uri.URI) ([]*model.Diagnostic, error)
	// GetURI gets the full path to a document as an LSP uri.
	GetURI(relPath string) uri.URI
}
