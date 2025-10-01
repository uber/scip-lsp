package scip

import (
	"io"
	"strings"
	"testing"

	scipproto "github.com/sourcegraph/scip/bindings/go/scip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uber/scip-lsp/src/scip-lib/model"
	partialloadermock "github.com/uber/scip-lsp/src/scip-lib/partialloader/partial_loader_mock"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

func TestPartialScipRegistry_UnimplementedMethods(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockIndex := partialloadermock.NewMockPartialIndex(ctrl)
	registry := &partialScipRegistry{
		WorkspaceRoot: "/test/workspace",
		Index:         mockIndex,
	}

	t.Run("GetUri returns URI", func(t *testing.T) {
		u := registry.GetURI("test/file.go")
		assert.True(t, strings.HasSuffix(string(u), "test/file.go"))
	})

	t.Run("GetDocumentSymbolForFile returns error", func(t *testing.T) {
		_, err := registry.GetDocumentSymbolForFile(uri.URI("file:///test/file.go"))
		assert.Error(t, err)
	})

	t.Run("GetFileInfo returns error", func(t *testing.T) {
		info := registry.GetFileInfo(uri.URI("file:///test/file.go"))
		assert.Nil(t, info)
	})

	t.Run("GetPackageInfo returns error", func(t *testing.T) {
		pkg := registry.GetPackageInfo("test-package")
		assert.Nil(t, pkg)
	})

	t.Run("GetSymbolForPosition returns error", func(t *testing.T) {
		_, _, err := registry.GetSymbolForPosition(uri.URI("file:///test/file.go"), protocol.Position{Line: 1, Character: 1})
		assert.Error(t, err)
	})

	t.Run("Diagnostics returns error", func(t *testing.T) {
		_, err := registry.Diagnostics(uri.URI("file:///test/file.go"))
		assert.Error(t, err)
		assert.Equal(t, "not implemented", err.Error())
	})

	t.Run("GetSymbolDataForSymbol returns error", func(t *testing.T) {
		_, err := registry.GetSymbolDataForSymbol("test-symbol", nil)
		assert.Error(t, err)
		assert.Equal(t, "not implemented", err.Error())
	})

	t.Run("GetSymbolOccurrence returns error", func(t *testing.T) {
		_, err := registry.GetSymbolOccurrence(uri.URI("file:///test/file.go"), protocol.Position{Line: 1, Character: 1})
		assert.Error(t, err)
		assert.Equal(t, "not implemented", err.Error())
	})

	t.Run("LoadIndex returns error", func(t *testing.T) {
		err := registry.LoadIndex(nil)
		assert.Error(t, err)
	})

	t.Run("LoadConcurrency returns proper value", func(t *testing.T) {
		concurrency := registry.LoadConcurrency()
		assert.NotZero(t, concurrency)
	})
}

// MockReadSeeker is a manual mock for testing the ReadSeeker interface
type MockReadSeeker struct {
	io.Reader
	io.Seeker
}

func TestPartialScipRegistry_LoadIndex(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockIndex := partialloadermock.NewMockPartialIndex(ctrl)
	mockIndex.EXPECT().LoadIndex(gomock.Any(), gomock.Any()).Return(nil)

	registry := &partialScipRegistry{
		WorkspaceRoot: "/test/workspace",
		Index:         mockIndex,
	}

	// Create a simple mock reader (implementation details aren't important for this test)
	mockReader := &MockReadSeeker{}

	// Test that LoadIndex delegates to the Index
	err := registry.LoadIndex(mockReader)

	// Verify the method was called and returned the expected result
	require.NoError(t, err)
}

func TestNewPartialScipRegistry(t *testing.T) {
	registry := NewPartialScipRegistry("/test/workspace", "/test/index", zap.NewNop().Sugar())

	// Type assertion to check the returned interface is of the correct implementation type
	partialRegistry, ok := registry.(*partialScipRegistry)
	require.True(t, ok, "Expected registry to be of type *partialScipRegistry")

	assert.Equal(t, "/test/workspace", partialRegistry.WorkspaceRoot)
	assert.NotNil(t, partialRegistry.Index, "Expected Index to be initialized")
}

func TestPartialScipRegistry_DidOpen(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockIndex := partialloadermock.NewMockPartialIndex(ctrl)
	logger := zaptest.NewLogger(t).Sugar()

	tests := []struct {
		name          string
		uri           uri.URI
		text          string
		setupMock     func(mock *partialloadermock.MockPartialIndex)
		expectedError string
	}{
		{
			name: "successful document load",
			uri:  uri.File("/workspace/test.go"),
			text: "package main",
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				mock.EXPECT().
					LoadDocument("test.go").
					Return(&model.Document{}, nil)
			},
		},
		{
			name: "document not found",
			uri:  uri.File("/workspace/missing.go"),
			text: "package main",
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				mock.EXPECT().
					LoadDocument("missing.go").
					Return(nil, nil)
			},
		},
		{
			name: "error loading document",
			uri:  uri.File("/workspace/error.go"),
			text: "package main",
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				mock.EXPECT().
					LoadDocument("error.go").
					Return(nil, assert.AnError)
			},
			expectedError: assert.AnError.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := &partialScipRegistry{
				WorkspaceRoot: "/workspace",
				Index:         mockIndex,
				logger:        logger,
			}

			tt.setupMock(mockIndex)

			err := registry.DidOpen(tt.uri, tt.text)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestPartialScipRegistry_UriToRelativePath(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	tests := []struct {
		name          string
		workspaceRoot string
		uri           uri.URI
		expected      string
	}{
		{
			name:          "valid relative path",
			workspaceRoot: "/workspace",
			uri:           uri.File("/workspace/src/test.go"),
			expected:      "src/test.go",
		},
		{
			name:          "uri outside workspace",
			workspaceRoot: "/workspace",
			uri:           uri.File("/other/test.go"),
			expected:      "../other/test.go",
		},
		{
			name:          "same directory",
			workspaceRoot: "/workspace",
			uri:           uri.File("/workspace/test.go"),
			expected:      "test.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := &partialScipRegistry{
				WorkspaceRoot: tt.workspaceRoot,
				logger:        logger,
			}

			result := registry.uriToRelativePath(tt.uri)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPartialScipRegistry_LoadIndexFile(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockIndex := partialloadermock.NewMockPartialIndex(ctrl)
	logger := zaptest.NewLogger(t).Sugar()

	tests := []struct {
		name          string
		indexPath     string
		setupMock     func(mock *partialloadermock.MockPartialIndex)
		expectedError string
	}{
		{
			name:      "successful index load",
			indexPath: "/test/index.scip",
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				mock.EXPECT().
					LoadIndexFile("/test/index.scip").
					Return(nil)
			},
		},
		{
			name:      "error loading index",
			indexPath: "/test/error.scip",
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				mock.EXPECT().
					LoadIndexFile("/test/error.scip").
					Return(assert.AnError)
			},
			expectedError: assert.AnError.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := &partialScipRegistry{
				WorkspaceRoot: "/workspace",
				Index:         mockIndex,
				logger:        logger,
			}

			tt.setupMock(mockIndex)

			err := registry.LoadIndexFile(tt.indexPath)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestPartialScipRegistry_SetDocumentLoadedCallback(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockIndex := partialloadermock.NewMockPartialIndex(ctrl)
	logger := zaptest.NewLogger(t).Sugar()

	registry := &partialScipRegistry{
		WorkspaceRoot: "/workspace",
		Index:         mockIndex,
		logger:        logger,
	}

	// Create a test callback function
	testCallback := func(*model.Document) {}

	// Expect the callback to be set on the mock index
	mockIndex.EXPECT().SetDocumentLoadedCallback(gomock.Any())

	// Call the method
	registry.SetDocumentLoadedCallback(testCallback)
}

func TestPartialScipRegistry_GetSymbolDefinitionOccurrence(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockIndex := partialloadermock.NewMockPartialIndex(ctrl)
	logger := zaptest.NewLogger(t).Sugar()

	tests := []struct {
		name           string
		descriptors    []model.Descriptor
		expectedResult *model.SymbolOccurrence
		setupMock      func(mock *partialloadermock.MockPartialIndex)
		wantError      bool
	}{
		{
			name:        "empty descriptors",
			descriptors: []model.Descriptor{},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				mock.EXPECT().
					GetSymbolInformationFromDescriptors([]model.Descriptor{}, gomock.Any()).
					Return(nil, "", nil)
			},
			expectedResult: nil,
			wantError:      false,
		},
		{
			name:        "symbol information error",
			descriptors: []model.Descriptor{},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				mock.EXPECT().
					GetSymbolInformationFromDescriptors([]model.Descriptor{}, gomock.Any()).
					Return(nil, "", assert.AnError)
			},
			expectedResult: nil,
			wantError:      true,
		},
		{
			name: "valid match with occurrence",
			descriptors: []model.Descriptor{
				{
					Name:   "sample",
					Suffix: scipproto.Descriptor_Namespace,
				},
				{
					Name:   "Test",
					Suffix: scipproto.Descriptor_Type,
				},
			},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				mock.EXPECT().
					GetSymbolInformationFromDescriptors([]model.Descriptor{
						{
							Name:   "sample",
							Suffix: scipproto.Descriptor_Namespace,
						},
						{
							Name:   "Test",
							Suffix: scipproto.Descriptor_Type,
						},
					}, gomock.Any()).
					Return(&model.SymbolInformation{
						Symbol: "test",
					}, "path/to/doc.java", nil)
				mock.EXPECT().
					LoadDocument("path/to/doc.java").
					Return(&model.Document{
						Occurrences: []*model.Occurrence{
							{
								Symbol:      "other symbol",
								Range:       []int32{10, 3, 5},
								SymbolRoles: int32(scipproto.SymbolRole_Definition),
							},
							{
								Symbol:      "test",
								Range:       []int32{1, 1, 1, 2},
								SymbolRoles: int32(scipproto.SymbolRole_Definition),
							},
							{
								Symbol:      "test",
								Range:       []int32{13, 3, 5},
								SymbolRoles: int32(scipproto.SymbolRole_ReadAccess),
							},
						},
					}, nil)
			},
			expectedResult: &model.SymbolOccurrence{
				Location: "file:///workspace/root/path/to/doc.java",
				Info: &model.SymbolInformation{
					Symbol: "test",
				},
				Occurrence: &model.Occurrence{
					Symbol:      "test",
					Range:       []int32{1, 1, 1, 2},
					SymbolRoles: int32(scipproto.SymbolRole_Definition),
				},
			},
			wantError: false,
		},
		{
			name: "valid match with missing occurrence",
			descriptors: []model.Descriptor{
				{
					Name:   "sample",
					Suffix: scipproto.Descriptor_Namespace,
				},
				{
					Name:   "test",
					Suffix: scipproto.Descriptor_Type,
				},
			},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				mock.EXPECT().
					GetSymbolInformationFromDescriptors([]model.Descriptor{
						{
							Name:   "sample",
							Suffix: scipproto.Descriptor_Namespace,
						},
						{
							Name:   "test",
							Suffix: scipproto.Descriptor_Type,
						},
					}, gomock.Any()).
					Return(&model.SymbolInformation{
						Symbol: "test",
					}, "path/to/doc.java", nil)
				mock.EXPECT().
					LoadDocument("path/to/doc.java").
					Return(&model.Document{
						Occurrences: []*model.Occurrence{
							{
								Symbol:      "other symbol",
								Range:       []int32{10, 3, 5},
								SymbolRoles: int32(scipproto.SymbolRole_Definition),
							},
							{
								Symbol:      "test",
								Range:       []int32{13, 3, 5},
								SymbolRoles: int32(scipproto.SymbolRole_ReadAccess),
							},
						},
					}, nil)
			},
			expectedResult: &model.SymbolOccurrence{
				Location: "file:///workspace/root/path/to/doc.java",
				Info: &model.SymbolInformation{
					Symbol: "test",
				},
				Occurrence: nil,
			},
			wantError: false,
		},
		{
			name: "load document error",
			descriptors: []model.Descriptor{
				{
					Name:   "sample",
					Suffix: scipproto.Descriptor_Namespace,
				},
				{
					Name:   "test",
					Suffix: scipproto.Descriptor_Type,
				},
			},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				mock.EXPECT().
					GetSymbolInformationFromDescriptors([]model.Descriptor{
						{
							Name:   "sample",
							Suffix: scipproto.Descriptor_Namespace,
						},
						{
							Name:   "test",
							Suffix: scipproto.Descriptor_Type,
						},
					}, gomock.Any()).
					Return(&model.SymbolInformation{
						Symbol: "test",
					}, "/path/to/doc.java", nil)
				mock.EXPECT().
					LoadDocument("/path/to/doc.java").
					Return(nil, assert.AnError)
			},
			expectedResult: nil,
			wantError:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := &partialScipRegistry{
				WorkspaceRoot: "/workspace/root",
				Index:         mockIndex,
				logger:        logger,
			}

			tt.setupMock(mockIndex)

			result, err := registry.GetSymbolDefinitionOccurrence(tt.descriptors, ".")

			if tt.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedResult, result)
			}
		})
	}
}

