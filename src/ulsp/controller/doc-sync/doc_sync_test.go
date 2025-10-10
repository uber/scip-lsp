package docsync

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tally "github.com/uber-go/tally/v4"
	"github.com/uber/scip-lsp/src/ulsp/entity"
	"github.com/uber/scip-lsp/src/ulsp/factory"
	"github.com/uber/scip-lsp/src/ulsp/gateway/ide-client/ideclientmock"
	"github.com/uber/scip-lsp/src/ulsp/internal/fs/fsmock"
	"github.com/uber/scip-lsp/src/ulsp/repository/session/repositorymock"
	"go.lsp.dev/protocol"
	"go.uber.org/config"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
)

func TestNew(t *testing.T) {
	mockConfig, _ := config.NewStaticProvider(map[string]interface{}{
		_maxFileSizeKey: 2000,
	})
	assert.NotPanics(t, func() {
		New(Params{
			Stats:  tally.NewTestScope("testing", make(map[string]string, 0)),
			Config: mockConfig,
			Logger: zap.NewNop().Sugar(),
		})
	})
}

func TestStartupInfo(t *testing.T) {
	ctx := context.Background()
	c := controller{}
	result, err := c.StartupInfo(ctx)

	assert.NoError(t, err)
	assert.NoError(t, result.Validate())
	assert.Equal(t, _nameKey, result.NameKey)
}

func TestInitialize(t *testing.T) {
	ctrl := gomock.NewController(t)
	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)

	ctx := context.WithValue(context.Background(), entity.SessionContextKey, s.UUID)

	c := controller{
		sessions:  sessionRepository,
		documents: make(documentStore),
		stats:     tally.NewTestScope("testing", make(map[string]string, 0)),
	}
	initParams := &protocol.InitializeParams{}
	initResult := &protocol.InitializeResult{}
	err := c.initialize(ctx, initParams, initResult)

	assert.NoError(t, err)
	_, ok := c.documents[s.UUID]
	assert.True(t, ok)
	assert.Len(t, c.documents, 1)
}

func TestShutdown(t *testing.T) {
	ctrl := gomock.NewController(t)
	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)

	ctx := context.WithValue(context.Background(), entity.SessionContextKey, s.UUID)

	c := controller{
		sessions:  sessionRepository,
		documents: make(documentStore),
		stats:     tally.NewTestScope("testing", make(map[string]string, 0)),
	}

	c.documents[s.UUID] = make(map[protocol.TextDocumentIdentifier]*documentStoreEntry)
	_, ok := c.documents[s.UUID]
	require.True(t, ok)

	err := c.shutdown(ctx)
	assert.NoError(t, err)

	_, ok = c.documents[s.UUID]
	assert.False(t, ok)
	assert.Len(t, c.documents, 0)
}

func TestDidOpen(t *testing.T) {
	ctrl := gomock.NewController(t)
	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil).AnyTimes()

	ctx := context.WithValue(context.Background(), entity.SessionContextKey, s.UUID)

	sampleParams := []*protocol.DidOpenTextDocumentParams{
		{
			TextDocument: protocol.TextDocumentItem{
				URI:        "file://my/path/file.go",
				LanguageID: "go",
				Version:    1,
				Text:       "Sample text 1",
			},
		},
		{
			TextDocument: protocol.TextDocumentItem{
				URI:        "file://my/path/file2.go",
				LanguageID: "go",
				Version:    2,
				Text:       "Sample text 2",
			},
		},
		{
			TextDocument: protocol.TextDocumentItem{
				URI:        "file://my/path/file3.go",
				LanguageID: "go",
				Version:    3,
				Text:       "Sample text 3",
			},
		},
	}

	t.Run("missing session", func(t *testing.T) {
		c := controller{
			sessions:         sessionRepository,
			documents:        make(documentStore),
			stats:            tally.NewTestScope("testing", make(map[string]string, 0)),
			maxFileSizeBytes: 2000,
		}

		for _, params := range sampleParams {
			err := c.didOpen(ctx, params)
			assert.Error(t, err)
		}
	})

	t.Run("valid session", func(t *testing.T) {
		c := controller{
			sessions:         sessionRepository,
			documents:        make(documentStore),
			stats:            tally.NewTestScope("testing", make(map[string]string, 0)),
			maxFileSizeBytes: 2000,
		}

		c.documents[s.UUID] = make(map[protocol.TextDocumentIdentifier]*documentStoreEntry)
		_, ok := c.documents[s.UUID]
		require.True(t, ok)

		for i, params := range sampleParams {
			err := c.didOpen(ctx, params)
			assert.NoError(t, err)

			result, ok := c.documents[s.UUID][protocol.TextDocumentIdentifier{URI: params.TextDocument.URI}]
			assert.True(t, ok)
			assert.Len(t, c.documents[s.UUID], i+1)
			assert.Equal(t, result.Document.URI, params.TextDocument.URI)
			assert.Equal(t, result.Document.Text, params.TextDocument.Text)
			assert.Equal(t, result.Document.Version, params.TextDocument.Version)
		}
	})
}

