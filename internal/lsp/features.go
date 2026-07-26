package lsp

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/StevenBuglione/spice/annotation"
	"github.com/StevenBuglione/spice/compiler/diagnostic"
	compilerservice "github.com/StevenBuglione/spice/compiler/service"
)

type textDocumentPositionParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     protocolPosition       `json:"position"`
}

type codeActionParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Range        protocolRange          `json:"range"`
	Context      struct {
		Diagnostics []protocolDiagnostic `json:"diagnostics"`
		Only        []string             `json:"only,omitempty"`
	} `json:"context"`
}

type completionItem struct {
	Label            string         `json:"label"`
	Kind             int            `json:"kind,omitempty"`
	Detail           string         `json:"detail,omitempty"`
	Documentation    *markupContent `json:"documentation,omitempty"`
	SortText         string         `json:"sortText,omitempty"`
	FilterText       string         `json:"filterText,omitempty"`
	InsertTextFormat int            `json:"insertTextFormat,omitempty"`
	TextEdit         protocolEdit   `json:"textEdit"`
}

type markupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type protocolEdit struct {
	Range   protocolRange `json:"range"`
	NewText string        `json:"newText"`
}

type completionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []completionItem `json:"items"`
}

type hoverResult struct {
	Contents markupContent  `json:"contents"`
	Range    *protocolRange `json:"range,omitempty"`
}

type protocolCodeAction struct {
	Title       string                `json:"title"`
	Kind        string                `json:"kind"`
	IsPreferred bool                  `json:"isPreferred"`
	Edit        protocolWorkspaceEdit `json:"edit"`
}

type protocolWorkspaceEdit struct {
	DocumentChanges []protocolDocumentEdit `json:"documentChanges"`
}

type protocolDocumentEdit struct {
	TextDocument protocolOptionalVersionedDocument `json:"textDocument"`
	Edits        []protocolEdit                    `json:"edits"`
}

type protocolOptionalVersionedDocument struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

type metadataView struct {
	definitions    []compilerservice.AnnotationDefinition
	modules        []compilerservice.Module
	configurations []compilerservice.Configuration
	actions        []diagnostic.SuggestedFix
}

func (server *Server) completion(message rpcMessage) error {
	if !message.request() {
		return nil
	}
	var params textDocumentPositionParams
	if err := decodeParams(message.Params, &params); err != nil {
		return server.writer.failure(
			message.ID,
			invalidParamsCode,
			err.Error(),
		)
	}
	source, metadata, found := server.featureSnapshot(
		params.TextDocument.URI,
	)
	if !found {
		return server.writer.response(
			message.ID,
			completionList{Items: []completionItem{}},
		)
	}
	offset, valid := byteOffset(source.content, params.Position)
	if !valid {
		return server.writer.failure(
			message.ID,
			invalidParamsCode,
			"completion position is outside the current document",
		)
	}
	items := completionItems(source.content, offset, metadata)
	return server.writer.response(
		message.ID,
		completionList{Items: items},
	)
}

func completionItems(
	content []byte,
	offset int,
	metadata metadataView,
) []completionItem {
	context := inspectCompletionContext(content, offset)
	switch context.kind {
	case completionNone:
		return []completionItem{}
	case completionAnnotation:
		return annotationCompletionItems(context, metadata.definitions, content)
	case completionArgument:
		return argumentCompletionItems(context, metadata, content)
	case completionConfiguration:
		return configurationCompletionItems(context, metadata, content)
	default:
		return []completionItem{}
	}
}

type completionContextKind uint8

const (
	completionNone completionContextKind = iota
	completionAnnotation
	completionArgument
	completionConfiguration
)

type completionContext struct {
	kind           completionContextKind
	annotation     string
	argument       string
	start          int
	end            int
	rawAnnotation  bool
	insideString   bool
	existingPrefix string
}

