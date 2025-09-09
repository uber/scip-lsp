package quickactions

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"go.uber.org/config"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/uber/scip-lsp/src/ulsp/controller/doc-sync/docsyncmock"
	action "github.com/uber/scip-lsp/src/ulsp/controller/quick-actions/action"
	quickactionmock "github.com/uber/scip-lsp/src/ulsp/controller/quick-actions/action/actionmock"
	"github.com/uber/scip-lsp/src/ulsp/entity"
	"github.com/uber/scip-lsp/src/ulsp/factory"
	"github.com/uber/scip-lsp/src/ulsp/gateway/ide-client/ideclientmock"
	ulsperrors "github.com/uber/scip-lsp/src/ulsp/internal/errors"
	"github.com/uber/scip-lsp/src/ulsp/mapper"
	"github.com/uber/scip-lsp/src/ulsp/repository/session/repositorymock"
	"go.lsp.dev/protocol"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
)

const _monorepoNameGoCode entity.MonorepoName = "go-code"

var _sampleRegex = regexp.MustCompile("sampleRegex")

func TestNew(t *testing.T) {
	mockConfig, _ := config.NewStaticProvider(map[string]interface{}{})

	assert.NotPanics(t, func() {
		New(Params{
			Logger: zap.NewNop().Sugar(),
			Config: mockConfig,
		})
	})
}

func TestStartupInfo(t *testing.T) {
	ctx := context.Background()
	c := controller{}
	info, err := c.StartupInfo(ctx)
	assert.NoError(t, err)
	assert.NoError(t, info.Validate())
}

func TestInitialize(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name     string
		monorepo entity.MonorepoName
		config   entity.MonorepoConfigs
		client   string
	}{
		{
			name:     "go",
			monorepo: "go-code",
			config: map[entity.MonorepoName]entity.MonorepoConfigEntry{
				"go-code": {
					Languages: []string{"go"},
				},
			},
		},
		{
			name:     "java vs code",
			monorepo: "lm/fievel",
			config: map[entity.MonorepoName]entity.MonorepoConfigEntry{
				"lm/fievel": {
					Languages: []string{"java"},
				},
			},
			client: "Visual Studio Code",
		},
		{
			name:     "java other",
			monorepo: "lm/fievel",
			config: map[entity.MonorepoName]entity.MonorepoConfigEntry{
				"lm/fievel": {
					Languages: []string{"java"},
				},
			},
			client: "other",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			sessionRepository := repositorymock.NewMockRepository(ctrl)
			s := &entity.Session{
				UUID: factory.UUID(),
			}
			s.Monorepo = tt.monorepo
			sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)

			c := controller{
				currentActionRanges: newActionRangeStore(),
				sessions:            sessionRepository,
				enabledActions:      make(map[uuid.UUID][]action.Action),
				config:              tt.config,
			}

			result := &protocol.InitializeResult{}
			err := c.initialize(ctx, &protocol.InitializeParams{}, result)
			assert.NoError(t, err)
			assert.Equal(t, result.Capabilities.CodeActionProvider, &protocol.CodeActionOptions{CodeActionKinds: []protocol.CodeActionKind{_codeActionKind}})

			for _, a := range allActions {
				if a.ShouldEnable(s, tt.config[tt.monorepo]) {
					assert.Contains(t, c.enabledActions[s.UUID], a)
				} else {
					assert.NotContains(t, c.enabledActions[s.UUID], a)
				}
			}

			expectedUnregistered := 0
			for _, a := range c.enabledActions[s.UUID] {
				if len(a.CommandName()) > 0 {
					assert.Contains(t, result.Capabilities.ExecuteCommandProvider.Commands, a.CommandName())
				} else {
					expectedUnregistered++
				}
			}
			assert.Len(t, result.Capabilities.ExecuteCommandProvider.Commands, len(c.enabledActions[s.UUID])-expectedUnregistered)
		})
	}

	t.Run("duplicate command", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		sessionRepository := repositorymock.NewMockRepository(ctrl)
		s := &entity.Session{
			UUID: factory.UUID(),
		}
		s.Monorepo = "lm/fievel"
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil).AnyTimes()

		c := controller{
			currentActionRanges: newActionRangeStore(),
			sessions:            sessionRepository,
			enabledActions:      make(map[uuid.UUID][]action.Action),
			// Currently only java adds actions with commands, so we can use that to trigger the duplicate command error.
			config: map[entity.MonorepoName]entity.MonorepoConfigEntry{
				"lm/fievel": {
					Languages: []string{"java"},
				},
			},
		}
		result := &protocol.InitializeResult{}
		err := c.initialize(ctx, &protocol.InitializeParams{}, result)
		assert.NoError(t, err)
		err = c.initialize(ctx, &protocol.InitializeParams{}, result)
		assert.Error(t, err)
	})

	t.Run("code action error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		sessionRepository := repositorymock.NewMockRepository(ctrl)
		s := &entity.Session{
			UUID: factory.UUID(),
		}
		s.Monorepo = _monorepoNameGoCode
		sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)

		c := controller{
			currentActionRanges: newActionRangeStore(),
			sessions:            sessionRepository,
			enabledActions:      make(map[uuid.UUID][]action.Action),
		}

		result := &protocol.InitializeResult{
			Capabilities: protocol.ServerCapabilities{
				CodeActionProvider: "not *CodeActionOptions",
			},
		}
		err := c.initialize(ctx, &protocol.InitializeParams{}, result)
		assert.Error(t, err)
	})
}

