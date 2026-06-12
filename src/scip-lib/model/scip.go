package model

import (
	"fmt"
	"strings"

	"github.com/sourcegraph/scip/bindings/go/scip"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// ScipPackage represents a package in the SCIP Index
type ScipPackage struct {
	Manager string
	Name    string
	Version string
}

// ID returns the ID of the package
func (x *ScipPackage) ID() string {
	return fmt.Sprintf("%s %s %s", x.Manager, x.Name, x.Version)
}

// Document represents a document in the SCIP Index
type Document struct {
	Language     string
	RelativePath string
	Occurrences  []*Occurrence
	Symbols      []*SymbolInformation
	SymbolMap    map[string]*SymbolInformation
	Text         string
	Diagnostics  []*protocol.Diagnostic
}

// Diagnostic represents a warning or error in a document
type Diagnostic struct {
	Severity scip.Severity
	Code     string
	Message  string
	Source   string
	Tags     []scip.DiagnosticTag
}

// Occurrence represents a symbol occurrence in a document
type Occurrence struct {
	Range                 []int32
	Symbol                string
	SymbolRoles           int32
	OverrideDocumentation []string
	SyntaxKind            scip.SyntaxKind
	Diagnostics           []*Diagnostic
	EnclosingRange        []int32
}

// Relationship represents a relationship between two symbols
type Relationship struct {
	Symbol           string
	IsReference      bool
	IsImplementation bool
	IsTypeDefinition bool
	IsDefinition     bool
}

// SymbolInformation represents a symbol in a document
type SymbolInformation struct {
	Symbol                 string
	Documentation          []string
	Relationships          []*Relationship
	Kind                   scip.SymbolInformation_Kind
	DisplayName            string
	SignatureDocumentation *Document
	EnclosingSymbol        string
}

// SymbolOccurrence represents a symbol occurrence in a document
type SymbolOccurrence struct {
	Location   uri.URI
	Occurrence *Occurrence
	Info       *SymbolInformation
}

// Descriptor represents a symbol descriptor
type Descriptor struct {
	Name          string
	Suffix        scip.Descriptor_Suffix
	Disambiguator string
}

// ParseScipSymbol parses a symbol string into a Symbol, ensuring the package name is set
func ParseScipSymbol(sy string) (*scip.Symbol, error) {
	parsed, err := scip.ParseSymbol(sy)
	if err != nil {
		return nil, err
	}

	// IDE-698: Package name is unset in Fievel
	if parsed.Package != nil && parsed.Package.Name == "" {
		// Join the Symbol descriptors together
		parts := make([]string, 0)
		for _, part := range parsed.Descriptors {
			if part.Suffix == scip.Descriptor_Namespace {
				parts = append(parts, part.Name)
			}
		}

		parsed.Package.Name = strings.Join(parts, "/")
	}

	defaultPackageVersion := "."
	if parsed.Package != nil && parsed.Package.Version == "" {
		parsed.Package.Version = defaultPackageVersion
	}

	return parsed, nil
}

// SymbolChild represents a child symbol of a class/interface/struct
type SymbolChild struct {
	Info       *SymbolInformation
	Descriptor Descriptor
	// Children contains nested children (e.g., method parameters)
	Children []*SymbolChild
}

// ParseScipSymbolToDisplayName parses a symbol and return the displayName
func ParseScipSymbolToDisplayName(symbolStr string) string {
	symbol, err := scip.ParseSymbol(symbolStr)
	if symbol == nil || err != nil || len(symbol.Descriptors) == 0 {
		return symbolStr
	}

	lastDescriptor := symbol.Descriptors[len(symbol.Descriptors)-1]
	return lastDescriptor.Name
}