func TestDidChange(t *testing.T) {
	ctrl := gomock.NewController(t)
	sessionRepository := repositorymock.NewMockRepository(ctrl)
	ideGatewayMock := ideclientmock.NewMockGateway(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}

	ctx := context.WithValue(context.Background(), entity.SessionContextKey, s.UUID)

	documents := []*protocol.TextDocumentItem{
		{
			URI:        "file://my/path/file.go",
			LanguageID: "go",
			Version:    1,
			Text:       "Sample text 1",
		},
		{
			URI:        "file://my/path/file2.go",
			LanguageID: "go",
			Version:    2,
			Text:       "Sample text 2",
		},
		{
			URI:        "file://my/path/file3.go",
			LanguageID: "go",
			Version:    3,
			Text:       "Sample text 3",
		},
	}

	t.Run("missing session", func(t *testing.T) {
		c := controller{
			sessions:  sessionRepository,
			documents: make(documentStore),
			stats:     tally.NewTestScope("testing", make(map[string]string, 0)),
		}

		for _, doc := range documents {
			sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, errors.New("error"))
			err := c.didChange(context.Background(), &protocol.DidChangeTextDocumentParams{
				TextDocument: protocol.VersionedTextDocumentIdentifier{
					TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: doc.URI},
					Version:                doc.Version,
				},
				ContentChanges: []protocol.TextDocumentContentChangeEvent{},
			})
			assert.Error(t, err)
		}
	})

	t.Run("apply valid changes", func(t *testing.T) {
		c := controller{
			sessions:         sessionRepository,
			documents:        make(documentStore),
			stats:            tally.NewTestScope("testing", make(map[string]string, 0)),
			maxFileSizeBytes: 2000,
			ideGateway:       ideGatewayMock,
		}

		c.documents[s.UUID] = make(map[protocol.TextDocumentIdentifier]*documentStoreEntry)
		_, ok := c.documents[s.UUID]
		require.True(t, ok)

		// Use didOpen to populate a map as if the user has the documents open.
		for _, doc := range documents {
			sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
			c.didOpen(ctx, &protocol.DidOpenTextDocumentParams{TextDocument: *doc})
		}

		// One of the documents has an active progress notification.
		ideGatewayMock.EXPECT().Progress(gomock.Any(), gomock.Any()).Return(nil)
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		c.setProgressToken(ctx, protocol.TextDocumentIdentifier{URI: documents[0].URI})

		for _, doc := range documents {
			// Apply a valid change, confirm that the text in the documents map is updated.
			sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
			err := c.didChange(ctx, &protocol.DidChangeTextDocumentParams{
				TextDocument: protocol.VersionedTextDocumentIdentifier{
					TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: doc.URI},
					Version:                doc.Version,
				},
				// Apply the same change to each document.
				ContentChanges: []protocol.TextDocumentContentChangeEvent{
					{
						Range: &protocol.Range{
							Start: protocol.Position{
								Line:      0,
								Character: 0,
							},
							End: protocol.Position{
								Line:      0,
								Character: 0,
							},
						},
						Text: "addedText",
					},
				},
			})
			assert.NoError(t, err)
			assert.Equal(t, "addedText"+doc.Text, c.documents[s.UUID][protocol.TextDocumentIdentifier{URI: doc.URI}].Document.Text)
			assert.True(t, c.documents[s.UUID][protocol.TextDocumentIdentifier{URI: doc.URI}].EditedSinceLastSave)
		}
	})

	t.Run("reject invalid changes", func(t *testing.T) {
		c := controller{
			sessions:         sessionRepository,
			documents:        make(documentStore),
			stats:            tally.NewTestScope("testing", make(map[string]string, 0)),
			maxFileSizeBytes: 2000,
			logger:           zap.NewNop().Sugar(),
		}

		c.documents[s.UUID] = make(map[protocol.TextDocumentIdentifier]*documentStoreEntry)
		_, ok := c.documents[s.UUID]
		require.True(t, ok)

		// Use didOpen to populate a map as if the user has the documents open.
		for _, doc := range documents {
			sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
			c.didOpen(ctx, &protocol.DidOpenTextDocumentParams{TextDocument: *doc})
		}

		for _, doc := range documents {
			sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)

			initial, ok := c.documents[s.UUID][protocol.TextDocumentIdentifier{URI: doc.URI}]
			require.True(t, ok)
			// Apply an invalid change, confirm that error is returned and document left unchanged.
			err := c.didChange(ctx, &protocol.DidChangeTextDocumentParams{
				TextDocument: protocol.VersionedTextDocumentIdentifier{
					TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: doc.URI},
					Version:                doc.Version,
				},
				ContentChanges: []protocol.TextDocumentContentChangeEvent{
					{
						Range: &protocol.Range{
							Start: protocol.Position{
								Line:      15,
								Character: 0,
							},
							End: protocol.Position{
								Line:      0,
								Character: 0,
							},
						},
						Text: "addedText",
					},
				},
			})
			assert.Error(t, err)

			docEntry, ok := c.documents[s.UUID][protocol.TextDocumentIdentifier{URI: doc.URI}]
			assert.True(t, ok)
			assert.NotNil(t, docEntry)
			assert.False(t, docEntry.EditedSinceLastSave)
			assert.Equal(t, initial, docEntry)
		}
	})

	t.Run("missing document", func(t *testing.T) {
		c := controller{
			sessions:         sessionRepository,
			documents:        make(documentStore),
			stats:            tally.NewTestScope("testing", make(map[string]string, 0)),
			maxFileSizeBytes: 2000,
		}

		c.documents[s.UUID] = make(map[protocol.TextDocumentIdentifier]*documentStoreEntry)
		_, ok := c.documents[s.UUID]
		require.True(t, ok)

		// Use didOpen to populate a map as if the user has the documents open.
		for _, doc := range documents {
			sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
			c.didOpen(ctx, &protocol.DidOpenTextDocumentParams{TextDocument: *doc})
		}

		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)

		// Attempt to change url that is not open.
		err := c.didChange(ctx, &protocol.DidChangeTextDocumentParams{
			TextDocument: protocol.VersionedTextDocumentIdentifier{
				TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: "file://nonexistent/file.go"},
				Version:                0,
			},
			// Apply the same change to each document.
			ContentChanges: []protocol.TextDocumentContentChangeEvent{
				{
					Range: &protocol.Range{
						Start: protocol.Position{
							Line:      15,
							Character: 0,
						},
						End: protocol.Position{
							Line:      0,
							Character: 0,
						},
					},
					Text: "addedText",
				},
			},
		})
		assert.Error(t, err)
	})
}

