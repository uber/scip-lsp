package mapper

import (
	"github.com/sourcegraph/scip/bindings/go/scip"
	"github.com/uber/scip-lsp/src/scip-lib/model"
	"go.lsp.dev/protocol"
)

const _ulspDocumentSymbolPrefix = "[uLSP]"

// ScipToProtocolRange maps a SCIP range array to a LSP protocol Range
func ScipToProtocolRange(rng []int32) protocol.Range {
	parsed, err := scip.NewRange(rng)
	if err != nil {
		return protocol.Range{}
	}
	return protocol.Range{
		Start: ScipToProtocolPosition(parsed.Start),
		End:   ScipToProtocolPosition(parsed.End),
	}
}

// ScipToProtocolPosition maps a SCIP position to an LSP position
func ScipToProtocolPosition(pos scip.Position) protocol.Position {
	return protocol.Position{
		Line:      uint32(pos.Line),
		Character: uint32(pos.Character),
	}
}

// ScipOccurrenceToLocation converts from scip.Range to protocol.Location.
func ScipOccurrenceToLocation(uri protocol.URI, occ *model.Occurrence) *protocol.Location {
	return &protocol.Location{
		URI:   uri,
		Range: ScipToProtocolRange(occ.Range),
	}
}

// ScipOccurrenceToLocationLink converts from scip.Range to protocol.LocationLink. The last argument
// allows the caller to define a selectionRange of the origin of the link.
func ScipOccurrenceToLocationLink(uri protocol.URI, occ *model.Occurrence, origSelection *protocol.Range) *protocol.LocationLink {
	return &protocol.LocationLink{
		OriginSelectionRange: origSelection,
		TargetURI:            uri,
		TargetRange:          ScipToProtocolRange(occ.Range),
		TargetSelectionRange: ScipToProtocolRange(occ.Range),
	}
}

// ScipSymbolInformationToDocumentSymbol converts from scip.SymbolInformation to protocol.DocumentSymbol.
func ScipSymbolInformationToDocumentSymbol(symbolInfo *model.SymbolInformation, occ *model.Occurrence) *protocol.DocumentSymbol {
	return &protocol.DocumentSymbol{
		Name:           symbolInfo.DisplayName,
		Detail:         _ulspDocumentSymbolPrefix + symbolInfo.DisplayName,
		Kind:           ScipSymbolKindToDocumentSymbolKind(symbolInfo.Kind),
		Range:          ScipToProtocolRange(occ.Range),
		SelectionRange: ScipToProtocolRange(occ.Range),
	}
}

