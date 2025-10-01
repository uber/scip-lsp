package partialloader

import (
	"path/filepath"
	"strings"
	"testing"

	scip "github.com/sourcegraph/scip/bindings/go/scip"
	"github.com/stretchr/testify/assert"
	"github.com/uber/scip-lsp/src/scip-lib/model"
)

func TestPartialLoadedIndex(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "index.scip"},
		{name: "invalid.scip"},
		{name: "local_sy.scip"},
		{name: "missing_defs.scip"},
		{name: "nopkg.scip"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			index := NewPartialLoadedIndex("../testdata")
			err := index.LoadIndexFile(filepath.Join("../testdata", test.name))
			assert.NoError(t, err)
		})
	}
}

func TestLoadDocumentNonExistent(t *testing.T) {
	index := NewPartialLoadedIndex("../testdata")
	err := index.LoadIndexFile(filepath.Join("../testdata", "index.scip"))
	assert.NoError(t, err)

	doc, err := index.LoadDocument("src/non_existent.go")
	assert.NoError(t, err)
	assert.Nil(t, doc)
}

func TestLoadDocument(t *testing.T) {
	index := NewPartialLoadedIndex("../testdata")
	err := index.LoadIndexFile(filepath.Join("../testdata", "index.scip"))
	assert.NoError(t, err)

	doc, err := index.LoadDocument("src/code.uber.internal/devexp/test_management/tracing/span.go")
	assert.NoError(t, err)
	assert.NotNil(t, doc)
}

func TestLoadDocumentAlreadyLoaded(t *testing.T) {
	index := NewPartialLoadedIndex("../testdata")
	doc, err := index.LoadDocument("src/code.uber.internal/devexp/test_management/tracing/span.go")
	assert.NoError(t, err)
	assert.NotNil(t, doc)

	doc, err = index.LoadDocument("src/code.uber.internal/devexp/test_management/tracing/span.go")
	assert.NoError(t, err)
	assert.NotNil(t, doc)
}

func TestTidy(t *testing.T) {
	index := NewPartialLoadedIndex("../testdata")
	err := index.LoadIndexFile(filepath.Join("../testdata", "index.scip"))
	assert.NoError(t, err)

	err = index.Tidy()
	assert.NoError(t, err)

	doc, err := index.LoadDocument("src/code.uber.internal/devexp/test_management/tracing/span.go")
	assert.NoError(t, err)
	assert.NotNil(t, doc)
}

func TestGetSymbolInformationFromDescriptors(t *testing.T) {
	tests := []struct {
		name            string
		descriptors     []model.Descriptor
		version         string
		indexFiles      []string
		expectedError   bool
		expectNil       bool
		expectedDocPath string
		expectedVersion string
	}{
		{
			name:          "empty descriptors return error",
			descriptors:   []model.Descriptor{},
			version:       "1.0.0",
			indexFiles:    []string{"index.scip"},
			expectedError: true,
		},
		{
			name: "non-existent symbol returns nil",
			descriptors: []model.Descriptor{
				{
					Name:   "unknown",
					Suffix: scip.Descriptor_Namespace,
				},
			},
			version:    "1.0.0",
			indexFiles: []string{"index.scip"},
			expectNil:  true,
		},
		{
			name: "valid symbol empty version returns nil",
			descriptors: []model.Descriptor{
				{
					Name:   "code.uber.internal/devexp/test_management/tracing",
					Suffix: scip.Descriptor_Namespace,
				},
			},
			version:    "",
			indexFiles: []string{"index.scip"},
			expectNil:  true,
		},
		{
			name: "valid symbol unspecified version returns first version",
			descriptors: []model.Descriptor{
				{
					Name:   "code.uber.internal/devexp/test_management/tracing",
					Suffix: scip.Descriptor_Namespace,
				},
			},
			version:         "unknown-version-1",
			indexFiles:      []string{"index.scip", "index_v1.scip"},
			expectedDocPath: "src/code.uber.internal/devexp/test_management/tracing/span.go",
			expectedVersion: "0f67d80e60274b77875a241c43ef980bc9ffe0d8",
		},
		{
			name: "valid symbol unspecified version returns first version - 2",
			descriptors: []model.Descriptor{
				{
					Name:   "code.uber.internal/devexp/test_management/tracing",
					Suffix: scip.Descriptor_Namespace,
				},
			},
			version:         "unknown-version-2",
			indexFiles:      []string{"index.scip", "index_v1.scip"},
			expectedDocPath: "src/code.uber.internal/devexp/test_management/tracing/span.go",
			expectedVersion: "0f67d80e60274b77875a241c43ef980bc9ffe0d8",
		},
		{
			name: "valid symbol and version returns info",
			descriptors: []model.Descriptor{
				{
					Name:   "code.uber.internal/devexp/test_management/tracing",
					Suffix: scip.Descriptor_Namespace,
				},
			},
			version:         "0f67d80e60274b77875a241c43ef980bc9ffe0d8",
			indexFiles:      []string{"index.scip", "index_v1.scip"},
			expectedDocPath: "src/code.uber.internal/devexp/test_management/tracing/span.go",
			expectedVersion: "0f67d80e60274b77875a241c43ef980bc9ffe0d8",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			index := NewPartialLoadedIndex("../testdata")
			for _, indexFile := range test.indexFiles {
				err := index.LoadIndexFile(filepath.Join("../testdata", indexFile))
				assert.NoError(t, err)
			}

			info, docPath, err := index.GetSymbolInformationFromDescriptors(test.descriptors, test.version)
			if test.expectedError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			if test.expectNil {
				assert.Nil(t, info)
				assert.Empty(t, docPath)
				return
			}

			assert.NotNil(t, info)
			assert.Equal(t, test.expectedDocPath, docPath)
			assert.True(t, strings.Contains(info.Symbol, test.expectedVersion))
		})
	}
}