func inspectCompletionContext(content []byte, offset int) completionContext {
	lineStart, _, found := contentLineAtOffset(content, offset)
	if !found {
		return completionContext{kind: completionNone, start: offset, end: offset}
	}
	prefix := content[lineStart:offset]
	at := strings.LastIndexByte(string(prefix), '@')
	if at >= 0 {
		absoluteAt := lineStart + at
		raw, valid := annotationPrefix(prefix[:at])
		if !valid {
			return completionContext{
				kind:  completionNone,
				start: offset,
				end:   offset,
			}
		}
		after := string(content[absoluteAt+1 : offset])
		open := strings.IndexByte(after, '(')
		if open < 0 {
			return completionContext{
				kind:           completionAnnotation,
				start:          absoluteAt,
				end:            offset,
				rawAnnotation:  raw,
				existingPrefix: after,
			}
		}
		annotationName := strings.TrimSpace(after[:open])
		if validAnnotationName(annotationName) {
			argument, start, insideString := currentArgumentContext(
				content,
				absoluteAt+1+open+1,
				offset,
			)
			return completionContext{
				kind:         completionArgument,
				annotation:   annotationName,
				argument:     argument,
				start:        start,
				end:          offset,
				insideString: insideString,
			}
		}
	}
	start := wordStart(content, offset, configurationCharacter)
	return completionContext{
		kind:           completionConfiguration,
		start:          start,
		end:            offset,
		existingPrefix: string(content[start:offset]),
		insideString:   insideStringLiteral(content[lineStart:offset]),
	}
}

func annotationPrefix(prefix []byte) (raw bool, valid bool) {
	trimmed := strings.TrimSpace(string(prefix))
	switch trimmed {
	case "":
		return true, true
	case "//":
		return false, true
	default:
		return false, false
	}
}

func validAnnotationName(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character != '.' &&
			character != '_' &&
			!unicode.IsLetter(character) &&
			!unicode.IsDigit(character) {
			return false
		}
	}
	return true
}

func currentArgumentContext(
	content []byte,
	start int,
	offset int,
) (string, int, bool) {
	segment := content[start:offset]
	insideString := insideStringLiteral(segment)
	word := wordStart(content, offset, argumentValueCharacter)
	beforeCursor := string(segment)
	if equals := strings.LastIndexByte(beforeCursor, '='); equals >= 0 {
		beforeEquals := beforeCursor[:equals]
		separator := strings.LastIndexByte(beforeEquals, ',')
		field := strings.TrimSpace(beforeEquals[separator+1:])
		return field, word, insideString
	}
	fieldStart := wordStart(content, offset, annotationCharacter)
	return "", fieldStart, insideString
}

func annotationCompletionItems(
	context completionContext,
	definitions []compilerservice.AnnotationDefinition,
	content []byte,
) []completionItem {
	itemRange := protocolRangeAtOffsets(content, context.start, context.end)
	items := make([]completionItem, 0, len(definitions))
	for _, definition := range definitions {
		if context.existingPrefix != "" &&
			!strings.HasPrefix(
				strings.ToLower(definition.Name),
				strings.ToLower(context.existingPrefix),
			) {
			continue
		}
		insert := "@" + annotationSnippet(definition)
		if context.rawAnnotation {
			insert = "// " + insert
		}
		documentation := markupContent{
			Kind:  "markdown",
			Value: annotationDocumentation(definition),
		}
		items = append(items, completionItem{
			Label:            "@" + definition.Name,
			Kind:             14,
			Detail:           annotationTargetDetail(definition),
			Documentation:    &documentation,
			SortText:         definition.Name,
			FilterText:       "@" + definition.Name,
			InsertTextFormat: 2,
			TextEdit: protocolEdit{
				Range:   itemRange,
				NewText: insert,
			},
		})
	}
	return items
}

func annotationSnippet(
	definition compilerservice.AnnotationDefinition,
) string {
	var required []compilerservice.AnnotationArgument
	for _, argument := range definition.Arguments {
		if argument.Required {
			required = append(required, argument)
		}
	}
	if len(required) == 0 {
		return definition.Name
	}
	values := make([]string, len(required))
	for index, argument := range required {
		value := argumentSnippet(argument, index+1)
		if argument.Positional {
			values[index] = value
		} else {
			values[index] = argument.Name + "=" + value
		}
	}
	return definition.Name + "(" + strings.Join(values, ", ") + ")"
}