func TestPartialScipRegistry_Definition(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockIndex := partialloadermock.NewMockPartialIndex(ctrl)
	logger := zaptest.NewLogger(t).Sugar()

	tests := []struct {
		name           string
		sourceURI      uri.URI
		pos            protocol.Position
		setupMock      func(mock *partialloadermock.MockPartialIndex)
		expectedSource *model.SymbolOccurrence
		expectedDef    *model.SymbolOccurrence
		expectedError  string
	}{
		{
			name:      "document load error",
			sourceURI: uri.File("/workspace/test.go"),
			pos:       protocol.Position{Line: 1, Character: 1},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				mock.EXPECT().
					LoadDocument("test.go").
					Return(nil, assert.AnError)
			},
			expectedError: assert.AnError.Error(),
		},
		{
			name:      "document not found",
			sourceURI: uri.File("/workspace/test.go"),
			pos:       protocol.Position{Line: 1, Character: 1},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				mock.EXPECT().
					LoadDocument("test.go").
					Return(nil, nil)
			},
		},
		{
			name:      "no occurrence at position",
			sourceURI: uri.File("/workspace/test.go"),
			pos:       protocol.Position{Line: 1, Character: 1},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				mock.EXPECT().
					LoadDocument("test.go").
					Return(&model.Document{
						Occurrences: []*model.Occurrence{},
					}, nil)
			},
		},
		{
			name:      "local symbol with definition",
			sourceURI: uri.File("/workspace/test.go"),
			pos:       protocol.Position{Line: 1, Character: 1},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				sourceOcc := &model.Occurrence{
					Symbol: "local 1",
					Range:  []int32{1, 1, 1, 2}, // start_line, start_char, end_line, end_char
				}
				defOcc := &model.Occurrence{
					Symbol:      "local 1",
					Range:       []int32{0, 0, 0, 1},
					SymbolRoles: int32(scipproto.SymbolRole_Definition),
				}
				symInfo := &model.SymbolInformation{
					Symbol: "local 1",
				}
				mock.EXPECT().
					LoadDocument("test.go").
					Return(&model.Document{
						Occurrences: []*model.Occurrence{sourceOcc, defOcc},
						Symbols:     []*model.SymbolInformation{symInfo},
					}, nil)
			},
			expectedSource: &model.SymbolOccurrence{
				Info: &model.SymbolInformation{Symbol: "local 1"},
				Occurrence: &model.Occurrence{
					Symbol: "local 1",
					Range:  []int32{1, 1, 1, 2},
				},
				Location: uri.File("/workspace/test.go"),
			},
			expectedDef: &model.SymbolOccurrence{
				Info: &model.SymbolInformation{Symbol: "local 1"},
				Occurrence: &model.Occurrence{
					Symbol:      "local 1",
					Range:       []int32{0, 0, 0, 1},
					SymbolRoles: int32(scipproto.SymbolRole_Definition),
				},
				Location: uri.File("/workspace/test.go"),
			},
		},
		{
			name:      "global symbol with definition",
			sourceURI: uri.File("/workspace/test.go"),
			pos:       protocol.Position{Line: 1, Character: 1},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				sourceOcc := &model.Occurrence{
					Symbol: tracingUUIDKey,
					Range:  []int32{1, 1, 1, 2},
				}
				defOcc := &model.Occurrence{
					Symbol:      tracingUUIDKey,
					Range:       []int32{0, 0, 0, 1},
					SymbolRoles: int32(scipproto.SymbolRole_Definition),
				}
				symInfo := &model.SymbolInformation{
					Symbol: tracingUUIDKey,
				}
				mock.EXPECT().
					LoadDocument("test.go").
					Return(&model.Document{
						Occurrences: []*model.Occurrence{sourceOcc},
					}, nil)
				mock.EXPECT().
					GetSymbolInformationFromDescriptors([]model.Descriptor{
						{
							Name:   "code.uber.internal/devexp/test_management/tracing",
							Suffix: scipproto.Descriptor_Namespace,
						},
						{
							Name:   "PipelineUUIDTagKey",
							Suffix: scipproto.Descriptor_Term,
						},
					}, gomock.Any()).
					Return(symInfo, "def.go", nil)
				mock.EXPECT().
					LoadDocument("def.go").
					Return(&model.Document{
						Occurrences: []*model.Occurrence{defOcc},
					}, nil)
			},
			expectedSource: &model.SymbolOccurrence{
				Info: &model.SymbolInformation{Symbol: tracingUUIDKey},
				Occurrence: &model.Occurrence{
					Symbol: tracingUUIDKey,
					Range:  []int32{1, 1, 1, 2},
				},
				Location: uri.File("/workspace/test.go"),
			},
			expectedDef: &model.SymbolOccurrence{
				Info: &model.SymbolInformation{Symbol: tracingUUIDKey},
				Occurrence: &model.Occurrence{
					Symbol:      tracingUUIDKey,
					Range:       []int32{0, 0, 0, 1},
					SymbolRoles: int32(scipproto.SymbolRole_Definition),
				},
				Location: uri.File("/workspace/def.go"),
			},
		},
		{
			name:      "global symbol info not found",
			sourceURI: uri.File("/workspace/test.go"),
			pos:       protocol.Position{Line: 1, Character: 1},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				sourceOcc := &model.Occurrence{
					Symbol: tracingUUIDKey,
					Range:  []int32{1, 1, 1, 2},
				}
				mock.EXPECT().
					LoadDocument("test.go").
					Return(&model.Document{
						Occurrences: []*model.Occurrence{sourceOcc},
					}, nil)
				mock.EXPECT().
					GetSymbolInformationFromDescriptors([]model.Descriptor{
						{
							Name:   "code.uber.internal/devexp/test_management/tracing",
							Suffix: scipproto.Descriptor_Namespace,
						},
						{
							Name:   "PipelineUUIDTagKey",
							Suffix: scipproto.Descriptor_Term,
						},
					}, gomock.Any()).
					Return(nil, "def.go", nil)
			},
		},
		{
			name:      "error getting symbol information",
			sourceURI: uri.File("/workspace/test.go"),
			pos:       protocol.Position{Line: 1, Character: 1},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				sourceOcc := &model.Occurrence{
					Symbol: tracingUUIDKey,
					Range:  []int32{1, 1, 1, 2},
				}
				mock.EXPECT().
					LoadDocument("test.go").
					Return(&model.Document{
						Occurrences: []*model.Occurrence{sourceOcc},
					}, nil)
				mock.EXPECT().
					GetSymbolInformationFromDescriptors([]model.Descriptor{
						{
							Name:   "code.uber.internal/devexp/test_management/tracing",
							Suffix: scipproto.Descriptor_Namespace,
						},
						{
							Name:   "PipelineUUIDTagKey",
							Suffix: scipproto.Descriptor_Term,
						},
					}, gomock.Any()).
					Return(nil, "def.go", assert.AnError)
			},
			expectedError: assert.AnError.Error(),
		},
		{
			name:      "error loading definition document",
			sourceURI: uri.File("/workspace/test.go"),
			pos:       protocol.Position{Line: 1, Character: 1},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				sourceOcc := &model.Occurrence{
					Symbol: tracingUUIDKey,
					Range:  []int32{1, 1, 1, 2},
				}
				symInfo := &model.SymbolInformation{
					Symbol: tracingUUIDKey,
				}
				mock.EXPECT().
					LoadDocument("test.go").
					Return(&model.Document{
						Occurrences: []*model.Occurrence{sourceOcc},
					}, nil)
				mock.EXPECT().
					GetSymbolInformationFromDescriptors([]model.Descriptor{
						{
							Name:   "code.uber.internal/devexp/test_management/tracing",
							Suffix: scipproto.Descriptor_Namespace,
						},
						{
							Name:   "PipelineUUIDTagKey",
							Suffix: scipproto.Descriptor_Term,
						},
					}, gomock.Any()).
					Return(symInfo, "def.go", nil)
				mock.EXPECT().
					LoadDocument("def.go").
					Return(nil, assert.AnError)
			},
			expectedError: assert.AnError.Error(),
		},
		{
			name:      "definition document not found",
			sourceURI: uri.File("/workspace/test.go"),
			pos:       protocol.Position{Line: 1, Character: 1},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				sourceOcc := &model.Occurrence{
					Symbol: tracingUUIDKey,
					Range:  []int32{1, 1, 1, 2},
				}
				symInfo := &model.SymbolInformation{
					Symbol: tracingUUIDKey,
				}
				mock.EXPECT().
					LoadDocument("test.go").
					Return(&model.Document{
						Occurrences: []*model.Occurrence{sourceOcc},
					}, nil)
				mock.EXPECT().
					GetSymbolInformationFromDescriptors([]model.Descriptor{
						{
							Name:   "code.uber.internal/devexp/test_management/tracing",
							Suffix: scipproto.Descriptor_Namespace,
						},
						{
							Name:   "PipelineUUIDTagKey",
							Suffix: scipproto.Descriptor_Term,
						},
					}, gomock.Any()).
					Return(symInfo, "def.go", nil)
				mock.EXPECT().
					LoadDocument("def.go").
					Return(nil, nil)
			},
		},
		{
			name:      "symbol parse error",
			sourceURI: uri.File("/workspace/test.go"),
			pos:       protocol.Position{Line: 1, Character: 1},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				sourceOcc := &model.Occurrence{
					Symbol: "invalid symbol",
					Range:  []int32{1, 1, 1, 2},
				}
				mock.EXPECT().
					LoadDocument("test.go").
					Return(&model.Document{
						Occurrences: []*model.Occurrence{sourceOcc},
					}, nil)
			},
			expectedError: "reached end of symbol while parsing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := &partialScipRegistry{
				WorkspaceRoot: "/workspace",
				Index:         mockIndex,
				logger:        logger,
			}

			tt.setupMock(mockIndex)

			sourceSymOcc, defSymOcc, err := registry.Definition(tt.sourceURI, tt.pos)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				return
			}

			require.NoError(t, err)

			if tt.expectedSource == nil {
				assert.Nil(t, sourceSymOcc)
			} else {
				require.NotNil(t, sourceSymOcc)
				assert.Equal(t, tt.expectedSource.Info, sourceSymOcc.Info)
				assert.Equal(t, tt.expectedSource.Occurrence, sourceSymOcc.Occurrence)
				assert.Equal(t, tt.expectedSource.Location, sourceSymOcc.Location)
			}

			if tt.expectedDef == nil {
				assert.Nil(t, defSymOcc)
			} else {
				require.NotNil(t, defSymOcc)
				assert.Equal(t, tt.expectedDef.Info, defSymOcc.Info)
				assert.Equal(t, tt.expectedDef.Occurrence, defSymOcc.Occurrence)
				assert.Equal(t, tt.expectedDef.Location, defSymOcc.Location)
			}
		})
	}
}