func TestDidSave(t *testing.T) {
	ctrl := gomock.NewController(t)
	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil).AnyTimes()

	ctx := context.WithValue(context.Background(), entity.SessionContextKey, s.UUID)

	documents := []struct {
		textDocument protocol.TextDocumentItem
		saveText     string
		dirty        bool
	}{
		{

			textDocument: protocol.TextDocumentItem{
				URI:        "file://my/path/file.go",
				LanguageID: "go",
				Version:    1,
				Text:       "Sample text 1",
			},
			saveText: "New file contents 1",
			dirty:    false,
		},
		{

			textDocument: protocol.TextDocumentItem{
				URI:        "file://my/path/file2.go",
				LanguageID: "go",
				Version:    1,
				Text:       "Sample text 2",
			},
			saveText: "New file contents 2",
			dirty:    true,
		},
		{

			textDocument: protocol.TextDocumentItem{
				URI:        "file://my/path/file3.go",
				LanguageID: "go",
				Version:    1,
				Text:       "Sample text 3",
			},
			saveText: "New file contents 3",
			dirty:    true,
		},
	}

	c := controller{
		sessions:         sessionRepository,
		documents:        make(documentStore),
		stats:            tally.NewTestScope("testing", make(map[string]string, 0)),
		maxFileSizeBytes: 2000,
	}

	c.documents[s.UUID] = make(map[protocol.TextDocumentIdentifier]*documentStoreEntry)
	_, ok := c.documents[s.UUID]
	require.True(t, ok)

	for _, document := range documents {
		entry := newDocumentStoreEntry(document.textDocument, nil)
		entry.EditedSinceLastSave = document.dirty
		c.documents[s.UUID][protocol.TextDocumentIdentifier{URI: document.textDocument.URI}] = entry
	}

	for _, document := range documents {
		params := &protocol.DidSaveTextDocumentParams{
			Text: document.saveText,
			TextDocument: protocol.TextDocumentIdentifier{
				URI: document.textDocument.URI,
			},
		}
		err := c.didSave(ctx, params)
		assert.NoError(t, err)
		assert.Equal(t, document.saveText, c.documents[s.UUID][protocol.TextDocumentIdentifier{URI: document.textDocument.URI}].Document.Text)
		assert.False(t, c.documents[s.UUID][protocol.TextDocumentIdentifier{URI: document.textDocument.URI}].EditedSinceLastSave)
		assert.Len(t, c.documents[s.UUID], len(documents))
	}
}

func TestDidClose(t *testing.T) {
	tests := []struct {
		name         string
		withProgress bool
	}{
		{
			name:         "has progress token",
			withProgress: false,
		},
		{
			name:         "no progress token",
			withProgress: true,
		},
	}
	ctrl := gomock.NewController(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionRepository := repositorymock.NewMockRepository(ctrl)
			s := &entity.Session{
				UUID: factory.UUID(),
			}
			sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil).AnyTimes()

			ctx := context.WithValue(context.Background(), entity.SessionContextKey, s.UUID)

			ideGatewayMock := ideclientmock.NewMockGateway(ctrl)
			if tt.withProgress {
				ideGatewayMock.EXPECT().Progress(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			}

			c := controller{
				sessions:   sessionRepository,
				documents:  make(documentStore),
				stats:      tally.NewTestScope("testing", make(map[string]string, 0)),
				ideGateway: ideGatewayMock,
			}

			c.documents[s.UUID] = make(map[protocol.TextDocumentIdentifier]*documentStoreEntry)
			_, ok := c.documents[s.UUID]
			require.True(t, ok)

			sampleTextDocuments := []protocol.TextDocumentItem{
				{
					URI:        "file://my/path/file.go",
					LanguageID: "go",
					Version:    1,
					Text:       "Sample text 1",
				},
				{
					URI:        "file://my/path/file2.go",
					LanguageID: "go",
					Version:    2,
					Text:       "Sample text 2",
				},
				{
					URI:        "file://my/path/file3.go",
					LanguageID: "go",
					Version:    3,
					Text:       "Sample text 3",
				},
			}

			for _, doc := range sampleTextDocuments {
				c.documents[s.UUID][protocol.TextDocumentIdentifier{URI: doc.URI}] = newDocumentStoreEntry(doc, nil)
				if tt.withProgress {
					c.setProgressToken(ctx, protocol.TextDocumentIdentifier{URI: doc.URI})
				}
			}

			for i, doc := range sampleTextDocuments {
				params := &protocol.DidCloseTextDocumentParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: doc.URI,
					},
				}
				err := c.didClose(ctx, params)
				assert.NoError(t, err)
				assert.Len(t, c.documents[s.UUID], len(sampleTextDocuments)-i-1)
				_, ok := c.documents[s.UUID][protocol.TextDocumentIdentifier{URI: doc.URI}]
				require.False(t, ok)
			}
		})
	}
}

