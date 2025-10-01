package scip

import (
	"errors"
	"io"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sourcegraph/scip/bindings/go/scip"
	"github.com/uber/scip-lsp/src/scip-lib/mapper"
	"github.com/uber/scip-lsp/src/scip-lib/model"
	"github.com/uber/scip-lsp/src/scip-lib/partialloader"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
	"go.uber.org/zap"
)

type partialScipRegistry struct {
	WorkspaceRoot string
	Index         partialloader.PartialIndex
	IndexFolder   string
	logger        *zap.SugaredLogger
}

// Legacy methods

// GetURI gets the full path to a document as an LSP uri.
func (p *partialScipRegistry) GetURI(relPath string) uri.URI {
	return uri.File(path.Join(p.WorkspaceRoot, relPath))
}

// GetDocumentSymbolForFile implements Registry.
func (p *partialScipRegistry) GetDocumentSymbolForFile(uri uri.URI) (*[]*SymbolData, error) {
	return nil, errors.New("not implemented")
}

// GetFileInfo implements Registry.
func (p *partialScipRegistry) GetFileInfo(uri uri.URI) *FileInfo {
	return nil
}

// GetPackageInfo implements Registry.
func (p *partialScipRegistry) GetPackageInfo(pkgID PackageID) *PackageMeta {
	return nil
}

// GetSymbolForPosition implements Registry.
func (p *partialScipRegistry) GetSymbolForPosition(uri uri.URI, loc protocol.Position) (*model.Occurrence, *SymbolData, error) {
	return nil, nil, errors.New("not implemented")
}

// vNext Methods
// LoadIndex implements Registry.
func (p *partialScipRegistry) LoadIndex(indexReader io.ReadSeeker) error {
	if indexReader == nil {
		return errors.New("index reader is nil")
	}
	return p.Index.LoadIndex("", indexReader)
}

// LoadIndexFile implements Registry.
func (p *partialScipRegistry) LoadIndexFile(indexPath string) error {
	return p.Index.LoadIndexFile(indexPath)
}

// SetDocumentLoadedCallback implements Registry.
func (p *partialScipRegistry) SetDocumentLoadedCallback(callback func(*model.Document)) {
	p.Index.SetDocumentLoadedCallback(callback)
}

// DidOpen implements Registry.
func (p *partialScipRegistry) DidOpen(uri uri.URI, text string) error {
	relativePath := p.uriToRelativePath(uri)

	doc, err := p.Index.LoadDocument(relativePath)
	if err != nil {
		p.logger.Errorf("failed to load document %s: %s", relativePath, err)
		return err
	}

	if doc == nil {
		p.logger.Infof("document not found: %s", relativePath)
		return nil
	}

	return nil
}

// DidClose implements Registry.
func (p *partialScipRegistry) DidClose(sourceURI uri.URI) error {
	return nil
}

// TODO(IDE-1640): Add support for the registry to store and retrieve multiple versions of the same symbol from different jars that may reference different source documents.
func (p *partialScipRegistry) GetSymbolDefinitionOccurrence(descriptors []model.Descriptor, version string) (*model.SymbolOccurrence, error) {
	symbolInformation, defDocPath, err := p.Index.GetSymbolInformationFromDescriptors(descriptors, version)
	if err != nil {
		return nil, err
	}

	if symbolInformation == nil {
		return nil, nil
	}

	result := &model.SymbolOccurrence{
		Info:     symbolInformation,
		Location: uri.File(filepath.Join(p.WorkspaceRoot, defDocPath)),
	}

	defDoc, err := p.Index.LoadDocument(defDocPath)
	if err != nil {
		return nil, err
	} else if defDoc == nil {
		return nil, nil
	}

	definitionOccs := GetOccurrencesForSymbol(defDoc.Occurrences, symbolInformation.Symbol, scip.SymbolRole_Definition)
	if len(definitionOccs) > 0 {
		result.Occurrence = definitionOccs[0]
	}

	return result, nil
}