func argumentSnippet(
	argument compilerservice.AnnotationArgument,
	tabstop int,
) string {
	if len(argument.AllowedStrings) != 0 {
		return fmt.Sprintf(
			`["${%d:%s}"]`,
			tabstop,
			argument.AllowedStrings[0],
		)
	}
	if slices.Contains(argument.Kinds, annotation.KindList) {
		return fmt.Sprintf(`["${%d:value}"]`, tabstop)
	}
	if slices.Contains(argument.Kinds, annotation.KindBoolean) {
		return fmt.Sprintf("${%d:true}", tabstop)
	}
	if slices.Contains(argument.Kinds, annotation.KindInteger) {
		return fmt.Sprintf("${%d:0}", tabstop)
	}
	return fmt.Sprintf(`"${%d:value}"`, tabstop)
}

func argumentCompletionItems(
	context completionContext,
	metadata metadataView,
	content []byte,
) []completionItem {
	definition, found := annotationDefinition(
		metadata.definitions,
		context.annotation,
	)
	if !found {
		return []completionItem{}
	}
	itemRange := protocolRangeAtOffsets(content, context.start, context.end)
	for _, argument := range definition.Arguments {
		if argument.Name != context.argument ||
			len(argument.AllowedStrings) == 0 {
			continue
		}
		return valueCompletionItems(
			argument.AllowedStrings,
			context,
			itemRange,
			"allowed "+argument.Name+" value",
		)
	}
	if context.annotation == "Module" &&
		context.argument == "allowedDependencies" {
		var values []string
		for _, module := range metadata.modules {
			values = append(values, module.ID)
			for _, named := range module.NamedInterfaces {
				values = append(values, module.ID+"::"+named.Name)
			}
		}
		slices.Sort(values)
		return valueCompletionItems(
			values,
			context,
			itemRange,
			"application module API",
		)
	}
	items := make([]completionItem, 0, len(definition.Arguments))
	for index, argument := range definition.Arguments {
		documentation := markupContent{
			Kind: "markdown",
			Value: fmt.Sprintf(
				"`%s` accepts %s.",
				argument.Name,
				argumentKinds(argument),
			),
		}
		items = append(items, completionItem{
			Label:            argument.Name,
			Kind:             5,
			Detail:           "Spice annotation argument",
			Documentation:    &documentation,
			SortText:         fmt.Sprintf("%03d-%s", index, argument.Name),
			InsertTextFormat: 2,
			TextEdit: protocolEdit{
				Range: itemRange,
				NewText: argument.Name + "=" +
					argumentSnippet(argument, 1),
			},
		})
	}
	return items
}

func valueCompletionItems(
	values []string,
	context completionContext,
	itemRange protocolRange,
	detail string,
) []completionItem {
	items := make([]completionItem, 0, len(values))
	for _, value := range values {
		insert := value
		if !context.insideString {
			insert = `"` + value + `"`
		}
		items = append(items, completionItem{
			Label:    value,
			Kind:     12,
			Detail:   detail,
			SortText: value,
			TextEdit: protocolEdit{Range: itemRange, NewText: insert},
		})
	}
	return items
}

func configurationCompletionItems(
	context completionContext,
	metadata metadataView,
	content []byte,
) []completionItem {
	if !context.insideString {
		return []completionItem{}
	}
	itemRange := protocolRangeAtOffsets(content, context.start, context.end)
	items := make([]completionItem, 0)
	for _, configuration := range metadata.configurations {
		for _, field := range configuration.Fields {
			if context.existingPrefix != "" &&
				!strings.HasPrefix(field.Key, context.existingPrefix) {
				continue
			}
			documentation := markupContent{
				Kind:  "markdown",
				Value: configurationFieldDocumentation(configuration, field),
			}
			items = append(items, completionItem{
				Label:         field.Key,
				Kind:          10,
				Detail:        field.TypeID,
				Documentation: &documentation,
				SortText:      field.Key,
				TextEdit: protocolEdit{
					Range:   itemRange,
					NewText: field.Key,
				},
			})
		}
	}
	sort.SliceStable(items, func(left, right int) bool {
		return items[left].Label < items[right].Label
	})
	return items
}