func TestRefreshAvailableCodeActions(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	s.Monorepo = _monorepoNameGoCode
	sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil).AnyTimes()

	documents := docsyncmock.NewMockController(ctrl)
	sampleDoc := protocol.TextDocumentItem{URI: "sampleURI"}

	action1 := quickactionmock.NewMockAction(ctrl)
	t.Run("doc not relevant", func(t *testing.T) {
		c := controller{
			currentActionRanges: newActionRangeStore(),
			sessions:            sessionRepository,
			documents:           documents,
			enabledActions:      map[uuid.UUID][]action.Action{s.UUID: {action1}},
		}
		documents.EXPECT().GetTextDocument(gomock.Any(), gomock.Any()).Return(sampleDoc, nil)
		action1.EXPECT().IsRelevantDocument(gomock.Any(), gomock.Any()).Return(false)

		err := c.refreshAvailableCodeActions(ctx, protocol.TextDocumentIdentifier{URI: sampleDoc.URI})
		assert.NoError(t, err)
	})

	t.Run("doc process success", func(t *testing.T) {
		c := controller{
			currentActionRanges: newActionRangeStore(),
			sessions:            sessionRepository,
			documents:           documents,
			enabledActions:      map[uuid.UUID][]action.Action{s.UUID: {action1}},
		}

		sampleMatches := getSampleMatches()
		documents.EXPECT().GetTextDocument(gomock.Any(), gomock.Any()).Return(sampleDoc, nil)
		action1.EXPECT().IsRelevantDocument(gomock.Any(), gomock.Any()).Return(true)
		action1.EXPECT().ProcessDocument(ctx, sampleDoc).Return(sampleMatches, nil)

		err := c.refreshAvailableCodeActions(ctx, protocol.TextDocumentIdentifier{URI: sampleDoc.URI})
		assert.NoError(t, err)
		for _, match := range sampleMatches {
			if codeActionMatch, ok := match.(mapper.CodeActionWithRange); ok {
				assert.NotNil(t, c.currentActionRanges.actions[s.UUID][protocol.TextDocumentIdentifier{URI: sampleDoc.URI}][codeActionMatch.Range])
			} else if codeLensMatch, ok := match.(protocol.CodeLens); ok {
				assert.Contains(t, c.currentActionRanges.codeLenses[s.UUID][protocol.TextDocumentIdentifier{URI: sampleDoc.URI}], codeLensMatch)
			} else {
				assert.Fail(t, "unexpected match type")
			}
		}
	})

	t.Run("invalid type returned", func(t *testing.T) {
		c := controller{
			currentActionRanges: newActionRangeStore(),
			sessions:            sessionRepository,
			documents:           documents,
			enabledActions:      map[uuid.UUID][]action.Action{s.UUID: {action1}},
		}

		sampleMatches := []interface{}{"invalid result"}
		documents.EXPECT().GetTextDocument(gomock.Any(), gomock.Any()).Return(sampleDoc, nil)
		action1.EXPECT().IsRelevantDocument(gomock.Any(), gomock.Any()).Return(true)
		action1.EXPECT().ProcessDocument(ctx, sampleDoc).Return(sampleMatches, nil)

		err := c.refreshAvailableCodeActions(ctx, protocol.TextDocumentIdentifier{URI: sampleDoc.URI})
		assert.Error(t, err)
	})

	t.Run("doc not present", func(t *testing.T) {
		c := controller{
			currentActionRanges: newActionRangeStore(),
			sessions:            sessionRepository,
			documents:           documents,
			enabledActions:      map[uuid.UUID][]action.Action{s.UUID: {action1}},
		}

		documents.EXPECT().GetTextDocument(gomock.Any(), gomock.Any()).Return(protocol.TextDocumentItem{}, &ulsperrors.DocumentNotFoundError{Document: protocol.TextDocumentIdentifier{URI: sampleDoc.URI}})
		err := c.refreshAvailableCodeActions(ctx, protocol.TextDocumentIdentifier{URI: sampleDoc.URI})
		assert.NoError(t, err)
	})

	t.Run("other get document error", func(t *testing.T) {
		c := controller{
			currentActionRanges: newActionRangeStore(),
			sessions:            sessionRepository,
			documents:           documents,
			enabledActions:      map[uuid.UUID][]action.Action{s.UUID: {action1}},
		}

		documents.EXPECT().GetTextDocument(gomock.Any(), gomock.Any()).Return(protocol.TextDocumentItem{}, errors.New("sample"))
		err := c.refreshAvailableCodeActions(ctx, protocol.TextDocumentIdentifier{URI: sampleDoc.URI})
		assert.Error(t, err)
	})

	t.Run("doc process failure", func(t *testing.T) {
		c := controller{
			currentActionRanges: newActionRangeStore(),
			sessions:            sessionRepository,
			documents:           documents,
			enabledActions:      map[uuid.UUID][]action.Action{s.UUID: {action1}},
		}

		documents.EXPECT().GetTextDocument(gomock.Any(), gomock.Any()).Return(sampleDoc, nil)
		action1.EXPECT().IsRelevantDocument(gomock.Any(), gomock.Any()).Return(true)
		action1.EXPECT().ProcessDocument(ctx, sampleDoc).Return(nil, errors.New("sampleError"))

		err := c.refreshAvailableCodeActions(ctx, protocol.TextDocumentIdentifier{URI: sampleDoc.URI})
		assert.Error(t, err)
	})

}