func TestGetSymbolInformation(t *testing.T) {
	tests := []struct {
		name            string
		symbol          string
		indexFile       string
		expectedError   bool
		expectNil       bool
		expectedDocPath string
	}{
		{
			name:      "local symbol returns nil",
			symbol:    "local 1",
			indexFile: "local_sy.scip",
			expectNil: true,
		},
		{
			name:          "invalid symbol returns error",
			symbol:        "invalid:symbol:format",
			indexFile:     "index.scip",
			expectedError: true,
		},
		{
			name:      "non-existent symbol returns nil",
			symbol:    "scip . . . go/types.Object#",
			indexFile: "index.scip",
			expectNil: true,
		},
		{
			name:            "valid symbol returns info",
			symbol:          "scip-go gomod code.uber.internal/devexp/test_management/tracing 0f67d80e60274b77875a241c43ef980bc9ffe0d8 `code.uber.internal/devexp/test_management/tracing`/",
			indexFile:       "index.scip",
			expectedDocPath: "src/code.uber.internal/devexp/test_management/tracing/span.go",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			index := NewPartialLoadedIndex("../testdata")
			err := index.LoadIndexFile(filepath.Join("../testdata", test.indexFile))
			assert.NoError(t, err)

			info, docPath, err := index.GetSymbolInformation(test.symbol)
			if test.expectedError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			if test.expectNil {
				assert.Nil(t, info)
				assert.Empty(t, docPath)
				return
			}

			assert.NotNil(t, info)
			assert.Equal(t, test.expectedDocPath, docPath)
		})
	}
}

func TestReferences(t *testing.T) {
	tests := []struct {
		name                string
		symbol              string
		indexFile           string
		expectNil           bool
		expectedError       bool
		expectedOccurrences int // minimum number of occurrences expected across all files
	}{
		{
			name:                "local symbol returns nil",
			symbol:              "local 1",
			indexFile:           "local_sy.scip",
			expectNil:           true,
			expectedOccurrences: 0,
		},
		{
			name:                "non-existent symbol returns empty map",
			symbol:              "scip . . . go/types.Object#",
			indexFile:           "index.scip",
			expectNil:           false,
			expectedOccurrences: 0,
		},
		{
			name:                "valid symbol returns occurrences",
			symbol:              "scip-go gomod code.uber.internal/devexp/test_management/tracing 0f67d80e60274b77875a241c43ef980bc9ffe0d8 `code.uber.internal/devexp/test_management/tracing`/",
			indexFile:           "index.scip",
			expectNil:           false,
			expectedOccurrences: 1,
		},
		{
			name:                "valid symbol returns occurrences",
			symbol:              "scip-go gomod github.com/opentracing/opentracing-go 1.2.0 `github.com/opentracing/opentracing-go`/Span#SetTag.",
			indexFile:           "index.scip",
			expectNil:           false,
			expectedOccurrences: 6,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			index := NewPartialLoadedIndex("../testdata")
			err := index.LoadIndexFile(filepath.Join("../testdata", test.indexFile))
			assert.NoError(t, err)

			occurrences, err := index.References(test.symbol)
			if test.expectedError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			if test.expectNil {
				assert.Nil(t, occurrences)
				return
			}

			assert.NotNil(t, occurrences)

			// Count total occurrences across all files
			totalOccurrences := 0
			for _, occs := range occurrences {
				totalOccurrences += len(occs)
			}
			assert.Equal(t, test.expectedOccurrences, totalOccurrences,
				"Expected %d occurrences but got %d",
				test.expectedOccurrences, totalOccurrences)
		})
	}
}