func (server *Server) hover(message rpcMessage) error {
	if !message.request() {
		return nil
	}
	var params textDocumentPositionParams
	if err := decodeParams(message.Params, &params); err != nil {
		return server.writer.failure(
			message.ID,
			invalidParamsCode,
			err.Error(),
		)
	}
	source, metadata, found := server.featureSnapshot(
		params.TextDocument.URI,
	)
	if !found {
		return server.writer.response(message.ID, nil)
	}
	offset, valid := byteOffset(source.content, params.Position)
	if !valid {
		return server.writer.response(message.ID, nil)
	}
	value, start, end := tokenAt(source.content, offset)
	annotationName := strings.TrimPrefix(value, "@")
	if definition, found := annotationDefinition(
		metadata.definitions,
		annotationName,
	); found {
		return server.writer.response(message.ID, hoverResult{
			Contents: markupContent{
				Kind:  "markdown",
				Value: annotationDocumentation(definition),
			},
			Range: new(protocolRangeAtOffsets(source.content, start, end)),
		})
	}
	for _, configuration := range metadata.configurations {
		for _, field := range configuration.Fields {
			if value == field.Key {
				return server.writer.response(message.ID, hoverResult{
					Contents: markupContent{
						Kind: "markdown",
						Value: configurationFieldDocumentation(
							configuration,
							field,
						),
					},
					Range: new(protocolRangeAtOffsets(
						source.content,
						start,
						end,
					)),
				})
			}
		}
	}
	for _, module := range metadata.modules {
		if value == module.ID {
			return server.writer.response(message.ID, hoverResult{
				Contents: markupContent{
					Kind: "markdown",
					Value: fmt.Sprintf(
						"`%s` owns %d package(s) and exposes %d named interface(s).",
						module.ID,
						len(module.Packages),
						len(module.NamedInterfaces),
					),
				},
				Range: new(protocolRangeAtOffsets(
					source.content,
					start,
					end,
				)),
			})
		}
	}
	return server.writer.response(message.ID, nil)
}

func (server *Server) codeAction(message rpcMessage) error {
	if !message.request() {
		return nil
	}
	var params codeActionParams
	if err := decodeParams(message.Params, &params); err != nil {
		return server.writer.failure(
			message.ID,
			invalidParamsCode,
			err.Error(),
		)
	}
	source, metadata, found := server.featureSnapshot(
		params.TextDocument.URI,
	)
	if !found {
		return server.writer.response(
			message.ID,
			[]protocolCodeAction{},
		)
	}
	actions := server.protocolCodeActions(
		source,
		params.Range,
		metadata.actions,
	)
	return server.writer.response(message.ID, actions)
}

func (server *Server) protocolCodeActions(
	requestDocument document,
	requestRange protocolRange,
	fixes []diagnostic.SuggestedFix,
) []protocolCodeAction {
	var result []protocolCodeAction
	for _, fix := range fixes {
		action, relevant, safe := server.protocolCodeAction(
			requestDocument,
			requestRange,
			fix,
		)
		if safe && relevant {
			result = append(result, action)
		}
	}
	return result
}

func (server *Server) protocolCodeAction(
	requestDocument document,
	requestRange protocolRange,
	fix diagnostic.SuggestedFix,
) (protocolCodeAction, bool, bool) {
	byDocument := make(map[string][]protocolEdit)
	versions := make(map[string]int)
	relevant := false
	server.mu.Lock()
	defer server.mu.Unlock()
	for _, edit := range fix.Edits {
		document := server.documentForEditLocked(edit)
		if document == nil ||
			edit.DocumentVersion == nil ||
			*edit.DocumentVersion != document.version {
			return protocolCodeAction{}, false, false
		}
		converted := protocolEdit{
			Range: protocolRangeFromCompiler(
				edit.Location.Range,
				document.content,
			),
			NewText: edit.NewText,
		}
		if document.uri == requestDocument.uri &&
			rangesOverlap(converted.Range, requestRange) {
			relevant = true
		}
		byDocument[document.uri] = append(
			byDocument[document.uri],
			converted,
		)
		versions[document.uri] = document.version
	}
	uris := make([]string, 0, len(byDocument))
	for uri := range byDocument {
		uris = append(uris, uri)
	}
	sort.Strings(uris)
	changes := make([]protocolDocumentEdit, 0, len(uris))
	for _, uri := range uris {
		changes = append(changes, protocolDocumentEdit{
			TextDocument: protocolOptionalVersionedDocument{
				URI:     uri,
				Version: versions[uri],
			},
			Edits: byDocument[uri],
		})
	}
	return protocolCodeAction{
		Title:       fix.Title,
		Kind:        "quickfix",
		IsPreferred: true,
		Edit:        protocolWorkspaceEdit{DocumentChanges: changes},
	}, relevant, true
}

