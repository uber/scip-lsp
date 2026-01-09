package registry

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
	"github.com/uber/scip-lsp/src/scip-lib/utils"
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

func (p *partialScipRegistry) LoadIndex(indexReader io.ReadSeeker) error {
	if indexReader == nil {
		return errors.New("index reader is nil")
	}
	return p.Index.LoadIndex("", indexReader)
}

func (p *partialScipRegistry) LoadIndexFile(indexPath string) error {
	return p.Index.LoadIndexFile(indexPath)
}

func (p *partialScipRegistry) SetDocumentLoadedCallback(callback func(*model.Document)) {
	p.Index.SetDocumentLoadedCallback(callback)
}

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

func (p *partialScipRegistry) DidClose(sourceURI uri.URI) error {
	return nil
}

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

	definitionOccs := utils.GetOccurrencesForSymbol(defDoc.Occurrences, symbolInformation.Symbol, scip.SymbolRole_Definition)
	if len(definitionOccs) > 0 {
		result.Occurrence = definitionOccs[0]
	}

	return result, nil
}

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

	sourceOccurrence = utils.GetOccurrenceForPosition(doc.Occurrences, pos)
	if sourceOccurrence == nil {
		return nil, nil, nil
	}

	if scip.IsLocalSymbol(sourceOccurrence.Symbol) {
		// Local symbols are file unique, so we can just return the first definition occurrence
		definitionOccs := utils.GetOccurrencesForSymbol(doc.Occurrences, sourceOccurrence.Symbol, scip.SymbolRole_Definition)
		if len(definitionOccs) > 0 {
			definitionOcc = definitionOccs[0]
			defDocURI = sourceURI
		}
		// A local symbol may not have SymbolInformation
		symbolInformation = utils.GetLocalSymbolInformation(doc.Symbols, sourceOccurrence.Symbol)

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

func (p *partialScipRegistry) References(sourceURI uri.URI, pos protocol.Position) ([]protocol.Location, error) {
	doc, err := p.Index.LoadDocument(p.uriToRelativePath(sourceURI))
	if err != nil {
		p.logger.Errorf("failed to load document %s: %s", sourceURI, err)
		return nil, err
	}
	if doc == nil {
		return nil, nil
	}

	sourceOccurrence := utils.GetOccurrenceForPosition(doc.Occurrences, pos)
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

// Implementations returns all known implementing symbols for the given (typically abstract/interface) symbol.
// Note: this is based on SymbolInformation.Relationships in loaded indices.
func (p *partialScipRegistry) Implementations(symbol string) ([]string, error) {
	return p.Index.Implementations(symbol)
}

func (p *partialScipRegistry) Hover(uri uri.URI, pos protocol.Position) (string, *model.Occurrence, error) {
	doc, err := p.Index.LoadDocument(p.uriToRelativePath(uri))
	if err != nil {
		p.logger.Errorf("failed to load document %s: %s", uri, err)
		return "", nil, err
	}
	if doc == nil {
		return "", nil, nil
	}

	occurrence := utils.GetOccurrenceForPosition(doc.Occurrences, pos)
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

func (p *partialScipRegistry) Diagnostics(uri uri.URI) ([]*model.Diagnostic, error) {
	return nil, errors.New("not implemented")
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