func TestGetTextDocument(t *testing.T) {
	ctrl := gomock.NewController(t)
	sessionRepository := repositorymock.NewMockRepository(ctrl)

	s := &entity.Session{
		UUID: factory.UUID(),
	}
	sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil).AnyTimes()

	ctx := context.WithValue(context.Background(), entity.SessionContextKey, s.UUID)

	sampleParams := []*protocol.DidOpenTextDocumentParams{
		{
			TextDocument: protocol.TextDocumentItem{
				URI:        "file://my/path/file.go",
				LanguageID: "go",
				Version:    1,
				Text:       "Sample text 1",
			},
		},
		{
			TextDocument: protocol.TextDocumentItem{
				URI:        "file://my/path/file2.go",
				LanguageID: "go",
				Version:    2,
				Text:       "Sample text 2",
			},
		},
		{
			TextDocument: protocol.TextDocumentItem{
				URI:        "file://my/path/file3.go",
				LanguageID: "go",
				Version:    3,
				Text:       "Sample text 3",
			},
		},
	}

	c := controller{
		sessions:         sessionRepository,
		documents:        make(documentStore),
		stats:            tally.NewTestScope("testing", make(map[string]string, 0)),
		maxFileSizeBytes: 2000,
	}

	c.documents[s.UUID] = make(map[protocol.TextDocumentIdentifier]*documentStoreEntry)
	_, ok := c.documents[s.UUID]
	require.True(t, ok)

	for _, params := range sampleParams {
		c.didOpen(ctx, params)
	}

	require.Len(t, c.documents[s.UUID], len(sampleParams))

	t.Run("valid document", func(t *testing.T) {
		docs := c.documents[s.UUID]
		for _, val := range docs {
			result, err := c.GetTextDocument(ctx, protocol.TextDocumentIdentifier{URI: val.Document.URI})
			assert.NoError(t, err)
			assert.Equal(t, result.URI, val.Document.URI)
		}
	})

	t.Run("unknown identifier", func(t *testing.T) {
		_, err := c.GetTextDocument(ctx, protocol.TextDocumentIdentifier{URI: "invalidURI"})
		assert.Error(t, err)
	})
}

func TestGetDocumentState(t *testing.T) {
	ctrl := gomock.NewController(t)
	sessionRepository := repositorymock.NewMockRepository(ctrl)
	fs := fsmock.NewMockUlspFS(ctrl)

	s := &entity.Session{
		UUID: factory.UUID(),
	}
	sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil).AnyTimes()

	ctx := context.WithValue(context.Background(), entity.SessionContextKey, s.UUID)

	documents := []protocol.TextDocumentItem{
		protocol.TextDocumentItem{
			URI:        "file://my/path/file.go",
			LanguageID: "go",
			Version:    1,
			Text:       "Sample text 1",
		},
		protocol.TextDocumentItem{
			URI:        "file://my/path/file2.go",
			LanguageID: "go",
			Version:    2,
			Text:       "Sample text 2",
		},
		protocol.TextDocumentItem{
			URI:        "file://my/path/file3.go",
			LanguageID: "go",
			Version:    3,
			Text:       "Sample text 3",
		},
	}

	setupController := func() *controller {
		c := controller{
			sessions:  sessionRepository,
			documents: make(documentStore),
			stats:     tally.NewTestScope("testing", make(map[string]string, 0)),
			fs:        fs,
		}

		c.documents[s.UUID] = make(map[protocol.TextDocumentIdentifier]*documentStoreEntry)
		_, ok := c.documents[s.UUID]
		require.True(t, ok)

		for _, doc := range documents {
			c.documents[s.UUID][protocol.TextDocumentIdentifier{URI: doc.URI}] = newDocumentStoreEntry(doc, nil)
		}

		return &c
	}

	t.Run("no edits", func(t *testing.T) {
		c := setupController()
		docs := c.documents[s.UUID]
		for _, val := range docs {
			result, err := c.GetDocumentState(ctx, protocol.TextDocumentIdentifier{URI: val.Document.URI})
			assert.NoError(t, err)
			assert.Equal(t, DocumentStateOpenClean, result)
		}
	})

	t.Run("edits match disk", func(t *testing.T) {
		c := setupController()
		docs := c.documents[s.UUID]
		for _, val := range docs {
			fs.EXPECT().ReadFile(gomock.Any()).Return([]byte(val.Document.Text), nil)
			val.EditedSinceLastSave = true
			c.documents[s.UUID][protocol.TextDocumentIdentifier{URI: val.Document.URI}] = val
			result, err := c.GetDocumentState(ctx, protocol.TextDocumentIdentifier{URI: val.Document.URI})
			assert.NoError(t, err)
			assert.Equal(t, DocumentStateOpenClean, result)
		}
	})

	t.Run("edits do not match disk", func(t *testing.T) {
		c := setupController()
		docs := c.documents[s.UUID]
		for _, val := range docs {
			fs.EXPECT().ReadFile(gomock.Any()).Return([]byte(val.Document.Text+"changes"), nil)
			val.EditedSinceLastSave = true
			c.documents[s.UUID][protocol.TextDocumentIdentifier{URI: val.Document.URI}] = val
			result, err := c.GetDocumentState(ctx, protocol.TextDocumentIdentifier{URI: val.Document.URI})
			assert.NoError(t, err)
			assert.Equal(t, DocumentStateOpenDirty, result)
		}
	})

	t.Run("closed document", func(t *testing.T) {
		c := setupController()
		result, err := c.GetDocumentState(ctx, protocol.TextDocumentIdentifier{URI: "/other/path/file.go"})
		assert.NoError(t, err)
		assert.Equal(t, DocumentStateClosed, result)
	})
}