// locationEqual compares two protocol.Location objects for equality
func locationEqual(a, b protocol.Location) bool {
	return a.URI == b.URI &&
		a.Range.Start.Line == b.Range.Start.Line &&
		a.Range.Start.Character == b.Range.Start.Character &&
		a.Range.End.Line == b.Range.End.Line &&
		a.Range.End.Character == b.Range.End.Character
}

// containsLocation checks if a location exists in a slice of locations
func containsLocation(locations []protocol.Location, target protocol.Location) bool {
	for _, loc := range locations {
		if locationEqual(loc, target) {
			return true
		}
	}
	return false
}

// locationsEqual compares two slices of locations without considering order
func locationsEqual(a, b []protocol.Location) bool {
	if len(a) != len(b) {
		return false
	}
	for _, loc := range a {
		if !containsLocation(b, loc) {
			return false
		}
	}
	return true
}

func TestPartialScipRegistry_References(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockIndex := partialloadermock.NewMockPartialIndex(ctrl)
	logger := zaptest.NewLogger(t).Sugar()

	tests := []struct {
		name          string
		sourceURI     uri.URI
		pos           protocol.Position
		setupMock     func(mock *partialloadermock.MockPartialIndex)
		expectedLocs  []protocol.Location
		expectedError string
	}{
		{
			name:      "document load error",
			sourceURI: uri.File("/workspace/test.go"),
			pos:       protocol.Position{Line: 1, Character: 1},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				mock.EXPECT().
					LoadDocument("test.go").
					Return(nil, assert.AnError)
			},
			expectedError: assert.AnError.Error(),
		},
		{
			name:      "document not found",
			sourceURI: uri.File("/workspace/test.go"),
			pos:       protocol.Position{Line: 1, Character: 1},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				mock.EXPECT().
					LoadDocument("test.go").
					Return(nil, nil)
			},
			expectedLocs: nil,
		},
		{
			name:      "no occurrence at position",
			sourceURI: uri.File("/workspace/test.go"),
			pos:       protocol.Position{Line: 1, Character: 1},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				mock.EXPECT().
					LoadDocument("test.go").
					Return(&model.Document{
						Occurrences: []*model.Occurrence{},
					}, nil)
			},
			expectedLocs: nil,
		},
		{
			name:      "local symbol references",
			sourceURI: uri.File("/workspace/test.go"),
			pos:       protocol.Position{Line: 1, Character: 1},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				sourceOcc := &model.Occurrence{
					Symbol: "local 1",
					Range:  []int32{1, 1, 1, 2},
				}
				refOcc := &model.Occurrence{
					Symbol: "local 1",
					Range:  []int32{2, 1, 2, 2},
				}
				mock.EXPECT().
					LoadDocument("test.go").
					Return(&model.Document{
						Occurrences: []*model.Occurrence{sourceOcc, refOcc},
					}, nil)
			},
			expectedLocs: []protocol.Location{
				{
					URI: uri.File("/workspace/test.go"),
					Range: protocol.Range{
						Start: protocol.Position{Line: 1, Character: 1},
						End:   protocol.Position{Line: 1, Character: 2},
					},
				},
				{
					URI: uri.File("/workspace/test.go"),
					Range: protocol.Range{
						Start: protocol.Position{Line: 2, Character: 1},
						End:   protocol.Position{Line: 2, Character: 2},
					},
				},
			},
		},
		{
			name:      "global symbol references",
			sourceURI: uri.File("/workspace/test.go"),
			pos:       protocol.Position{Line: 1, Character: 1},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				sourceOcc := &model.Occurrence{
					Symbol: tracingUUIDKey,
					Range:  []int32{1, 1, 1, 2},
				}
				mock.EXPECT().
					LoadDocument("test.go").
					Return(&model.Document{
						Occurrences: []*model.Occurrence{sourceOcc},
					}, nil)

				// References in multiple files
				refs := map[string][]*model.Occurrence{
					"test.go": {
						{
							Symbol: tracingUUIDKey,
							Range:  []int32{1, 1, 1, 2},
						},
					},
					"other.go": {
						{
							Symbol: tracingUUIDKey,
							Range:  []int32{5, 1, 5, 2},
						},
					},
				}
				mock.EXPECT().
					References(tracingUUIDKey).
					Return(refs, nil)
			},
			expectedLocs: []protocol.Location{
				{
					URI: uri.File("/workspace/test.go"),
					Range: protocol.Range{
						Start: protocol.Position{Line: 1, Character: 1},
						End:   protocol.Position{Line: 1, Character: 2},
					},
				},
				{
					URI: uri.File("/workspace/other.go"),
					Range: protocol.Range{
						Start: protocol.Position{Line: 5, Character: 1},
						End:   protocol.Position{Line: 5, Character: 2},
					},
				},
			},
		},
		{
			name:      "global symbol references error",
			sourceURI: uri.File("/workspace/test.go"),
			pos:       protocol.Position{Line: 1, Character: 1},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				sourceOcc := &model.Occurrence{
					Symbol: tracingUUIDKey,
					Range:  []int32{1, 1, 1, 2},
				}
				mock.EXPECT().
					LoadDocument("test.go").
					Return(&model.Document{
						Occurrences: []*model.Occurrence{sourceOcc},
					}, nil)
				mock.EXPECT().
					References(tracingUUIDKey).
					Return(nil, assert.AnError)
			},
			expectedError: assert.AnError.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := &partialScipRegistry{
				WorkspaceRoot: "/workspace",
				Index:         mockIndex,
				logger:        logger,
			}

			tt.setupMock(mockIndex)

			locations, err := registry.References(tt.sourceURI, tt.pos)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				return
			}

			require.NoError(t, err)
			if tt.expectedLocs == nil {
				assert.Nil(t, locations)
			} else {
				require.Equal(t, len(tt.expectedLocs), len(locations))
				assert.True(t, locationsEqual(locations, tt.expectedLocs),
					"Locations don't match expected locations (ignoring order)")
			}
		})
	}
}