// ScipSymbolKindToDocumentSymbolKind converts from scip.SymbolInformation_Kind to protocol.SymbolKind.
func ScipSymbolKindToDocumentSymbolKind(symbolKind scip.SymbolInformation_Kind) protocol.SymbolKind {

    symKindMap := map[scip.SymbolInformation_Kind]protocol.SymbolKind{
        scip.SymbolInformation_Function:       protocol.SymbolKindFunction,
        scip.SymbolInformation_Delegate:       protocol.SymbolKindFunction,
        scip.SymbolInformation_Macro:          protocol.SymbolKindFunction,
        scip.SymbolInformation_File:           protocol.SymbolKindFile,
        scip.SymbolInformation_Module:         protocol.SymbolKindModule,
        scip.SymbolInformation_Library:        protocol.SymbolKindModule,
        scip.SymbolInformation_Namespace:      protocol.SymbolKindNamespace,
        scip.SymbolInformation_Package:        protocol.SymbolKindPackage,
        scip.SymbolInformation_PackageObject:  protocol.SymbolKindPackage,
        scip.SymbolInformation_Class:          protocol.SymbolKindClass,
        scip.SymbolInformation_TypeClass:      protocol.SymbolKindClass,
        scip.SymbolInformation_Mixin:          protocol.SymbolKindClass,
        scip.SymbolInformation_SingletonClass: protocol.SymbolKindClass,
        scip.SymbolInformation_Contract:       protocol.SymbolKindClass,
        scip.SymbolInformation_Method:         protocol.SymbolKindMethod,
        scip.SymbolInformation_MethodReceiver: protocol.SymbolKindMethod,
        scip.SymbolInformation_AbstractMethod: protocol.SymbolKindMethod,
        scip.SymbolInformation_StaticMethod:   protocol.SymbolKindMethod,
        scip.SymbolInformation_SingletonMethod:   protocol.SymbolKindMethod,
        scip.SymbolInformation_MethodSpecification: protocol.SymbolKindMethod,
        scip.SymbolInformation_MethodAlias:       protocol.SymbolKindMethod,
        scip.SymbolInformation_TraitMethod:       protocol.SymbolKindMethod,
        scip.SymbolInformation_ProtocolMethod:    protocol.SymbolKindMethod,
        scip.SymbolInformation_PureVirtualMethod: protocol.SymbolKindMethod,
        scip.SymbolInformation_TypeClassMethod:   protocol.SymbolKindMethod,
        scip.SymbolInformation_Property:       protocol.SymbolKindProperty,
        scip.SymbolInformation_Getter:         protocol.SymbolKindProperty,
        scip.SymbolInformation_Setter:         protocol.SymbolKindProperty,
        scip.SymbolInformation_Accessor:       protocol.SymbolKindProperty,
        scip.SymbolInformation_StaticProperty: protocol.SymbolKindProperty,
        scip.SymbolInformation_Subscript:      protocol.SymbolKindProperty,
        scip.SymbolInformation_Attribute:      protocol.SymbolKindProperty,
        scip.SymbolInformation_Field:          protocol.SymbolKindField,
        scip.SymbolInformation_StaticField:    protocol.SymbolKindField,
        scip.SymbolInformation_StaticDataMember: protocol.SymbolKindField,
        scip.SymbolInformation_Constructor:    protocol.SymbolKindConstructor,
        scip.SymbolInformation_Enum:           protocol.SymbolKindEnum,
        scip.SymbolInformation_EnumMember:     protocol.SymbolKindEnumMember,
        scip.SymbolInformation_Interface:      protocol.SymbolKindInterface,
        scip.SymbolInformation_Trait:          protocol.SymbolKindInterface,
        scip.SymbolInformation_Protocol:       protocol.SymbolKindInterface,
        scip.SymbolInformation_Variable:       protocol.SymbolKindVariable,
        scip.SymbolInformation_Parameter:      protocol.SymbolKindVariable,
        scip.SymbolInformation_ParameterLabel: protocol.SymbolKindVariable,
        scip.SymbolInformation_SelfParameter:  protocol.SymbolKindVariable,
        scip.SymbolInformation_ThisParameter:  protocol.SymbolKindVariable,
        scip.SymbolInformation_StaticVariable: protocol.SymbolKindVariable,
        scip.SymbolInformation_Value:          protocol.SymbolKindVariable,
        scip.SymbolInformation_Constant:       protocol.SymbolKindConstant,
        scip.SymbolInformation_String:         protocol.SymbolKindString,
        scip.SymbolInformation_Number:         protocol.SymbolKindNumber,
        scip.SymbolInformation_Boolean:        protocol.SymbolKindBoolean,
        scip.SymbolInformation_Array:          protocol.SymbolKindArray,
        scip.SymbolInformation_Object:         protocol.SymbolKindObject,
        scip.SymbolInformation_Key:            protocol.SymbolKindKey,
        scip.SymbolInformation_Null:           protocol.SymbolKindNull,
        scip.SymbolInformation_Struct:         protocol.SymbolKindStruct,
        scip.SymbolInformation_Union:          protocol.SymbolKindStruct,
        scip.SymbolInformation_Message:        protocol.SymbolKindStruct,
        scip.SymbolInformation_Event:          protocol.SymbolKindEvent,
        scip.SymbolInformation_StaticEvent:    protocol.SymbolKindEvent,
        scip.SymbolInformation_Operator:       protocol.SymbolKindOperator,
        scip.SymbolInformation_Type:           protocol.SymbolKindTypeParameter,
        scip.SymbolInformation_TypeAlias:      protocol.SymbolKindTypeParameter,
        scip.SymbolInformation_TypeParameter:  protocol.SymbolKindTypeParameter,
        scip.SymbolInformation_AssociatedType: protocol.SymbolKindTypeParameter,
        scip.SymbolInformation_TypeFamily:     protocol.SymbolKindTypeParameter,
    }

    kind, ok := symKindMap[symbolKind]
    if !ok {
        return protocol.SymbolKindNull
    }
    return kind
}