func TestAddPendingEdits(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.WithValue(context.Background(), entity.SessionContextKey, factory.UUID())
	ideGatewayMock := ideclientmock.NewMockGateway(ctrl)

	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil).AnyTimes()

	sampleEdits := []protocol.TextEdit{
		{
			Range: protocol.Range{
				Start: protocol.Position{
					Line:      0,
					Character: 3,
				},
				End: protocol.Position{
					Line:      0,
					Character: 4,
				},
			},
			NewText: "sampleNewText1",
		},
		{
			Range: protocol.Range{
				Start: protocol.Position{
					Line:      0,
					Character: 8,
				},
				End: protocol.Position{
					Line:      0,
					Character: 9,
				},
			},
			NewText: "sampleNewText2",
		},
	}

	t.Run("valid update", func(t *testing.T) {
		c := controller{
			sessions:   sessionRepository,
			documents:  make(documentStore),
			stats:      tally.NewTestScope("testing", make(map[string]string, 0)),
			ideGateway: ideGatewayMock,
		}

		ideGatewayMock.EXPECT().WorkDoneProgressCreate(gomock.Any(), gomock.Any()).Return(nil)
		ideGatewayMock.EXPECT().Progress(gomock.Any(), gomock.Any()).Return(nil)

		c.documents[s.UUID] = getSampleDocumentEntries()

		id := protocol.TextDocumentIdentifier{URI: "file://my/path/file.go"}
		_, err := c.AddPendingEdits(ctx, c.documents[s.UUID][id].Document, sampleEdits)
		assert.NoError(t, err)
		assert.Len(t, c.documents[s.UUID][id].Edits, len(sampleEdits))
		for i := range sampleEdits {
			assert.Equal(t, sampleEdits[i].NewText, c.documents[s.UUID][id].Edits[i].NewText)
		}
	})

	t.Run("no edits", func(t *testing.T) {
		c := controller{
			sessions:   sessionRepository,
			documents:  make(documentStore),
			stats:      tally.NewTestScope("testing", make(map[string]string, 0)),
			ideGateway: ideGatewayMock,
		}
		c.documents[s.UUID] = getSampleDocumentEntries()

		id := protocol.TextDocumentIdentifier{URI: "file://my/path/file.go"}
		_, err := c.AddPendingEdits(ctx, c.documents[s.UUID][id].Document, []protocol.TextEdit{})
		assert.NoError(t, err)
		assert.Len(t, c.documents[s.UUID][id].Edits, 0)
	})

	t.Run("version mismatch", func(t *testing.T) {
		c := controller{
			sessions:  sessionRepository,
			documents: make(documentStore),
			stats:     tally.NewTestScope("testing", make(map[string]string, 0)),
			logger:    zap.NewNop().Sugar(),
		}

		c.documents[s.UUID] = getSampleDocumentEntries()

		id := protocol.TextDocumentIdentifier{URI: "file://my/path/file.go"}
		added, err := c.AddPendingEdits(ctx, protocol.TextDocumentItem{
			URI:        c.documents[s.UUID][id].Document.URI,
			LanguageID: c.documents[s.UUID][id].Document.LanguageID,
			Version:    c.documents[s.UUID][id].Document.Version - 1,
			Text:       c.documents[s.UUID][id].Document.Text,
		}, sampleEdits)
		assert.Error(t, err)
		assert.False(t, added)
	})

	t.Run("uri mismatch", func(t *testing.T) {
		c := controller{
			sessions:  sessionRepository,
			documents: make(documentStore),
			stats:     tally.NewTestScope("testing", make(map[string]string, 0)),
		}

		c.documents[s.UUID] = getSampleDocumentEntries()

		id := protocol.TextDocumentIdentifier{URI: "file://my/path/file.go"}
		_, err := c.AddPendingEdits(ctx, protocol.TextDocumentItem{
			URI:        c.documents[s.UUID][id].Document.URI + "invalid",
			LanguageID: c.documents[s.UUID][id].Document.LanguageID,
			Version:    c.documents[s.UUID][id].Document.Version - 1,
			Text:       c.documents[s.UUID][id].Document.Text,
		}, sampleEdits)
		assert.Error(t, err)
	})

	t.Run("no docs for session", func(t *testing.T) {
		c := controller{
			sessions:  sessionRepository,
			documents: make(documentStore),
			stats:     tally.NewTestScope("testing", make(map[string]string, 0)),
		}

		sampleDocs := getSampleDocumentEntries()
		id := protocol.TextDocumentIdentifier{URI: "file://my/path/file.go"}

		_, err := c.AddPendingEdits(ctx, sampleDocs[id].Document, []protocol.TextEdit{
			{
				Range:   protocol.Range{},
				NewText: "sampleNewText1",
			},
		})
		assert.Error(t, err)
	})
}