func TestPartialScipRegistry_Hover(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockIndex := partialloadermock.NewMockPartialIndex(ctrl)
	logger := zaptest.NewLogger(t).Sugar()

	tests := []struct {
		name          string
		sourceURI     uri.URI
		pos           protocol.Position
		setupMock     func(mock *partialloadermock.MockPartialIndex)
		expectedDocs  string
		expectedOcc   *model.Occurrence
		expectedError string
	}{
		{
			name:      "document load error",
			sourceURI: uri.File("/workspace/test.go"),
			pos:       protocol.Position{Line: 1, Character: 1},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				mock.EXPECT().
					LoadDocument("test.go").
					Return(nil, assert.AnError)
			},
			expectedError: assert.AnError.Error(),
		},
		{
			name:      "document not found",
			sourceURI: uri.File("/workspace/test.go"),
			pos:       protocol.Position{Line: 1, Character: 1},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				mock.EXPECT().
					LoadDocument("test.go").
					Return(nil, nil)
			},
		},
		{
			name:      "no occurrence at position",
			sourceURI: uri.File("/workspace/test.go"),
			pos:       protocol.Position{Line: 1, Character: 1},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				mock.EXPECT().
					LoadDocument("test.go").
					Return(&model.Document{
						Occurrences: []*model.Occurrence{},
					}, nil)
			},
		},
		{
			name:      "symbol information error",
			sourceURI: uri.File("/workspace/test.go"),
			pos:       protocol.Position{Line: 1, Character: 1},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				occ := &model.Occurrence{
					Symbol: "test",
					Range:  []int32{1, 1, 1, 2},
				}
				mock.EXPECT().
					LoadDocument("test.go").
					Return(&model.Document{
						Occurrences: []*model.Occurrence{occ},
					}, nil)
				mock.EXPECT().
					GetSymbolInformation("test").
					Return(nil, "", assert.AnError)
			},
			expectedError: assert.AnError.Error(),
		},
		{
			name:      "override documentation",
			sourceURI: uri.File("/workspace/test.go"),
			pos:       protocol.Position{Line: 1, Character: 1},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				occ := &model.Occurrence{
					Symbol:                "test",
					Range:                 []int32{1, 1, 1, 2},
					OverrideDocumentation: []string{"Override doc line 1", "Override doc line 2"},
				}
				mock.EXPECT().
					LoadDocument("test.go").
					Return(&model.Document{
						Occurrences: []*model.Occurrence{occ},
					}, nil)
				mock.EXPECT().
					GetSymbolInformation("test").
					Return(&model.SymbolInformation{
						Symbol:        "test",
						Documentation: []string{"Symbol doc line 1", "Symbol doc line 2"},
					}, "", nil)
			},
			expectedDocs: "Override doc line 1\nOverride doc line 2",
			expectedOcc: &model.Occurrence{
				Symbol:                "test",
				Range:                 []int32{1, 1, 1, 2},
				OverrideDocumentation: []string{"Override doc line 1", "Override doc line 2"},
			},
		},
		{
			name:      "symbol documentation",
			sourceURI: uri.File("/workspace/test.go"),
			pos:       protocol.Position{Line: 1, Character: 1},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				occ := &model.Occurrence{
					Symbol: "test",
					Range:  []int32{1, 1, 1, 2},
				}
				mock.EXPECT().
					LoadDocument("test.go").
					Return(&model.Document{
						Occurrences: []*model.Occurrence{occ},
					}, nil)
				mock.EXPECT().
					GetSymbolInformation("test").
					Return(&model.SymbolInformation{
						Symbol:        "test",
						Documentation: []string{"Symbol doc line 1", "Symbol doc line 2"},
					}, "", nil)
			},
			expectedDocs: "Symbol doc line 1\nSymbol doc line 2",
			expectedOcc: &model.Occurrence{
				Symbol: "test",
				Range:  []int32{1, 1, 1, 2},
			},
		},
		{
			name:      "signature documentation",
			sourceURI: uri.File("/workspace/test.go"),
			pos:       protocol.Position{Line: 1, Character: 1},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				occ := &model.Occurrence{
					Symbol: "test",
					Range:  []int32{1, 1, 1, 2},
				}
				mock.EXPECT().
					LoadDocument("test.go").
					Return(&model.Document{
						Occurrences: []*model.Occurrence{occ},
					}, nil)
				mock.EXPECT().
					GetSymbolInformation("test").
					Return(&model.SymbolInformation{
						Symbol: "test",
						SignatureDocumentation: &model.Document{
							Text:     "Signature documentation",
							Language: "go",
						},
					}, "", nil)
			},
			expectedDocs: "Signature documentation",
			expectedOcc: &model.Occurrence{
				Symbol: "test",
				Range:  []int32{1, 1, 1, 2},
			},
		},
		{
			name:      "no documentation",
			sourceURI: uri.File("/workspace/test.go"),
			pos:       protocol.Position{Line: 1, Character: 1},
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				occ := &model.Occurrence{
					Symbol: "test",
					Range:  []int32{1, 1, 1, 2},
				}
				mock.EXPECT().
					LoadDocument("test.go").
					Return(&model.Document{
						Occurrences: []*model.Occurrence{occ},
					}, nil)
				mock.EXPECT().
					GetSymbolInformation("test").
					Return(&model.SymbolInformation{
						Symbol: "test",
					}, "", nil)
			},
			expectedDocs: "",
			expectedOcc: &model.Occurrence{
				Symbol: "test",
				Range:  []int32{1, 1, 1, 2},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := &partialScipRegistry{
				WorkspaceRoot: "/workspace",
				Index:         mockIndex,
				logger:        logger,
			}

			tt.setupMock(mockIndex)

			docs, occ, err := registry.Hover(tt.sourceURI, tt.pos)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedDocs, docs)

			if tt.expectedOcc == nil {
				assert.Nil(t, occ)
			} else {
				require.NotNil(t, occ)
				assert.Equal(t, tt.expectedOcc.Symbol, occ.Symbol)
				assert.Equal(t, tt.expectedOcc.Range, occ.Range)
				assert.Equal(t, tt.expectedOcc.OverrideDocumentation, occ.OverrideDocumentation)
			}
		})
	}
}