func TestDidOpen(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	s.Monorepo = _monorepoNameGoCode
	sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)

	documents := docsyncmock.NewMockController(ctrl)
	sampleDoc := protocol.TextDocumentItem{URI: "sampleURI"}
	documents.EXPECT().GetTextDocument(gomock.Any(), gomock.Any()).Return(sampleDoc, nil)

	action1 := quickactionmock.NewMockAction(ctrl)
	c := controller{
		currentActionRanges: newActionRangeStore(),
		sessions:            sessionRepository,
		documents:           documents,
		enabledActions:      map[uuid.UUID][]action.Action{s.UUID: {action1}},
	}

	action1.EXPECT().IsRelevantDocument(gomock.Any(), gomock.Any()).Return(true)
	action1.EXPECT().ProcessDocument(ctx, sampleDoc).Return(getSampleMatches(), nil)

	params := &protocol.DidOpenTextDocumentParams{TextDocument: sampleDoc}
	err := c.didOpen(ctx, params)
	assert.NotNil(t, c.currentActionRanges.actions[s.UUID][protocol.TextDocumentIdentifier{URI: params.TextDocument.URI}])
	assert.NoError(t, err)
}

func TestDidSave(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	s.Monorepo = _monorepoNameGoCode
	sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)

	documents := docsyncmock.NewMockController(ctrl)
	sampleDoc := protocol.TextDocumentItem{URI: "sampleURI"}
	documents.EXPECT().GetTextDocument(gomock.Any(), gomock.Any()).Return(sampleDoc, nil)

	action1 := quickactionmock.NewMockAction(ctrl)
	c := controller{
		currentActionRanges: newActionRangeStore(),
		sessions:            sessionRepository,
		documents:           documents,
		enabledActions:      map[uuid.UUID][]action.Action{s.UUID: {action1}},
	}

	action1.EXPECT().IsRelevantDocument(gomock.Any(), gomock.Any()).Return(true)
	action1.EXPECT().ProcessDocument(ctx, sampleDoc).Return(getSampleMatches(), nil)

	params := &protocol.DidSaveTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: sampleDoc.URI}}
	err := c.didSave(ctx, params)
	assert.NotNil(t, c.currentActionRanges.actions[s.UUID][protocol.TextDocumentIdentifier{URI: params.TextDocument.URI}])
	assert.NoError(t, err)
}