func TestWillSaveWaitUntil(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.WithValue(context.Background(), entity.SessionContextKey, factory.UUID())

	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil).AnyTimes()

	t.Run("valid document", func(t *testing.T) {
		c := controller{
			sessions:  sessionRepository,
			documents: make(documentStore),
			stats:     tally.NewTestScope("testing", make(map[string]string, 0)),
		}

		c.documents[s.UUID] = getSampleDocumentEntries()
		sampleEdits := getSampleEdits()
		for key, entry := range c.documents[s.UUID] {
			entry.Edits = append(entry.Edits, sampleEdits...)
			c.documents[s.UUID][key] = entry
		}

		for key := range c.documents[s.UUID] {
			result := []protocol.TextEdit{}
			err := c.willSaveWaitUntil(ctx, &protocol.WillSaveTextDocumentParams{
				TextDocument: protocol.TextDocumentIdentifier{
					URI: key.URI,
				},
				Reason: protocol.TextDocumentSaveReasonManual,
			}, &result)
			assert.NoError(t, err)
			assert.Len(t, result, len(sampleEdits))
			for edit := range result {
				assert.Equal(t, sampleEdits[edit].NewText, result[edit].NewText)
			}
		}
	})

	t.Run("invalid document", func(t *testing.T) {
		c := controller{
			sessions:  sessionRepository,
			documents: make(documentStore),
			stats:     tally.NewTestScope("testing", make(map[string]string, 0)),
		}

		c.documents[s.UUID] = getSampleDocumentEntries()
		sampleEdits := getSampleEdits()
		for key, entry := range c.documents[s.UUID] {
			entry.Edits = append(entry.Edits, sampleEdits...)
			c.documents[s.UUID][key] = entry
		}

		for key := range c.documents[s.UUID] {
			result := []protocol.TextEdit{}
			err := c.willSaveWaitUntil(ctx, &protocol.WillSaveTextDocumentParams{
				TextDocument: protocol.TextDocumentIdentifier{
					URI: key.URI + "invalid",
				},
				Reason: protocol.TextDocumentSaveReasonManual,
			}, &result)
			assert.Error(t, err)
		}
	})

}

func TestUpdateMetrics(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Run("basic metrics update", func(t *testing.T) {
		ctx := context.Background()
		sessionRepository := repositorymock.NewMockRepository(ctrl)
		s := &entity.Session{
			UUID: factory.UUID(),
		}
		testScope := tally.NewTestScope("testing", make(map[string]string, 0))

		c := controller{
			sessions:  sessionRepository,
			documents: make(documentStore),
			stats:     testScope,
		}

		sampleTextDocuments := []protocol.TextDocumentItem{
			{
				URI:        "file://my/path/file.go",
				LanguageID: "go",
				Version:    1,
				Text:       "Sample text 1",
			},
			{
				URI:        "file://my/path/file2.go",
				LanguageID: "go",
				Version:    2,
				Text:       "Sample text 2",
			},
			{
				URI:        "file://my/path/file3.go",
				LanguageID: "go",
				Version:    3,
				Text:       "Sample text 3",
			},
		}

		c.documents[s.UUID] = make(map[protocol.TextDocumentIdentifier]*documentStoreEntry)
		for _, doc := range sampleTextDocuments {
			c.documents[s.UUID][protocol.TextDocumentIdentifier{URI: doc.URI}] = newDocumentStoreEntry(doc, nil)
		}

		// Ensure that gauges are updated.
		c.updateMetrics(ctx)
		snapshot := testScope.Snapshot().Gauges()
		assert.Equal(t, float64(3), snapshot["testing.open_docs+"].Value())
		assert.Equal(t, float64(39), snapshot["testing.open_bytes+"].Value())

		// Delete a document and ensure that gauges update.
		delete(c.documents[s.UUID], protocol.TextDocumentIdentifier{URI: "file://my/path/file.go"})
		c.updateMetrics(ctx)
		snapshot = testScope.Snapshot().Gauges()
		assert.Equal(t, float64(2), snapshot["testing.open_docs+"].Value())
	})

	t.Run("concurrent lock handling", func(t *testing.T) {
		ctx := context.Background()
		sessionRepository := repositorymock.NewMockRepository(ctrl)
		s := &entity.Session{
			UUID: factory.UUID(),
		}
		testScope := tally.NewTestScope("testing", make(map[string]string, 0))

		c := controller{
			sessions:  sessionRepository,
			documents: make(documentStore),
			stats:     testScope,
		}

		c.documents[s.UUID] = make(map[protocol.TextDocumentIdentifier]*documentStoreEntry)
		doc := protocol.TextDocumentItem{
			URI:        "file://my/path/file.go",
			LanguageID: "go",
			Version:    1,
			Text:       "Sample text 1",
		}
		c.documents[s.UUID][protocol.TextDocumentIdentifier{URI: doc.URI}] = newDocumentStoreEntry(doc, nil)

		// Channels to control test flow
		lockAcquired := make(chan struct{})
		continueExecution := make(chan struct{})
		done := make(chan struct{})
		metricsComplete := make(chan struct{})

		// Start a goroutine that will hold the write lock
		go func() {
			c.documentsMu.Lock()
			lockAcquired <- struct{}{}

			// This will hold the lock until we signal to continue
			<-continueExecution
			c.documentsMu.Unlock()
			done <- struct{}{}
		}()

		// Wait for the write lock to be acquired
		<-lockAcquired

		// Start updateMetrics in another goroutine - it should block until lock is released
		go func() {
			c.updateMetrics(ctx)
			metricsComplete <- struct{}{}
		}()

		// Give updateMetrics a chance to block
		continueExecution <- struct{}{}

		// Wait for both operations to complete
		<-done
		<-metricsComplete

		// Verify the metrics were updated
		snapshot := testScope.Snapshot().Gauges()
		assert.Equal(t, float64(1), snapshot["testing.open_docs+"].Value())
	})
}

func TestValidateSize(t *testing.T) {
	ctx := context.Background()

	c := controller{
		documents:        make(documentStore),
		stats:            tally.NewTestScope("testing", make(map[string]string, 0)),
		maxFileSizeBytes: 10,
	}

	t.Run("valid size", func(t *testing.T) {
		err := c.validateSize(ctx, "test")
		assert.NoError(t, err)
	})

	t.Run("invalid size", func(t *testing.T) {
		err := c.validateSize(ctx, "longer text string")
		assert.Error(t, err)
	})
}