func TestPartialScipRegistry_DocumentSymbols(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockIndex := partialloadermock.NewMockPartialIndex(ctrl)
	logger := zaptest.NewLogger(t).Sugar()

	tests := []struct {
		name            string
		sourceURI       uri.URI
		setupMock       func(mock *partialloadermock.MockPartialIndex)
		expectedSymbols []*model.SymbolOccurrence
		expectedError   string
	}{
		{
			name:      "document load error",
			sourceURI: uri.File("/workspace/test.go"),
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				mock.EXPECT().
					LoadDocument("test.go").
					Return(nil, assert.AnError)
			},
			expectedError: assert.AnError.Error(),
		},
		{
			name:      "document not found",
			sourceURI: uri.File("/workspace/test.go"),
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				mock.EXPECT().
					LoadDocument("test.go").
					Return(nil, nil)
			},
			expectedSymbols: nil,
		},
		{
			name:      "empty document",
			sourceURI: uri.File("/workspace/test.go"),
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				mock.EXPECT().
					LoadDocument("test.go").
					Return(&model.Document{
						Occurrences: []*model.Occurrence{},
						SymbolMap:   map[string]*model.SymbolInformation{},
					}, nil)
			},
			expectedSymbols: []*model.SymbolOccurrence{},
		},
		{
			name:      "local symbols only",
			sourceURI: uri.File("/workspace/test.go"),
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				mock.EXPECT().
					LoadDocument("test.go").
					Return(&model.Document{
						Occurrences: []*model.Occurrence{
							{
								Symbol:      "local 1",
								Range:       []int32{1, 1, 1, 2},
								SymbolRoles: int32(scipproto.SymbolRole_Definition),
							},
						},
						SymbolMap: map[string]*model.SymbolInformation{
							"local 1": {
								Symbol:      "local 1",
								DisplayName: "LocalVar",
							},
						},
					}, nil)
			},
			expectedSymbols: []*model.SymbolOccurrence{},
		},
		{
			name:      "global symbols with definitions",
			sourceURI: uri.File("/workspace/test.go"),
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				mock.EXPECT().
					LoadDocument("test.go").
					Return(&model.Document{
						Occurrences: []*model.Occurrence{
							{
								Symbol:      tracingUUIDKey,
								Range:       []int32{1, 1, 1, 10},
								SymbolRoles: int32(scipproto.SymbolRole_Definition),
							},
							{
								Symbol:      "global2",
								Range:       []int32{2, 1, 2, 10},
								SymbolRoles: int32(scipproto.SymbolRole_Definition),
							},
							{
								Symbol: "global3",
								Range:  []int32{3, 1, 3, 10},
								// Not a definition
							},
						},
						SymbolMap: map[string]*model.SymbolInformation{
							tracingUUIDKey: {
								Symbol:      tracingUUIDKey,
								DisplayName: "TracingUUID",
							},
							"global2": {
								Symbol:      "global2",
								DisplayName: "Global2",
							},
							"global3": {
								Symbol:      "global3",
								DisplayName: "Global3",
							},
						},
					}, nil)
			},
			expectedSymbols: []*model.SymbolOccurrence{
				{
					Info: &model.SymbolInformation{
						Symbol:      tracingUUIDKey,
						DisplayName: "TracingUUID",
					},
					Occurrence: &model.Occurrence{
						Symbol:      tracingUUIDKey,
						Range:       []int32{1, 1, 1, 10},
						SymbolRoles: int32(scipproto.SymbolRole_Definition),
					},
					Location: uri.File("/workspace/test.go"),
				},
				{
					Info: &model.SymbolInformation{
						Symbol:      "global2",
						DisplayName: "Global2",
					},
					Occurrence: &model.Occurrence{
						Symbol:      "global2",
						Range:       []int32{2, 1, 2, 10},
						SymbolRoles: int32(scipproto.SymbolRole_Definition),
					},
					Location: uri.File("/workspace/test.go"),
				},
			},
		},
		{
			name:      "global symbols with empty display name",
			sourceURI: uri.File("/workspace/test.go"),
			setupMock: func(mock *partialloadermock.MockPartialIndex) {
				mock.EXPECT().
					LoadDocument("test.go").
					Return(&model.Document{
						Occurrences: []*model.Occurrence{
							{
								Symbol:      tracingUUIDKey,
								Range:       []int32{1, 1, 1, 10},
								SymbolRoles: int32(scipproto.SymbolRole_Definition),
							},
						},
						SymbolMap: map[string]*model.SymbolInformation{
							tracingUUIDKey: {
								Symbol: tracingUUIDKey,
								// DisplayName is empty, should be parsed from symbol
							},
						},
					}, nil)
			},
			expectedSymbols: []*model.SymbolOccurrence{
				{
					Info: &model.SymbolInformation{
						Symbol:      tracingUUIDKey,
						DisplayName: "PipelineUUIDTagKey",
					},
					Occurrence: &model.Occurrence{
						Symbol:      tracingUUIDKey,
						Range:       []int32{1, 1, 1, 10},
						SymbolRoles: int32(scipproto.SymbolRole_Definition),
					},
					Location: uri.File("/workspace/test.go"),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := &partialScipRegistry{
				WorkspaceRoot: "/workspace",
				Index:         mockIndex,
				logger:        logger,
			}

			tt.setupMock(mockIndex)

			symbols, err := registry.DocumentSymbols(tt.sourceURI)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				return
			}

			require.NoError(t, err)
			if tt.expectedSymbols == nil {
				assert.Nil(t, symbols)
			} else {
				assert.ElementsMatch(t, tt.expectedSymbols, symbols)
			}
		})
	}
}