func (server *Server) documentForEditLocked(
	edit diagnostic.TextEdit,
) *document {
	if document := server.documents[edit.Location.URI]; document != nil {
		return document
	}
	for _, document := range server.documents {
		if pathKey(document.path) == pathKey(edit.Location.Path) {
			return document
		}
	}
	return nil
}

func (server *Server) featureSnapshot(
	uri string,
) (document, metadataView, bool) {
	server.mu.Lock()
	defer server.mu.Unlock()
	source := server.documents[uri]
	if source == nil {
		return document{}, metadataView{}, false
	}
	workspace := server.workspaces[source.root]
	if workspace == nil || !workspace.hasLatest {
		return cloneDocument(*source), metadataView{}, true
	}
	latest := workspace.latest
	view := metadataView{
		definitions:    latest.AnnotationDefinitions(),
		modules:        latest.ModuleGraph().Modules,
		configurations: latest.Configurations(),
		actions:        latest.CodeActions(),
	}
	if workspace.hasGood {
		if len(view.definitions) == 0 {
			view.definitions = workspace.lastGood.AnnotationDefinitions()
		}
		if len(view.modules) == 0 {
			view.modules = workspace.lastGood.ModuleGraph().Modules
		}
		if len(view.configurations) == 0 {
			view.configurations = workspace.lastGood.Configurations()
		}
	}
	return cloneDocument(*source), view, true
}

func cloneDocument(source document) document {
	source.content = slices.Clone(source.content)
	return source
}

func annotationDefinition(
	definitions []compilerservice.AnnotationDefinition,
	name string,
) (compilerservice.AnnotationDefinition, bool) {
	for _, definition := range definitions {
		if definition.Name == name {
			return definition, true
		}
	}
	return compilerservice.AnnotationDefinition{}, false
}

func annotationDocumentation(
	definition compilerservice.AnnotationDefinition,
) string {
	var content strings.Builder
	fmt.Fprintf(&content, "`@%s`\n\n", definition.Name)
	fmt.Fprintf(&content, "Valid targets: %s.", annotationTargetDetail(definition))
	if definition.Repeatable {
		content.WriteString(" Repeatable.")
	}
	if len(definition.Arguments) != 0 {
		content.WriteString("\n\nArguments:\n")
		for _, argument := range definition.Arguments {
			fmt.Fprintf(
				&content,
				"\n- `%s`: %s",
				argument.Name,
				argumentKinds(argument),
			)
			if argument.Required {
				content.WriteString(" (required)")
			}
		}
	}
	return content.String()
}

func annotationTargetDetail(
	definition compilerservice.AnnotationDefinition,
) string {
	values := make([]string, len(definition.Targets))
	for index, target := range definition.Targets {
		values[index] = string(target)
	}
	return strings.Join(values, ", ")
}

func argumentKinds(argument compilerservice.AnnotationArgument) string {
	values := make([]string, len(argument.Kinds))
	for index, kind := range argument.Kinds {
		values[index] = string(kind)
	}
	return strings.Join(values, " or ")
}

func configurationFieldDocumentation(
	configuration compilerservice.Configuration,
	field compilerservice.ConfigurationField,
) string {
	var content strings.Builder
	fmt.Fprintf(
		&content,
		"`%s` · `%s`",
		field.Key,
		field.TypeID,
	)
	if field.Environment != "" {
		fmt.Fprintf(
			&content,
			"\n\nEnvironment: `%s`.",
			field.Environment,
		)
	}
	if field.Required {
		content.WriteString(" Required.")
	}
	if field.Secret {
		content.WriteString(" Secret value; defaults are redacted.")
	} else if field.HasDefault {
		fmt.Fprintf(&content, " Default: `%s`.", field.Default)
	}
	if configuration.Module != "" {
		fmt.Fprintf(&content, " Module: `%s`.", configuration.Module)
	}
	return content.String()
}