func TestEndSession(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()

	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}

	c := controller{
		sessions:  sessionRepository,
		documents: make(documentStore),
		stats:     tally.NewTestScope("testing", make(map[string]string, 0)),
	}

	c.documents[s.UUID] = make(map[protocol.TextDocumentIdentifier]*documentStoreEntry)
	_, ok := c.documents[s.UUID]
	require.True(t, ok)

	c.endSession(ctx, s.UUID)
	_, ok = c.documents[s.UUID]
	assert.False(t, ok)
}
func TestSetProgressToken(t *testing.T) {
	ctrl := gomock.NewController(t)
	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}

	ctx := context.WithValue(context.Background(), entity.SessionContextKey, s.UUID)

	c := controller{
		sessions:         sessionRepository,
		documents:        make(documentStore),
		stats:            tally.NewTestScope("testing", make(map[string]string, 0)),
		maxFileSizeBytes: 2000,
	}

	c.documents[s.UUID] = make(map[protocol.TextDocumentIdentifier]*documentStoreEntry)
	_, ok := c.documents[s.UUID]
	require.True(t, ok)

	doc := protocol.TextDocumentItem{
		URI:        "file://my/path/file.go",
		LanguageID: "go",
		Version:    1,
		Text:       "Sample text 1",
	}

	c.documents[s.UUID][protocol.TextDocumentIdentifier{URI: doc.URI}] = newDocumentStoreEntry(doc, nil)

	t.Run("valid document", func(t *testing.T) {
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		entry, err := c.setProgressToken(ctx, protocol.TextDocumentIdentifier{URI: doc.URI})
		assert.NoError(t, err)
		assert.NotNil(t, entry.ProgressToken)
	})

	t.Run("missing document", func(t *testing.T) {
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)
		_, err := c.setProgressToken(ctx, protocol.TextDocumentIdentifier{URI: "file://nonexistent/file.go"})
		assert.Error(t, err)
	})

	t.Run("invalid session", func(t *testing.T) {
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(&entity.Session{
			UUID: factory.UUID(),
		}, nil)
		_, err := c.setProgressToken(ctx, protocol.TextDocumentIdentifier{URI: "file://nonexistent/file.go"})
		assert.Error(t, err)
	})

	t.Run("get from context error", func(t *testing.T) {
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(nil, errors.New("sample"))
		_, err := c.setProgressToken(ctx, protocol.TextDocumentIdentifier{URI: "file://nonexistent/file.go"})
		assert.Error(t, err)
	})
}

func getSampleDocumentEntries() map[protocol.TextDocumentIdentifier]*documentStoreEntry {
	return map[protocol.TextDocumentIdentifier]*documentStoreEntry{
		{URI: "file://my/path/file.go"}: newDocumentStoreEntry(protocol.TextDocumentItem{
			URI:        "file://my/path/file.go",
			LanguageID: "go",
			Version:    1,
			Text:       "Sample text 1",
		}, nil),
		{URI: "file://my/path/file2.go"}: newDocumentStoreEntry(protocol.TextDocumentItem{
			URI:        "file://my/path/file2.go",
			LanguageID: "go",
			Version:    3,
			Text:       "Sample text 2",
		}, nil),
		{URI: "file://my/path/file3.go"}: newDocumentStoreEntry(protocol.TextDocumentItem{
			URI:        "file://my/path/file3.go",
			LanguageID: "go",
			Version:    3,
			Text:       "Sample text 3",
		}, nil),
	}
}

func getSampleEdits() []protocol.TextEdit {
	return []protocol.TextEdit{
		{
			Range: protocol.Range{
				Start: protocol.Position{
					Line:      0,
					Character: 3,
				},
				End: protocol.Position{
					Line:      0,
					Character: 4,
				},
			},
			NewText: "sampleNewText1",
		},
		{
			Range: protocol.Range{
				Start: protocol.Position{
					Line:      0,
					Character: 8,
				},
				End: protocol.Position{
					Line:      0,
					Character: 9,
				},
			},
			NewText: "sampleNewText2",
		},
	}
}