func TestPartialScipRegistry_Implementation_FastPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockIndex := partialloadermock.NewMockPartialIndex(ctrl)
	logger := zaptest.NewLogger(t).Sugar()

	registry := &partialScipRegistry{
		WorkspaceRoot: "/workspace",
		Index:         mockIndex,
		logger:        logger,
	}

	sourceURI := uri.File("/workspace/test.go")
	pos := protocol.Position{Line: 1, Character: 1}

	// Source occurrence at position
	sourceOcc := &model.Occurrence{Symbol: tracingUUIDKey, Range: []int32{1, 1, 1, 2}}
	mockIndex.EXPECT().LoadDocument("test.go").Return(&model.Document{
		Occurrences: []*model.Occurrence{sourceOcc},
	}, nil)

	// Fast path implementors from reverse index
	mockIndex.EXPECT().GetImplementationSymbols(tracingUUIDKey).Return([]string{
		"scip-go gomod example/pkg v1.0.0 `example/pkg`/Foo#Bar.",
	}, nil)

	// Resolve implementor to definition occurrence
	mockIndex.EXPECT().GetSymbolInformationFromDescriptors(gomock.Any(), gomock.Any()).Return(&model.SymbolInformation{Symbol: "impl#sym"}, "impl.go", nil)
	mockIndex.EXPECT().LoadDocument("impl.go").Return(&model.Document{
		Occurrences: []*model.Occurrence{
			{Symbol: "impl#sym", SymbolRoles: int32(scipproto.SymbolRole_Definition), Range: []int32{10, 1, 10, 5}},
		},
	}, nil)

	locs, err := registry.Implementation(sourceURI, pos)
	require.NoError(t, err)
	require.Equal(t, 1, len(locs))
	assert.Equal(t, uri.File("/workspace/impl.go"), locs[0].URI)
	assert.Equal(t, protocol.Position{Line: 10, Character: 1}, locs[0].Range.Start)
	assert.Equal(t, protocol.Position{Line: 10, Character: 5}, locs[0].Range.End)
}

