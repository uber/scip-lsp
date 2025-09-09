// Package entity contains the domain logic for the ulsp-daemon service.
package entity

import (
	"slices"

	"github.com/gofrs/uuid"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
)

type keyType string

// SessionContextKey indicates the key to be used to identify the session UUID in the context.
const SessionContextKey keyType = "SessionUUID"

// MonorepoConfigKey is the key that contains monorepo specific configuration.
const MonorepoConfigKey = "monorepos"

const _javaLang = "java"
const _scalaLang = "scala"

// UlspDaemon placeholder entity.
type UlspDaemon struct {
	Name string    `json:"name" zap:"name"`
	UUID uuid.UUID `json:"uuid" zap:"uuid"`
}

// String implements fmt.Stringer.
func (f *UlspDaemon) String() string {
	return ""
}

// RequestKey implements logger.RequestMarshaler.
func (f *UlspDaemon) RequestKey() string {
	return "request_ulspdaemon"
}

// ResponseKey implements logger.ResponseMarshaler.
func (f *UlspDaemon) ResponseKey() string {
	return "response_ulspdaemon"
}

// Session entity representing a single IDE session.
type Session struct {
	UUID             uuid.UUID                  `json:"uuid" zap:"uuid"`
	InitializeParams *protocol.InitializeParams `json:"-" zap:"-"`
	Conn             *jsonrpc2.Conn             `json:"-" zap:"-"`
	WorkspaceRoot    string                     `json:"workspaceRoot" zap:"workspaceRoot"`
	Monorepo         MonorepoName               `json:"monorepo" zap:"monorepo"`
	Env              []string                   `json:"-" zap:"-"`
	UlspEnabled      bool                       `json:"ulspEnabled" zap:"ulspEnabled"`
}

// TextDocumentIdenfitierWithSession is a wrapper around TextDocumentIdentifier to include the session UUID.
type TextDocumentIdenfitierWithSession struct {
	Document    protocol.TextDocumentIdentifier
	SessionUUID uuid.UUID
}

// MonorepoName for supported Uber monorepos.
type MonorepoName string

// MonorepoConfigs contain the config entries that differ between monorepos
type MonorepoConfigs map[MonorepoName]MonorepoConfigEntry

// MonorepoConfigEntry defines the properties and types of each config entry
type MonorepoConfigEntry struct {
	Bazel                string            `yaml:"bazel"`
	Aspects              []string          `yaml:"aspects"`
	Scip                 ScipConfig        `yaml:"scip"`
	ProjectViewPaths     []string          `yaml:"projectViewPaths"`
	BSPVersionOverride   string            `yaml:"bspVersionOverride"`
	Formatters           []FormatterConfig `yaml:"formatters"`
	Languages            []string          `yaml:"languages"`
	BuildEnvOverrides    []string          `yaml:"buildEnvOverrides"`
	RegistryFeatureFlags map[string]bool   `yaml:"registryFeatureFlags"`
}

// ScipConfig configures enabled SCIP features
type ScipConfig struct {
	LoadFromBazel       bool     `yaml:"loadFromBazel"`
	LoadFromDirectories bool     `yaml:"loadFromDirectories"`
	Directories         []string `yaml:"directories"`
}

// FormatterConfig configures a formatter to be run based on language and filter patterns.
// If a document matches multiple patterns, multiple formatters will be run in the order listed.
type FormatterConfig struct {
	// Relative path within the monorepo to the formatter to be run.
	BinaryPath string `yaml:"binaryPath"`
	// Language (e.g. Go) which will be matched based on TextDocumentIdentifier.
	Language protocol.LanguageIdentifier `yaml:"language"`
	// Glob patterns to be used for selecting relevant files.
	FilterPatterns []string `yaml:"filterPatterns"`
}

// ClientName identifies the name that the will be set in the initialization parameters for a given client.
type ClientName string

const (
	// ClientNameVSCode is the name of the VSCode client.
	ClientNameVSCode ClientName = "Visual Studio Code"
	// ClientNameCursor is the name of the Cursor client.
	ClientNameCursor ClientName = "Cursor"
)

// IsVSCodeBased returns true if the client is a VS Code based client.
func (c ClientName) IsVSCodeBased() bool {
	return c == ClientNameVSCode || c == ClientNameCursor
}

func (mce MonorepoConfigEntry) EnableJavaSupport() bool {
	return slices.Contains(mce.Languages, _javaLang)
}

func (mce MonorepoConfigEntry) EnableScalaSupport() bool {
	return slices.Contains(mce.Languages, _scalaLang)
}

func (configs MonorepoConfigs) RelevantJavaRepos() map[MonorepoName]struct{} {
	return configs.relevantRepos(func(entry MonorepoConfigEntry) bool {
		return entry.EnableJavaSupport()
	})
}

func (configs MonorepoConfigs) RelevantScalaRepos() map[MonorepoName]struct{} {
	return configs.relevantRepos(func(entry MonorepoConfigEntry) bool {
		return entry.EnableScalaSupport()
	})
}

func (configs MonorepoConfigs) relevantRepos(predicate func(entry MonorepoConfigEntry) bool) map[MonorepoName]struct{} {
	relevantRepos := make(map[MonorepoName]struct{})
	for monorepoName, monorepoConfig := range configs {
		if predicate(monorepoConfig) {
			relevantRepos[monorepoName] = struct{}{}
		}
	}

	return relevantRepos
}