func TestGetPositionMapper(t *testing.T) {
	ctrl := gomock.NewController(t)
	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}

	ctx := context.WithValue(context.Background(), entity.SessionContextKey, s.UUID)
	sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil).AnyTimes()

	t.Run("valid document with no edits", func(t *testing.T) {
		c := controller{
			sessions:         sessionRepository,
			documents:        make(documentStore),
			stats:            tally.NewTestScope("testing", make(map[string]string, 0)),
			maxFileSizeBytes: 2000,
		}

		c.documents[s.UUID] = getSampleDocumentEntries()
		for docID := range c.documents[s.UUID] {
			mapper, err := c.GetPositionMapper(ctx, docID)
			assert.NoError(t, err)
			assert.NotNil(t, mapper)

			// Subsequent calls return the same instance
			mapper2, err := c.GetPositionMapper(ctx, docID)
			assert.NoError(t, err)
			assert.NotNil(t, mapper2)
			assert.Equal(t, mapper, mapper2)
		}
	})

	t.Run("valid document with different base version", func(t *testing.T) {
		c := controller{
			sessions:         sessionRepository,
			documents:        make(documentStore),
			stats:            tally.NewTestScope("testing", make(map[string]string, 0)),
			maxFileSizeBytes: 2000,
		}

		c.documents[s.UUID] = make(map[protocol.TextDocumentIdentifier]*documentStoreEntry)

		docID := protocol.TextDocumentIdentifier{URI: "file://my/path/file.go"}
		doc := protocol.TextDocumentItem{
			URI:        docID.URI,
			LanguageID: "go",
			Version:    5,
			Text:       "abc",
		}
		baseDoc := protocol.TextDocumentItem{
			URI:        docID.URI,
			LanguageID: "go",
			Version:    2,
			Text:       "abcd",
		}
		entry := newDocumentStoreEntry(doc, newDocumentStoreEntry(baseDoc, nil))
		c.documents[s.UUID][docID] = entry

		// Get position mapper and verify it's created correctly
		mapper, err := c.GetPositionMapper(ctx, docID)
		assert.NoError(t, err)
		assert.NotNil(t, mapper)

		// Verify we can map positions between different versions
		pos := protocol.Position{Line: 0, Character: 2} // Position at 'c'
		mappedPos, _, err := mapper.MapCurrentPositionToBase(pos)
		assert.NoError(t, err)
		assert.Equal(t, pos, mappedPos, "Position 'c' should map to same position since it exists in both texts")
	})

	t.Run("untracked document", func(t *testing.T) {
		c := controller{
			sessions:         sessionRepository,
			documents:        make(documentStore),
			stats:            tally.NewTestScope("testing", make(map[string]string, 0)),
			maxFileSizeBytes: 2000,
		}

		c.documents[s.UUID] = getSampleDocumentEntries()

		mapper, err := c.GetPositionMapper(ctx, protocol.TextDocumentIdentifier{URI: "file://nonexistent/file.go"})
		assert.Nil(t, mapper)
		assert.NoError(t, err)
	})

	t.Run("missing session", func(t *testing.T) {
		c := controller{
			sessions:         sessionRepository,
			documents:        make(documentStore),
			stats:            tally.NewTestScope("testing", make(map[string]string, 0)),
			maxFileSizeBytes: 2000,
		}

		_, err := c.GetPositionMapper(ctx, protocol.TextDocumentIdentifier{URI: "file://my/path/file.go"})
		assert.Error(t, err)
	})
}

func TestResetBase(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()
	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s1 := &entity.Session{
		UUID: factory.UUID(),
	}
	s2 := &entity.Session{
		UUID: factory.UUID(),
	}

	doc := protocol.TextDocumentIdentifier{URI: "file://my/path/file.go"}

	t.Run("success", func(t *testing.T) {
		sessionRepository.EXPECT().GetAllFromWorkspaceRoot(gomock.Any(), gomock.Any()).Return([]*entity.Session{s1, s2}, nil)

		c := controller{
			sessions:  sessionRepository,
			documents: make(documentStore),
		}

		// Set up documents for both sessions
		c.documents[s1.UUID] = make(map[protocol.TextDocumentIdentifier]*documentStoreEntry)
		c.documents[s2.UUID] = make(map[protocol.TextDocumentIdentifier]*documentStoreEntry)

		// Create document entries with bases
		baseEntry := newDocumentStoreEntry(protocol.TextDocumentItem{
			URI:     doc.URI,
			Version: 1,
			Text:    "base text",
		}, nil)

		docEntry := newDocumentStoreEntry(protocol.TextDocumentItem{
			URI:     doc.URI,
			Version: 2,
			Text:    "current text",
		}, baseEntry)

		c.documents[s1.UUID][doc] = docEntry
		c.documents[s2.UUID][doc] = docEntry

		// Verify bases are set
		assert.NotNil(t, c.documents[s1.UUID][doc].Base)
		assert.NotNil(t, c.documents[s2.UUID][doc].Base)

		err := c.ResetBase(ctx, "/home/fievel", doc)
		assert.NoError(t, err)

		// Verify bases are cleared
		assert.Nil(t, c.documents[s1.UUID][doc].Base)
		assert.Nil(t, c.documents[s2.UUID][doc].Base)
	})

	t.Run("error getting sessions", func(t *testing.T) {
		sessionRepository.EXPECT().GetAllFromWorkspaceRoot(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("failed to get sessions"))

		c := controller{
			sessions: sessionRepository,
		}

		err := c.ResetBase(ctx, "/home/fievel", doc)
		assert.Error(t, err)
	})

	t.Run("no matching sessions", func(t *testing.T) {
		sessionRepository.EXPECT().GetAllFromWorkspaceRoot(gomock.Any(), gomock.Any()).Return([]*entity.Session{}, nil)

		c := controller{
			sessions: sessionRepository,
		}

		err := c.ResetBase(ctx, "/home/fievel", doc)
		assert.NoError(t, err)
	})
}

func TestDocumentStoreEntry_ClearBase(t *testing.T) {
	doc := protocol.TextDocumentItem{
		URI:     "file://my/path/file.go",
		Version: 2,
		Text:    "current text",
	}

	baseDoc := protocol.TextDocumentItem{
		URI:     "file://my/path/file.go",
		Version: 1,
		Text:    "base text",
	}

	t.Run("clear base and position mapper", func(t *testing.T) {
		baseEntry := newDocumentStoreEntry(baseDoc, nil)
		entry := newDocumentStoreEntry(doc, baseEntry)

		// Set up a position mapper
		entry.positionMapper = NewPositionMapper(baseDoc.Text, doc.Text)

		assert.NotNil(t, entry.Base)
		assert.NotNil(t, entry.positionMapper)

		entry.clearBase()

		assert.Nil(t, entry.Base)
		assert.Nil(t, entry.positionMapper)
	})

	t.Run("clear when already nil", func(t *testing.T) {
		entry := newDocumentStoreEntry(doc, nil)
		entry.clearBase()

		assert.Nil(t, entry.Base)
		assert.Nil(t, entry.positionMapper)
	})
}