func TestPartialScipRegistry_Implementation_Fallback(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockIndex := partialloadermock.NewMockPartialIndex(ctrl)
	logger := zaptest.NewLogger(t).Sugar()

	registry := &partialScipRegistry{
		WorkspaceRoot: "/workspace",
		Index:         mockIndex,
		logger:        logger,
	}

	sourceURI := uri.File("/workspace/test.go")
	pos := protocol.Position{Line: 1, Character: 1}

	// Source occurrence at position
	sourceOcc := &model.Occurrence{Symbol: tracingUUIDKey, Range: []int32{1, 1, 1, 2}}
	mockIndex.EXPECT().LoadDocument("test.go").Return(&model.Document{
		Occurrences: []*model.Occurrence{sourceOcc},
	}, nil)

	// Reverse index empty, fallback to relationships
	mockIndex.EXPECT().GetImplementationSymbols(tracingUUIDKey).Return([]string{}, nil)
	mockIndex.EXPECT().GetSymbolInformation(tracingUUIDKey).Return(&model.SymbolInformation{
		Symbol: tracingUUIDKey,
		Relationships: []*model.Relationship{
			{Symbol: "scip-go gomod example/pkg v1.0.0 `example/pkg`/Foo#Bar.", IsImplementation: true},
		},
	}, "", nil)

	// Resolve implementor to definition occurrence
	mockIndex.EXPECT().GetSymbolInformationFromDescriptors(gomock.Any(), gomock.Any()).Return(&model.SymbolInformation{Symbol: "impl#sym"}, "impl.go", nil)
	mockIndex.EXPECT().LoadDocument("impl.go").Return(&model.Document{
		Occurrences: []*model.Occurrence{
			{Symbol: "impl#sym", SymbolRoles: int32(scipproto.SymbolRole_Definition), Range: []int32{10, 1, 10, 5}},
		},
	}, nil)

	locs, err := registry.Implementation(sourceURI, pos)
	require.NoError(t, err)
	require.Equal(t, 1, len(locs))
	assert.Equal(t, uri.File("/workspace/impl.go"), locs[0].URI)
}