// Definition implements Registry.
func (p *partialScipRegistry) Definition(sourceURI uri.URI, pos protocol.Position) (sourceSymOcc *model.SymbolOccurrence, defSymOcc *model.SymbolOccurrence, err error) {
	doc, err := p.Index.LoadDocument(p.uriToRelativePath(sourceURI))
	if err != nil {
		p.logger.Errorf("failed to load document %s: %s", p.uriToRelativePath(sourceURI), err)
		return nil, nil, err
	}

	if doc == nil {
		return nil, nil, nil
	}

	var sourceOccurrence *model.Occurrence
	var definitionOcc *model.Occurrence
	var symbolInformation *model.SymbolInformation
	var defDocURI uri.URI

	sourceOccurrence = GetOccurrenceForPosition(doc.Occurrences, pos)
	if sourceOccurrence == nil {
		return nil, nil, nil
	}

	if scip.IsLocalSymbol(sourceOccurrence.Symbol) {
		// Local symbols are file unique, so we can just return the first definition occurrence
		definitionOccs := GetOccurrencesForSymbol(doc.Occurrences, sourceOccurrence.Symbol, scip.SymbolRole_Definition)
		if len(definitionOccs) > 0 {
			definitionOcc = definitionOccs[0]
			defDocURI = sourceURI
		}
		// A local symbol may not have SymbolInformation
		symbolInformation = GetLocalSymbolInformation(doc.Symbols, sourceOccurrence.Symbol)

	} else {
		// Descriptor-based lookup in the prefix tree for global symbols.
		symbol, err := model.ParseScipSymbol(sourceOccurrence.Symbol)
		if err != nil {
			p.logger.Errorf("failed to parse symbol %s: %s", sourceOccurrence.Symbol, err)
			return nil, nil, err
		}

		def, err := p.GetSymbolDefinitionOccurrence(mapper.ScipDescriptorsToModelDescriptors(symbol.Descriptors), symbol.Package.Version)
		if err != nil {
			p.logger.Errorf("failed to get symbol definition occurrence for %s: %s", sourceOccurrence.Symbol, err)
			return nil, nil, err
		} else if def == nil {
			p.logger.Errorf("failed to get symbol definition occurrence for %s: %s", sourceOccurrence.Symbol, err)
			return nil, nil, nil
		}

		definitionOcc = def.Occurrence
		defDocURI = def.Location
		symbolInformation = def.Info
	}

	sourceSymOcc = &model.SymbolOccurrence{
		Info:       symbolInformation,
		Occurrence: sourceOccurrence,
		Location:   sourceURI,
	}

	defSymOcc = &model.SymbolOccurrence{
		Info:       symbolInformation,
		Occurrence: definitionOcc,
		Location:   defDocURI,
	}

	return sourceSymOcc, defSymOcc, nil
}

// References implements Registry.
func (p *partialScipRegistry) References(sourceURI uri.URI, pos protocol.Position) ([]protocol.Location, error) {
	doc, err := p.Index.LoadDocument(p.uriToRelativePath(sourceURI))
	if err != nil {
		p.logger.Errorf("failed to load document %s: %s", sourceURI, err)
		return nil, err
	}
	if doc == nil {
		return nil, nil
	}

	sourceOccurrence := GetOccurrenceForPosition(doc.Occurrences, pos)
	if sourceOccurrence == nil {
		return nil, nil
	}

	// Get all references to this symbol across all documents
	locations := make([]protocol.Location, 0)

	// For local symbols, only search in current document
	if scip.IsLocalSymbol(sourceOccurrence.Symbol) {
		for _, occ := range doc.Occurrences {
			if occ.Symbol == sourceOccurrence.Symbol {
				locations = append(locations, *mapper.ScipOccurrenceToLocation(sourceURI, occ))
			}
		}
		return locations, nil
	}

	// For global symbols, search all documents
	occurrences, err := p.Index.References(sourceOccurrence.Symbol)
	if err != nil {
		p.logger.Errorf("failed to get references for %s: %s", sourceOccurrence.Symbol, err)
		return nil, err
	}

	for relDocPath, occs := range occurrences {
		for _, occ := range occs {
			locations = append(locations, *mapper.ScipOccurrenceToLocation(uri.File(filepath.Join(p.WorkspaceRoot, relDocPath)), occ))
		}
	}

	return locations, nil
}

// Hover implements Registry.
func (p *partialScipRegistry) Hover(uri uri.URI, pos protocol.Position) (string, *model.Occurrence, error) {
	doc, err := p.Index.LoadDocument(p.uriToRelativePath(uri))
	if err != nil {
		p.logger.Errorf("failed to load document %s: %s", uri, err)
		return "", nil, err
	}
	if doc == nil {
		return "", nil, nil
	}

	occurrence := GetOccurrenceForPosition(doc.Occurrences, pos)
	if occurrence == nil {
		return "", nil, nil
	}
	var docs string

	symbolInformation, _, err := p.Index.GetSymbolInformation(occurrence.Symbol)
	if err != nil {
		p.logger.Errorf("failed to get symbol information for %s: %s", occurrence.Symbol, err)
		return "", nil, err
	}

	if len(occurrence.OverrideDocumentation) > 0 {
		docs += strings.Join(occurrence.OverrideDocumentation, "\n")
	} else if symbolInformation != nil && len(symbolInformation.Documentation) > 0 {
		docs += strings.Join(symbolInformation.Documentation, "\n")
	} else if symbolInformation != nil && symbolInformation.SignatureDocumentation != nil {
		docs += symbolInformation.SignatureDocumentation.Text
	}

	return docs, occurrence, nil
}

// DocumentSymbols implements Registry.
func (p *partialScipRegistry) DocumentSymbols(uri uri.URI) ([]*model.SymbolOccurrence, error) {
	doc, err := p.Index.LoadDocument(p.uriToRelativePath(uri))
	if err != nil {
		p.logger.Errorf("failed to load document %s: %s", uri, err)
		return nil, err
	}

	if doc == nil {
		return nil, nil
	}

	symbolOccurrences := make([]*model.SymbolOccurrence, 0)
	for _, occ := range doc.Occurrences {
		if scip.IsGlobalSymbol(occ.Symbol) && occ.SymbolRoles&int32(scip.SymbolRole_Definition) > 0 {
			info := doc.SymbolMap[occ.Symbol]
			if info != nil {
				if info.DisplayName == "" {
					info.DisplayName = model.ParseScipSymbolToDisplayName(info.Symbol)
				}
				symbolOccurrences = append(symbolOccurrences, &model.SymbolOccurrence{
					Info:       info,
					Occurrence: occ,
					Location:   uri,
				})
			}
		}
	}

	return symbolOccurrences, nil
}

