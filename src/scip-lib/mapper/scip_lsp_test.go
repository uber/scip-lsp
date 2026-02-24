package mapper

import (
	"testing"

	"github.com/sourcegraph/scip/bindings/go/scip"
	"github.com/stretchr/testify/assert"
	"github.com/uber/scip-lsp/src/scip-lib/model"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

func TestScipToProtocolRange(t *testing.T) {
	tests := []struct {
		scipRange []int32
		expected  protocol.Range
	}{
		{
			scipRange: []int32{1, 10, 12},
			expected: protocol.Range{
				Start: protocol.Position{
					Line:      1,
					Character: 10,
				},
				End: protocol.Position{
					Line:      1,
					Character: 12,
				},
			},
		},
		{
			scipRange: []int32{0, 5, 3, 12},
			expected: protocol.Range{
				Start: protocol.Position{
					Line:      0,
					Character: 5,
				},
				End: protocol.Position{
					Line:      3,
					Character: 12,
				},
			},
		},
		{
			scipRange: []int32{0, 0, 0},
			expected: protocol.Range{
				Start: protocol.Position{
					Line:      0,
					Character: 0,
				},
				End: protocol.Position{
					Line:      0,
					Character: 0,
				},
			},
		},
	}

	for _, test := range tests {
		res := ScipToProtocolRange(test.scipRange)
		assert.Equal(t, test.expected, res)
	}
}

func TestScipOccurrenceToProtocolLink(t *testing.T) {
	occ := &model.Occurrence{
		Range: []int32{1, 3, 10, 5},
	}
	file := uri.File("/sample/file.go")
	expectedRange := protocol.Range{
		Start: protocol.Position{
			Line:      1,
			Character: 3,
		},
		End: protocol.Position{
			Line:      10,
			Character: 5,
		},
	}

	ll := ScipOccurrenceToLocationLink(file, occ, nil)
	assert.Equal(t, protocol.LocationLink{
		OriginSelectionRange: nil,
		TargetURI:            file,
		TargetRange:          expectedRange,
		TargetSelectionRange: expectedRange,
	}, *ll)
}

func TestScipOccurrenceToLocation(t *testing.T) {
	occ := &model.Occurrence{
		Range: []int32{1, 3, 10, 5},
	}
	file := uri.File("/sample/file.go")
	expectedRange := protocol.Range{
		Start: protocol.Position{
			Line:      1,
			Character: 3,
		},
		End: protocol.Position{
			Line:      10,
			Character: 5,
		},
	}

	ll := ScipOccurrenceToLocation(file, occ)
	assert.Equal(t, protocol.Location{
		URI:   file,
		Range: expectedRange,
	}, *ll)
}

func TestScipSymbolInformationToDocumentSymbol(t *testing.T) {

	rnge := []int32{1, 3, 10, 5}
	tests := []struct {
		symInfo        *model.SymbolInformation
		occ            *model.Occurrence
		expectedDocSym *protocol.DocumentSymbol
	}{
		{
			symInfo: &model.SymbolInformation{
				DisplayName: "dummy",
				Kind:        scip.SymbolInformation_Function,
			},
			occ: &model.Occurrence{
				Range: rnge,
			},
			expectedDocSym: &protocol.DocumentSymbol{
				Name:           "dummy",
				Detail:         "[uLSP]dummy",
				Kind:           protocol.SymbolKindFunction,
				Range:          ScipToProtocolRange(rnge),
				SelectionRange: ScipToProtocolRange(rnge),
			},
		},
		{
			symInfo: &model.SymbolInformation{
				DisplayName: "dummy",
				Kind:        scip.SymbolInformation_Class,
			},
			occ: &model.Occurrence{
				Range: rnge,
			},
			expectedDocSym: &protocol.DocumentSymbol{
				Name:           "dummy",
				Detail:         "[uLSP]dummy",
				Kind:           protocol.SymbolKindClass,
				Range:          ScipToProtocolRange(rnge),
				SelectionRange: ScipToProtocolRange(rnge),
			},
		},
	}

	for _, test := range tests {
		actualDocSym := ScipSymbolInformationToDocumentSymbol(test.symInfo, test.occ)
		assert.Equal(t, *test.expectedDocSym, *actualDocSym)
	}
}

func TestScipSymbolKindToDocumentSymbolKind(t *testing.T) {
	tests := []struct {
		symKind  scip.SymbolInformation_Kind
		expected protocol.SymbolKind
	}{
		{
			symKind:  scip.SymbolInformation_AbstractMethod,
			expected: protocol.SymbolKindMethod,
		},
		{
			symKind:  scip.SymbolInformation_Getter,
			expected: protocol.SymbolKindProperty,
		},
		{
			symKind:  scip.SymbolInformation_Parameter,
			expected: protocol.SymbolKindVariable,
		},
		{
			symKind:  scip.SymbolInformation_Method,
			expected: protocol.SymbolKindMethod,
		},
		{
			symKind:  scip.SymbolInformation_Function,
			expected: protocol.SymbolKindFunction,
		},
		{
			symKind:  scip.SymbolInformation_Signature,
			expected: protocol.SymbolKindNull,
		},
		{
			symKind:  scip.SymbolInformation_Class,
			expected: protocol.SymbolKindClass,
		},
	}

	for _, test := range tests {
		actual := ScipSymbolKindToDocumentSymbolKind(test.symKind)
		assert.Equal(t, test.expected, actual)
	}
}