func TestDidClose(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	s.Monorepo = _monorepoNameGoCode
	sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil)

	c := controller{
		currentActionRanges: newActionRangeStore(),
		sessions:            sessionRepository,
	}

	params := &protocol.DidCloseTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: "sampleURI"}}
	c.currentActionRanges.AddCodeAction(s.UUID, params.TextDocument, protocol.Range{}, protocol.CodeAction{})
	assert.NotNil(t, c.currentActionRanges.actions[s.UUID][params.TextDocument])
	c.didClose(ctx, params)
	assert.Nil(t, c.currentActionRanges.actions[s.UUID][params.TextDocument])
}

func TestEndSession(t *testing.T) {
	ctx := context.Background()
	_, cancelF := context.WithCancel(ctx)
	s := &entity.Session{
		UUID: factory.UUID(),
	}

	c := controller{
		currentActionRanges: newActionRangeStore(),
		pendingActionRuns:   newInProgressActionStore(),
		pendingCmds:         make(map[protocol.ProgressToken]context.CancelFunc),
	}

	c.currentActionRanges.AddCodeAction(s.UUID, protocol.TextDocumentIdentifier{URI: "sampleURI"}, protocol.Range{}, protocol.CodeAction{})
	assert.NotNil(t, c.currentActionRanges.actions[s.UUID])

	token := c.pendingActionRuns.AddInProgressAction(s.UUID, "sampleCommand", "sampleArgs")
	assert.True(t, c.pendingActionRuns.TokenExists(s.UUID, "sampleCommand", "sampleArgs"))
	c.pendingCmds[*token] = cancelF

	c.endSession(ctx, s.UUID)
	assert.Len(t, c.pendingCmds, 0)
	assert.Nil(t, c.currentActionRanges.actions[s.UUID])
	assert.False(t, c.pendingActionRuns.TokenExists(s.UUID, "sampleCommand", "sampleArgs"))
}