func tokenAt(content []byte, offset int) (string, int, int) {
	start := wordStart(content, offset, hoverCharacter)
	end := offset
	for end < len(content) && hoverCharacter(content[end]) {
		end++
	}
	return string(content[start:end]), start, end
}

func wordStart(
	content []byte,
	offset int,
	allowed func(byte) bool,
) int {
	start := min(max(offset, 0), len(content))
	for start > 0 && allowed(content[start-1]) {
		start--
	}
	return start
}

func annotationCharacter(value byte) bool {
	return value == '.' ||
		value == '_' ||
		value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9'
}

func argumentValueCharacter(value byte) bool {
	return annotationCharacter(value) ||
		value == '/' ||
		value == ':' ||
		value == '-'
}

func configurationCharacter(value byte) bool {
	return argumentValueCharacter(value)
}

func hoverCharacter(value byte) bool {
	return configurationCharacter(value) || value == '@'
}

func insideStringLiteral(content []byte) bool {
	quoted := false
	escaped := false
	for _, value := range content {
		switch {
		case escaped:
			escaped = false
		case value == '\\':
			escaped = true
		case value == '"':
			quoted = !quoted
		}
	}
	return quoted
}

func byteOffset(
	content []byte,
	position protocolPosition,
) (int, bool) {
	if position.Line < 0 || position.Character < 0 {
		return 0, false
	}
	start, end, found := contentLine(content, position.Line+1)
	if !found {
		return 0, false
	}
	units := 0
	for offset := start; offset < end; {
		if units == position.Character {
			return offset, true
		}
		character, size := utf8.DecodeRune(content[offset:end])
		next := 1
		if character > 0xFFFF {
			next = 2
		}
		if units+next > position.Character {
			return 0, false
		}
		units += next
		offset += size
	}
	if units == position.Character {
		return end, true
	}
	return 0, false
}

func protocolRangeAtOffsets(
	content []byte,
	start int,
	end int,
) protocolRange {
	return protocolRange{
		Start: protocolPositionAtOffset(content, start),
		End:   protocolPositionAtOffset(content, end),
	}
}

func protocolPositionAtOffset(
	content []byte,
	offset int,
) protocolPosition {
	offset = min(max(offset, 0), len(content))
	line := 0
	lineStart := 0
	for index, value := range content[:offset] {
		if value == '\n' {
			line++
			lineStart = index + 1
		}
	}
	return protocolPosition{
		Line:      line,
		Character: utf16Length(content[lineStart:offset]),
	}
}

func contentLineAtOffset(
	content []byte,
	offset int,
) (int, int, bool) {
	if offset < 0 || offset > len(content) {
		return 0, 0, false
	}
	start := offset
	for start > 0 && content[start-1] != '\n' {
		start--
	}
	end := offset
	for end < len(content) &&
		content[end] != '\n' &&
		content[end] != '\r' {
		end++
	}
	return start, end, true
}

func contentLine(
	content []byte,
	oneBasedLine int,
) (int, int, bool) {
	if oneBasedLine <= 0 {
		return 0, 0, false
	}
	if len(content) == 0 {
		return 0, 0, oneBasedLine == 1
	}
	start := 0
	for line := 1; line < oneBasedLine; line++ {
		index := slices.Index(content[start:], byte('\n'))
		if index < 0 {
			return 0, 0, false
		}
		start += index + 1
	}
	end := start
	for end < len(content) &&
		content[end] != '\n' &&
		content[end] != '\r' {
		end++
	}
	return start, end, true
}

func rangesOverlap(left, right protocolRange) bool {
	return comparePosition(left.End, right.Start) >= 0 &&
		comparePosition(right.End, left.Start) >= 0
}

func comparePosition(left, right protocolPosition) int {
	if left.Line != right.Line {
		return left.Line - right.Line
	}
	return left.Character - right.Character
}