func TestMergeDocTreeNodes(t *testing.T) {
	index := &PartialLoadedIndex{
		DocTreeNodes: make(map[string]*docNodes),
	}

	// Create initial nodes for a document
	existingNode := &SymbolPrefixTreeNode{
		Children: make(map[model.Descriptor]*SymbolPrefixTreeNode),
	}
	docPath := "test/doc.go"
	index.DocTreeNodes[docPath] = &docNodes{
		nodes:    []*SymbolPrefixTreeNode{existingNode},
		revision: 1,
	}

	// Create local nodes to merge
	newNode := &SymbolPrefixTreeNode{
		Children: make(map[model.Descriptor]*SymbolPrefixTreeNode),
	}
	localDocTreeNodes := map[string]*docNodes{
		docPath: {
			nodes:    []*SymbolPrefixTreeNode{newNode},
			revision: 2,
		},
	}

	// Merge the nodes
	index.mergeDocTreeNodes(localDocTreeNodes)

	// Verify the merge
	mergedNodes := index.DocTreeNodes[docPath]
	assert.Equal(t, 2, len(mergedNodes.nodes), "Expected merged nodes to contain both nodes")
	assert.Equal(t, existingNode, mergedNodes.nodes[0], "Expected first node to be the existing node")
	assert.Equal(t, newNode, mergedNodes.nodes[1], "Expected second node to be the new node")
	assert.Equal(t, int64(2), mergedNodes.revision, "Expected revision to be updated to the higher value")
}

func TestSetDocumentLoadedCallback(t *testing.T) {
	index := NewPartialLoadedIndex("../testdata")

	// Create a channel to track callback invocations
	callbackCalled := false

	// Set the callback
	index.SetDocumentLoadedCallback(func(doc *model.Document) {
		callbackCalled = true
	})

	// Load a document that we know exists in the test data
	err := index.LoadIndexFile(filepath.Join("../testdata", "index.scip"))
	assert.NoError(t, err)

	doc, err := index.LoadDocument("src/code.uber.internal/devexp/test_management/tracing/span.go")
	assert.NoError(t, err)
	assert.NotNil(t, doc)

	// Verify the callback was called with the correct document
	assert.True(t, callbackCalled, "Callback should be called")
}

func TestLoadIndexWithPreloadedDocument(t *testing.T) {
	index := NewPartialLoadedIndex("../testdata")

	// First load a document directly
	docPath := "src/code.uber.internal/devexp/test_management/tracing/span.go"
	doc, err := index.LoadDocument(docPath)
	assert.NoError(t, err)
	assert.NotNil(t, doc)

	// Track callback invocations
	callbackCount := 0
	index.SetDocumentLoadedCallback(func(doc *model.Document) {
		callbackCount++
	})

	// Now load an index that contains the same document
	err = index.LoadIndexFile(filepath.Join("../testdata", "index.scip"))
	assert.NoError(t, err)

	// Verify the document is still loaded
	loadedDoc, err := index.LoadDocument(docPath)
	assert.NoError(t, err)
	assert.NotNil(t, loadedDoc)

	// Verify the callback was called for the document in the index
	assert.Equal(t, 1, callbackCount, "Callback should be called once for the document in the index")

	// Verify symbol information is available for the document
	symbol := "scip-go gomod code.uber.internal/devexp/test_management/tracing 0f67d80e60274b77875a241c43ef980bc9ffe0d8 `code.uber.internal/devexp/test_management/tracing`/"
	info, foundDocPath, err := index.GetSymbolInformation(symbol)
	assert.NoError(t, err)
	assert.NotNil(t, info)
	assert.Equal(t, docPath, foundDocPath)
}

// New tests for reverse implementors index
func TestMergeImplementors(t *testing.T) {
	idx := &PartialLoadedIndex{
		ImplementorsBySymbol: make(map[string]map[string]struct{}),
	}
	local := map[string]map[string]struct{}{
		"abs#Symbol": {
			"impl#A": {},
			"impl#B": {},
		},
	}
	idx.mergeImplementors(local)
	set := idx.ImplementorsBySymbol["abs#Symbol"]
	if assert.NotNil(t, set) {
		_, okA := set["impl#A"]
		_, okB := set["impl#B"]
		assert.True(t, okA)
		assert.True(t, okB)
	}
}

func TestGetImplementationSymbols(t *testing.T) {
	idx := &PartialLoadedIndex{
		ImplementorsBySymbol: make(map[string]map[string]struct{}),
	}
	idx.ImplementorsBySymbol["abs#Symbol"] = map[string]struct{}{
		"impl#B": {},
		"impl#A": {},
	}
	list, err := idx.GetImplementationSymbols("abs#Symbol")
	assert.NoError(t, err)
	assert.Equal(t, []string{"impl#A", "impl#B"}, list)

	empty, err := idx.GetImplementationSymbols("unknown#Symbol")
	assert.NoError(t, err)
	assert.Equal(t, 0, len(empty))
}

func TestLoadIndexWithImplementors(t *testing.T) {
	index := NewPartialLoadedIndex("../testdata")
	err := index.LoadIndexFile(filepath.Join("../testdata", "index.scip"))
	assert.NoError(t, err)
	symbol := "scip-go gomod code.uber.internal/devexp/test_management/tracing 0f67d80e60274b77875a241c43ef980bc9ffe0d8 `code.uber.internal/devexp/test_management/tracing`/PartialIndex#"
	implementors, err := index.GetImplementationSymbols(symbol)
	assert.NoError(t, err)
	assert.Equal(t, []string{"scip-go gomod code.uber.internal/devexp/test_management/tracing 0f67d80e60274b77875a241c43ef980bc9ffe0d8 `code.uber.internal/devexp/test_management/tracing`/index#"}, implementors)
}