// Diagnostics implements Registry.
func (p *partialScipRegistry) Diagnostics(uri uri.URI) ([]*model.Diagnostic, error) {
	return nil, errors.New("not implemented")
}

// GetSymbolDataForSymbol implements Registry.
func (p *partialScipRegistry) GetSymbolDataForSymbol(symbol string, localPkg *PackageMeta) (*SymbolData, error) {
	return nil, errors.New("not implemented")
}

// GetSymbolOccurrence implements Registry.
func (p *partialScipRegistry) GetSymbolOccurrence(uri uri.URI, pos protocol.Position) (*model.SymbolOccurrence, error) {
	return nil, errors.New("not implemented")
}

// Implementation implements Registry.
func (p *partialScipRegistry) Implementation(sourceURI uri.URI, pos protocol.Position) ([]protocol.Location, error) {
	doc, err := p.Index.LoadDocument(p.uriToRelativePath(sourceURI))
	if err != nil {
		p.logger.Errorf("failed to load document %s: %s", sourceURI, err)
		return nil, err
	}
	if doc == nil {
		return nil, nil
	}

	sourceOccurrence := GetOccurrenceForPosition(doc.Occurrences, pos)
	if sourceOccurrence == nil {
		return nil, nil
	}

	// Local symbols typically don't have implementation relationships
	if scip.IsLocalSymbol(sourceOccurrence.Symbol) {
		return []protocol.Location{}, nil
	}

	locations := make([]protocol.Location, 0)

	// Fast path: use reverse implementors index
	implementors, err := p.Index.GetImplementationSymbols(sourceOccurrence.Symbol)
	if err != nil {
		p.logger.Errorf("failed to get implementing symbols for %s: %s", sourceOccurrence.Symbol, err)
	} else if len(implementors) > 0 {
		for _, implSym := range implementors {
			implementingSymbol, err := model.ParseScipSymbol(implSym)
			if err != nil {
				p.logger.Errorf("failed to parse implementing symbol %s: %s", implSym, err)
				continue
			}
			implOcc, err := p.GetSymbolDefinitionOccurrence(
				mapper.ScipDescriptorsToModelDescriptors(implementingSymbol.Descriptors),
				implementingSymbol.Package.Version,
			)
			if err != nil {
				p.logger.Errorf("failed to get definition for implementing symbol %s: %s", implSym, err)
				continue
			}
			if implOcc != nil && implOcc.Occurrence != nil {
				locations = append(locations, *mapper.ScipOccurrenceToLocation(implOcc.Location, implOcc.Occurrence))
			}
		}
		return locations, nil
	}

	// Fallback: traverse relationships on the abstract symbol if the reverse index is empty or there is error
	symbolInformation, _, err := p.Index.GetSymbolInformation(sourceOccurrence.Symbol)
	if err != nil {
		p.logger.Errorf("failed to get symbol information for %s: %s", sourceOccurrence.Symbol, err)
		return nil, err
	}
	if symbolInformation == nil {
		return []protocol.Location{}, nil
	}

	for _, relationship := range symbolInformation.Relationships {
		if relationship.IsImplementation {
			implementingSymbol, err := model.ParseScipSymbol(relationship.Symbol)
			if err != nil {
				p.logger.Errorf("failed to parse implementing symbol %s: %s", relationship.Symbol, err)
				continue
			}
			implOcc, err := p.GetSymbolDefinitionOccurrence(
				mapper.ScipDescriptorsToModelDescriptors(implementingSymbol.Descriptors),
				implementingSymbol.Package.Version,
			)
			if err != nil {
				p.logger.Errorf("failed to get definition for implementing symbol %s: %s", relationship.Symbol, err)
				continue
			}
			if implOcc != nil && implOcc.Occurrence != nil {
				locations = append(locations, *mapper.ScipOccurrenceToLocation(implOcc.Location, implOcc.Occurrence))
			}
		}
	}

	return locations, nil
}

func (p *partialScipRegistry) uriToRelativePath(uri uri.URI) string {
	rel, err := filepath.Rel(p.WorkspaceRoot, uri.Filename())
	if err != nil {
		return ""
	}
	return rel
}

func (p *partialScipRegistry) LoadConcurrency() int {
	return runtime.NumCPU() / 2
}

// NewPartialScipRegistry creates a new partial SCIP registry
func NewPartialScipRegistry(workspaceRoot string, indexFolder string, logger *zap.SugaredLogger) Registry {
	return &partialScipRegistry{
		WorkspaceRoot: workspaceRoot,
		IndexFolder:   indexFolder,
		Index:         partialloader.NewPartialLoadedIndex(indexFolder),
		logger:        logger,
	}
}