func TestCodeAction(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	s.Monorepo = _monorepoNameGoCode
	sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil).AnyTimes()

	documents := docsyncmock.NewMockController(ctrl)
	sampleDoc := protocol.TextDocumentItem{URI: "sampleURI"}
	documents.EXPECT().GetTextDocument(gomock.Any(), gomock.Any()).Return(sampleDoc, nil).AnyTimes()

	c := controller{
		currentActionRanges: newActionRangeStore(),
		sessions:            sessionRepository,
		documents:           documents,
	}

	sampleRange := protocol.Range{
		Start: protocol.Position{
			Line:      1,
			Character: 1,
		},
		End: protocol.Position{
			Line:      1,
			Character: 2,
		},
	}
	sampleDocument := protocol.TextDocumentIdentifier{URI: "sampleURI"}

	c.currentActionRanges.actions = map[uuid.UUID]map[protocol.TextDocumentIdentifier]map[protocol.Range][]protocol.CodeAction{
		s.UUID: {
			protocol.TextDocumentIdentifier{URI: "sampleURI"}: {
				sampleRange: {
					protocol.CodeAction{
						Title: "sampleTitle",
						Kind:  _codeActionKind,
						Command: &protocol.Command{
							Title:   "sampleTitle",
							Command: "sampleCommand",
							Arguments: []interface{}{
								struct {
									document      protocol.TextDocumentIdentifier
									interfaceName string
								}{
									document:      protocol.TextDocumentIdentifier{URI: "sampleURI"},
									interfaceName: "sampleInterfaceName",
								},
							},
						},
					},
				},
			},
		},
	}

	t.Run("single line range", func(t *testing.T) {
		params := &protocol.CodeActionParams{TextDocument: sampleDocument, Range: sampleRange}
		result := &[]protocol.CodeAction{}
		err := c.codeAction(ctx, params, result)
		assert.NoError(t, err)
		for _, element := range c.currentActionRanges.actions[s.UUID][sampleDocument][sampleRange] {
			assert.Contains(t, *result, element)
		}
	})

	t.Run("multi line", func(t *testing.T) {
		multilineRange := protocol.Range{
			Start: protocol.Position{
				Line:      1,
				Character: 1,
			},
			End: protocol.Position{
				Line:      3,
				Character: 2,
			},
		}

		params := &protocol.CodeActionParams{TextDocument: sampleDocument, Range: multilineRange}
		result := &[]protocol.CodeAction{}
		err := c.codeAction(ctx, params, result)
		assert.NoError(t, err)
		for _, element := range c.currentActionRanges.actions[s.UUID][sampleDocument][sampleRange] {
			assert.Contains(t, *result, element)
		}
	})

	t.Run("no matches", func(t *testing.T) {
		params := &protocol.CodeActionParams{TextDocument: sampleDocument, Range: protocol.Range{Start: protocol.Position{Line: 2, Character: 2}, End: protocol.Position{Line: 2, Character: 3}}}
		result := &[]protocol.CodeAction{}
		err := c.codeAction(ctx, params, result)
		assert.NoError(t, err)
		assert.Len(t, *result, 0)
	})
}

func TestCodeLens(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	sessionRepository := repositorymock.NewMockRepository(ctrl)
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	s.Monorepo = _monorepoNameGoCode
	sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil).AnyTimes()

	documents := docsyncmock.NewMockController(ctrl)
	sampleDoc := protocol.TextDocumentItem{URI: "sampleURI"}
	documents.EXPECT().GetTextDocument(gomock.Any(), gomock.Any()).Return(sampleDoc, nil).AnyTimes()

	c := controller{
		currentActionRanges: newActionRangeStore(),
		sessions:            sessionRepository,
		documents:           documents,
	}

	sampleRange := protocol.Range{
		Start: protocol.Position{
			Line:      1,
			Character: 1,
		},
		End: protocol.Position{
			Line:      1,
			Character: 2,
		},
	}
	sampleDocument := protocol.TextDocumentIdentifier{URI: "sampleURI"}

	c.currentActionRanges.codeLenses = map[uuid.UUID]map[protocol.TextDocumentIdentifier][]protocol.CodeLens{
		s.UUID: {
			protocol.TextDocumentIdentifier{URI: "sampleURI"}: {
				protocol.CodeLens{
					Range: sampleRange,
					Command: &protocol.Command{
						Title:   "sampleTitle",
						Command: "sampleCommand",
						Arguments: []interface{}{
							struct {
								document      protocol.TextDocumentIdentifier
								interfaceName string
							}{
								document:      protocol.TextDocumentIdentifier{URI: "sampleURI"},
								interfaceName: "sampleInterfaceName",
							},
						},
					},
				},
				protocol.CodeLens{
					Range: sampleRange,
					Command: &protocol.Command{
						Title:   "sampleTitle2",
						Command: "sampleCommand2",
						Arguments: []interface{}{
							struct {
								document      protocol.TextDocumentIdentifier
								interfaceName string
							}{
								document:      protocol.TextDocumentIdentifier{URI: "sampleURI"},
								interfaceName: "sampleInterfaceName",
							},
						},
					},
				},
			},
		},
	}

	params := &protocol.CodeLensParams{TextDocument: sampleDocument}
	result := &[]protocol.CodeLens{}
	err := c.codeLens(ctx, params, result)
	assert.NoError(t, err)
	for _, element := range c.currentActionRanges.codeLenses[s.UUID][sampleDocument] {
		assert.ElementsMatch(t, *result, element)
	}
}

func TestExecuteCommand(t *testing.T) {

	ctrl := gomock.NewController(t)

	sessionRepository := repositorymock.NewMockRepository(ctrl)
	ideGatewayMock := ideclientmock.NewMockGateway(ctrl)
	ideGatewayMock.EXPECT().WorkDoneProgressCreate(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	ideGatewayMock.EXPECT().Progress(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	s := &entity.Session{
		UUID: factory.UUID(),
	}
	s.Monorepo = _monorepoNameGoCode
	sessionRepository.EXPECT().GetFromContext(gomock.Any()).Return(s, nil).AnyTimes()

	action1 := quickactionmock.NewMockAction(ctrl)
	validArgs := []byte(`{"document": {"uri": "sampleURI"}, "interfaceName": "sampleInterfaceName"}`)
	commandStr := "sampleCommand"

	t.Run("valid run", func(t *testing.T) {
		c := controller{
			currentActionRanges: newActionRangeStore(),
			sessions:            sessionRepository,
			pendingActionRuns:   newInProgressActionStore(),
			pendingCmds:         make(map[protocol.ProgressToken]context.CancelFunc),
			enabledActions:      map[uuid.UUID][]action.Action{s.UUID: {action1}},
			ideGateway:          ideGatewayMock,
		}
		ctx := context.Background()
		cancelCtx, cancelF := context.WithCancel(ctx)
		defer cancelF()

		progressInfoParams := &action.ProgressInfoParams{
			Title:   "Sample cmd",
			Message: "Running Sample Cmd",
		}
		action1.EXPECT().CommandName().Return(commandStr).AnyTimes()
		action1.EXPECT().Execute(cancelCtx, gomock.Any(), gomock.Any()).Return(nil)
		action1.EXPECT().ProvideWorkDoneProgressParams(ctx, gomock.Any(), gomock.Any()).Return(progressInfoParams, nil)

		params := &protocol.ExecuteCommandParams{
			Command:   commandStr,
			Arguments: []interface{}{validArgs},
		}
		err := c.executeCommand(ctx, params)
		assert.NoError(t, err)
	})

	t.Run("valid run workdone progress disabled", func(t *testing.T) {
		c := controller{
			currentActionRanges: newActionRangeStore(),
			sessions:            sessionRepository,
			pendingActionRuns:   newInProgressActionStore(),
			pendingCmds:         make(map[protocol.ProgressToken]context.CancelFunc),
			enabledActions:      map[uuid.UUID][]action.Action{s.UUID: {action1}},
			ideGateway:          ideGatewayMock,
		}

		ctx := context.Background()
		cancelCtx, cancelF := context.WithCancel(ctx)
		defer cancelF()
		progressInfoParams := &action.ProgressInfoParams{
			Title:   "Sample cmd",
			Message: "Running Sample Cmd",
		}

		action1.EXPECT().CommandName().Return(commandStr).AnyTimes()
		action1.EXPECT().Execute(cancelCtx, gomock.Any(), gomock.Any()).Return(nil)
		action1.EXPECT().ProvideWorkDoneProgressParams(ctx, gomock.Any(), gomock.Any()).Return(progressInfoParams, nil)

		params := &protocol.ExecuteCommandParams{
			Command:   commandStr,
			Arguments: []interface{}{validArgs},
		}
		err := c.executeCommand(ctx, params)
		assert.NoError(t, err)
	})

	t.Run("workdone progress error", func(t *testing.T) {
		c := controller{
			currentActionRanges: newActionRangeStore(),
			sessions:            sessionRepository,
			pendingActionRuns:   newInProgressActionStore(),
			pendingCmds:         make(map[protocol.ProgressToken]context.CancelFunc),
			enabledActions:      map[uuid.UUID][]action.Action{s.UUID: {action1}},
			ideGateway:          ideGatewayMock,
		}

		ctx := context.Background()
		action1.EXPECT().CommandName().Return(commandStr).AnyTimes()
		action1.EXPECT().ProvideWorkDoneProgressParams(ctx, gomock.Any(), gomock.Any()).Return(nil, errors.New("workdone error"))

		params := &protocol.ExecuteCommandParams{
			Command:   commandStr,
			Arguments: []interface{}{validArgs},
		}
		err := c.executeCommand(ctx, params)
		assert.Error(t, err)
	})

	t.Run("different command", func(t *testing.T) {
		c := controller{
			currentActionRanges: newActionRangeStore(),
			sessions:            sessionRepository,
			enabledActions:      map[uuid.UUID][]action.Action{s.UUID: {action1}},
			pendingActionRuns:   newInProgressActionStore(),
			pendingCmds:         make(map[protocol.ProgressToken]context.CancelFunc),
		}
		ctx := context.Background()
		action1.EXPECT().CommandName().Return(commandStr).AnyTimes()

		params := &protocol.ExecuteCommandParams{
			Command:   "otherCommand",
			Arguments: []interface{}{validArgs},
		}
		err := c.executeCommand(ctx, params)
		assert.NoError(t, err)
	})

	t.Run("extra arguments", func(t *testing.T) {
		c := controller{
			currentActionRanges: newActionRangeStore(),
			pendingActionRuns:   newInProgressActionStore(),
			sessions:            sessionRepository,
			enabledActions:      map[uuid.UUID][]action.Action{s.UUID: {action1}},
			pendingCmds:         make(map[protocol.ProgressToken]context.CancelFunc),
		}
		ctx := context.Background()
		action1.EXPECT().CommandName().Return(commandStr).AnyTimes()

		params := &protocol.ExecuteCommandParams{
			Command:   commandStr,
			Arguments: []interface{}{[]byte(`{"arg1": "value1"}`), []byte(`{"arg2": "value2"}`)},
		}
		err := c.executeCommand(ctx, params)
		assert.Error(t, err)
	})

	t.Run("invalid arg type", func(t *testing.T) {
		c := controller{
			currentActionRanges: newActionRangeStore(),
			sessions:            sessionRepository,
			pendingActionRuns:   newInProgressActionStore(),
			pendingCmds:         make(map[protocol.ProgressToken]context.CancelFunc),
			enabledActions:      map[uuid.UUID][]action.Action{s.UUID: {action1}},
		}
		ctx := context.Background()
		cancelCtx, cancelF := context.WithCancel(ctx)
		defer cancelF()
		action1.EXPECT().CommandName().Return(commandStr).AnyTimes()

		params := &protocol.ExecuteCommandParams{
			Command:   commandStr,
			Arguments: []interface{}{},
		}
		err := c.executeCommand(cancelCtx, params)
		assert.Error(t, err)
	})

	t.Run("already running command", func(t *testing.T) {
		c := controller{
			currentActionRanges: newActionRangeStore(),
			sessions:            sessionRepository,
			pendingActionRuns:   newInProgressActionStore(),
			pendingCmds:         make(map[protocol.ProgressToken]context.CancelFunc),
			enabledActions:      map[uuid.UUID][]action.Action{s.UUID: {action1}},
		}
		ctx := context.Background()
		cancelCtx, cancelF := context.WithCancel(ctx)
		defer cancelF()
		action1.EXPECT().CommandName().Return(commandStr).AnyTimes()
		c.pendingActionRuns.AddInProgressAction(s.UUID, commandStr, string(validArgs))

		params := &protocol.ExecuteCommandParams{
			Command:   commandStr,
			Arguments: []interface{}{validArgs},
		}
		err := c.executeCommand(cancelCtx, params)
		assert.NoError(t, err)
	})
}

func TestWorkDoneProgressCancel(t *testing.T) {

	t.Run("valid request", func(t *testing.T) {
		c := controller{
			logger:      zap.NewNop().Sugar(),
			pendingCmds: make(map[protocol.ProgressToken]context.CancelFunc),
		}
		ctx := context.Background()
		_, cancelFunc := context.WithCancel(ctx)
		progressToken := protocol.NewProgressToken("Sample-token")
		c.pendingCmds[*progressToken] = cancelFunc

		cancelParams := protocol.WorkDoneProgressCancelParams{
			Token: *progressToken,
		}

		err := c.workDoneProgressCancel(ctx, &cancelParams)
		assert.NoError(t, err)
		assert.Len(t, c.pendingCmds, 0)
	})

	t.Run("non existent cancel request", func(t *testing.T) {
		c := controller{
			logger:      zap.NewNop().Sugar(),
			pendingCmds: make(map[protocol.ProgressToken]context.CancelFunc),
		}
		ctx := context.Background()
		_, cancelFunc := context.WithCancel(ctx)
		defer cancelFunc()
		progressToken := protocol.NewProgressToken("Sample-token")
		c.pendingCmds[*progressToken] = cancelFunc

		cancelParams := protocol.WorkDoneProgressCancelParams{
			Token: *protocol.NewProgressToken("non-existent-token"),
		}

		err := c.workDoneProgressCancel(ctx, &cancelParams)
		assert.NoError(t, err)
		assert.Len(t, c.pendingCmds, 1)
	})
}

func TestStartWorkDoneProgressMessage(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	ideGatewayMock := ideclientmock.NewMockGateway(ctrl)

	t.Run("enabled", func(t *testing.T) {
		c := controller{}

		params := &action.ExecuteParams{
			IdeGateway:    ideGatewayMock,
			ProgressToken: protocol.NewProgressToken("dummytoken"),
		}
		progressInfoParams := &action.ProgressInfoParams{
			Title:   "Sample cmd",
			Message: "Running Sample Cmd",
		}
		ideGatewayMock.EXPECT().WorkDoneProgressCreate(gomock.Any(), gomock.Any()).Return(nil)
		ideGatewayMock.EXPECT().Progress(gomock.Any(), gomock.Any()).Return(nil)
		err := c.startWorkDoneProgressMessage(ctx, params, progressInfoParams)
		assert.NoError(t, err)
	})

	t.Run("disabled", func(t *testing.T) {
		c := controller{}
		params := &action.ExecuteParams{}
		err := c.startWorkDoneProgressMessage(ctx, params, nil)
		assert.NoError(t, err)
	})
}

func TestEndWorkDoneProgressMessage(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	ideGatewayMock := ideclientmock.NewMockGateway(ctrl)

	t.Run("enabled", func(t *testing.T) {
		c := controller{}

		params := &action.ExecuteParams{
			IdeGateway:    ideGatewayMock,
			ProgressToken: protocol.NewProgressToken("dummytoken"),
		}
		progressInfoParams := &action.ProgressInfoParams{
			Title:   "Sample cmd",
			Message: "Running Sample Cmd",
		}
		ideGatewayMock.EXPECT().Progress(gomock.Any(), gomock.Any()).Return(nil)
		err := c.endWorkDoneProgressMessage(ctx, params, progressInfoParams)
		assert.NoError(t, err)
	})

	t.Run("disabled", func(t *testing.T) {
		c := controller{}
		params := &action.ExecuteParams{}
		err := c.endWorkDoneProgressMessage(ctx, params, nil)
		assert.NoError(t, err)
	})

}

func getSampleMatches() []interface{} {
	return []interface{}{
		mapper.CodeActionWithRange{
			Range: protocol.Range{
				Start: protocol.Position{
					Line:      1,
					Character: 1,
				},
				End: protocol.Position{
					Line:      1,
					Character: 2,
				},
			},
			CodeAction: protocol.CodeAction{
				Title: "sample",
				Kind:  _codeActionKind,
				Command: &protocol.Command{
					Title:   "sample",
					Command: "sample",
				},
			},
		},
		protocol.CodeLens{
			Range: protocol.Range{
				Start: protocol.Position{
					Line:      1,
					Character: 1,
				},
				End: protocol.Position{
					Line:      1,
					Character: 2,
				},
			},
			Command: &protocol.Command{
				Title:   "sampleCodeLensCommand",
				Command: "sampleCodeLensCommand",
			},
		},
	}
}
