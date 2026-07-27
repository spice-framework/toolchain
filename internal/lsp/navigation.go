package lsp

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/StevenBuglione/spice/compiler/diagnostic"
	compilerservice "github.com/StevenBuglione/spice/compiler/service"
)

const (
	annotationReferencePath = "docs/annotations.md"
	annotationReferenceURL  = "https://github.com/StevenBuglione/spice/blob/main/docs/annotations.md#built-in-definitions-and-targets"
	maxReferenceBytes       = 2 << 20
)

type protocolDocumentLink struct {
	Range   protocolRange `json:"range"`
	Target  string        `json:"target"`
	Tooltip string        `json:"tooltip,omitempty"`
}

type protocolLocationLink struct {
	OriginSelectionRange protocolRange `json:"originSelectionRange"`
	TargetURI            string        `json:"targetUri"`
	TargetRange          protocolRange `json:"targetRange"`
	TargetSelectionRange protocolRange `json:"targetSelectionRange"`
}

type annotationOccurrence struct {
	name  string
	start int
	end   int
}

type annotationReference struct {
	uri       string
	rangeItem protocolRange
}

func (server *Server) definition(message rpcMessage) error {
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
		return server.writer.response(message.ID, []protocolLocationLink{})
	}
	offset, valid := byteOffset(source.content, params.Position)
	if !valid {
		return server.writer.response(message.ID, []protocolLocationLink{})
	}
	occurrence, found := annotationOccurrenceAt(source.content, offset)
	if !found {
		return server.writer.response(message.ID, []protocolLocationLink{})
	}
	definition, found := definitionForOccurrence(
		source,
		metadata,
		occurrence,
	)
	if !found {
		return server.writer.response(message.ID, []protocolLocationLink{})
	}
	if definition.HasDescriptorLocation {
		return server.writer.response(message.ID, []protocolLocationLink{
			locationLink(source.content, occurrence, definition.DescriptorLocation),
		})
	}
	reference, found := localAnnotationReference(source.root, definition.Name)
	if !found {
		return server.writer.response(message.ID, []protocolLocationLink{})
	}
	return server.writer.response(message.ID, []protocolLocationLink{{
		OriginSelectionRange: protocolRangeAtOffsets(
			source.content,
			occurrence.start,
			occurrence.end,
		),
		TargetURI:            reference.uri,
		TargetRange:          reference.rangeItem,
		TargetSelectionRange: reference.rangeItem,
	}})
}

func (server *Server) implementation(message rpcMessage) error {
	if !message.request() {
		return nil
	}
	var params textDocumentPositionParams
	if err := decodeParams(message.Params, &params); err != nil {
		return server.writer.failure(message.ID, invalidParamsCode, err.Error())
	}
	source, metadata, found := server.featureSnapshot(params.TextDocument.URI)
	if !found {
		return server.writer.response(message.ID, []protocolLocationLink{})
	}
	offset, valid := byteOffset(source.content, params.Position)
	if !valid {
		return server.writer.response(message.ID, []protocolLocationLink{})
	}
	occurrence, found := annotationOccurrenceAt(source.content, offset)
	if !found {
		return server.writer.response(message.ID, []protocolLocationLink{})
	}
	definition, found := definitionForOccurrence(source, metadata, occurrence)
	if !found || !definition.Implementation.HasLocation {
		return server.writer.response(message.ID, []protocolLocationLink{})
	}
	return server.writer.response(message.ID, []protocolLocationLink{
		locationLink(
			source.content,
			occurrence,
			definition.Implementation.Location,
		),
	})
}

func (server *Server) documentLinks(message rpcMessage) error {
	if !message.request() {
		return nil
	}
	var params semanticTokensParams
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
		return server.writer.response(message.ID, []protocolDocumentLink{})
	}
	links := make([]protocolDocumentLink, 0)
	for _, occurrence := range annotationOccurrences(source.content) {
		definition, found := definitionForOccurrence(
			source,
			metadata,
			occurrence,
		)
		if !found {
			continue
		}
		var target string
		if definition.HasDescriptorLocation {
			target = locationTarget(definition.DescriptorLocation)
		} else if reference, local := localAnnotationReference(
			source.root,
			definition.Name,
		); local {
			target = localReferenceTarget(reference)
		} else {
			target = annotationReferenceURL
		}
		tooltip := fmt.Sprintf(
			"Open the @%s annotation definition",
			occurrence.name,
		)
		if definition.DescriptorPackage != "" {
			tooltip = fmt.Sprintf(
				"Open %s.%s",
				definition.DescriptorPackage,
				definition.DescriptorSymbol,
			)
		}
		links = append(links, protocolDocumentLink{
			Range: protocolRangeAtOffsets(
				source.content,
				occurrence.start,
				occurrence.end,
			),
			Target:  target,
			Tooltip: tooltip,
		})
	}
	return server.writer.response(message.ID, links)
}

func definitionForOccurrence(
	source document,
	metadata metadataView,
	occurrence annotationOccurrence,
) (compilerservice.AnnotationDefinition, bool) {
	position := protocolPositionAtOffset(source.content, occurrence.start)
	for _, summary := range metadata.annotations {
		if summary.Spelling != occurrence.name ||
			pathKey(summary.Location.Path) != pathKey(source.path) {
			continue
		}
		start := summary.Location.Range.Start
		if start.Offset != occurrence.start &&
			(start.Line != position.Line+1 ||
				start.Column != position.Character+1) {
			continue
		}
		for _, definition := range metadata.definitions {
			if summary.DefinitionPackage != "" &&
				definition.DescriptorPackage == summary.DefinitionPackage &&
				definition.DescriptorSymbol == summary.DefinitionSymbol {
				return definition, true
			}
			if summary.DefinitionPackage == "" &&
				definition.Name == summary.Name {
				return definition, true
			}
		}
	}
	definitions := fileScopedDefinitions(source.content, metadata.definitions)
	return annotationDefinition(definitions, occurrence.name)
}

func locationLink(
	source []byte,
	occurrence annotationOccurrence,
	target diagnostic.Location,
) protocolLocationLink {
	return protocolLocationLink{
		OriginSelectionRange: protocolRangeAtOffsets(
			source,
			occurrence.start,
			occurrence.end,
		),
		TargetURI:            target.URI,
		TargetRange:          protocolRangeFromLocation(target),
		TargetSelectionRange: protocolRangeFromLocation(target),
	}
}

func protocolRangeFromLocation(location diagnostic.Location) protocolRange {
	return protocolRange{
		Start: protocolPosition{
			Line:      max(location.Range.Start.Line-1, 0),
			Character: max(location.Range.Start.Column-1, 0),
		},
		End: protocolPosition{
			Line:      max(location.Range.End.Line-1, 0),
			Character: max(location.Range.End.Column-1, 0),
		},
	}
}

func locationTarget(location diagnostic.Location) string {
	position := location.Range.Start
	return fmt.Sprintf(
		"%s#%d,%d",
		strings.TrimSuffix(location.URI, "#"),
		position.Line,
		position.Column,
	)
}

func annotationOccurrences(content []byte) []annotationOccurrence {
	var result []annotationOccurrence
	for start := 0; start <= len(content); {
		relativeEnd := bytes.IndexByte(content[start:], '\n')
		last := relativeEnd < 0
		end := len(content)
		if !last {
			end = start + relativeEnd
		}
		line := bytes.TrimSuffix(content[start:end], []byte{'\r'})
		nameStart, nameEnd, found := annotationNameBounds(line)
		if found {
			result = append(result, annotationOccurrence{
				name:  string(line[nameStart+1 : nameEnd]),
				start: start + nameStart,
				end:   start + nameEnd,
			})
		}
		if last {
			break
		}
		start = end + 1
	}
	return result
}

func annotationOccurrenceAt(
	content []byte,
	offset int,
) (annotationOccurrence, bool) {
	for _, occurrence := range annotationOccurrences(content) {
		if offset >= occurrence.start && offset <= occurrence.end {
			return occurrence, true
		}
	}
	return annotationOccurrence{}, false
}

func knownAnnotation(
	definitions []compilerservice.AnnotationDefinition,
	name string,
) bool {
	for _, definition := range definitions {
		if definition.Name == name {
			return true
		}
	}
	return false
}

func localAnnotationReference(
	root string,
	name string,
) (annotationReference, bool) {
	if root == "" || name == "" {
		return annotationReference{}, false
	}
	path := filepath.Join(root, filepath.FromSlash(annotationReferencePath))
	info, err := os.Stat(path)
	if err != nil ||
		!info.Mode().IsRegular() ||
		info.Size() > maxReferenceBytes {
		return annotationReference{}, false
	}
	content, err := os.ReadFile(filepath.Clean(path))
	if err != nil || len(content) > maxReferenceBytes {
		return annotationReference{}, false
	}
	marker := []byte("| `@" + name + "` |")
	offset := bytes.Index(content, marker)
	if offset < 0 {
		return annotationReference{}, false
	}
	start := offset + len("| `")
	end := start + len("@"+name)
	uri, err := fileURI(path)
	if err != nil {
		return annotationReference{}, false
	}
	return annotationReference{
		uri:       uri,
		rangeItem: protocolRangeAtOffsets(content, start, end),
	}, true
}

func localReferenceTarget(reference annotationReference) string {
	position := reference.rangeItem.Start
	return fmt.Sprintf(
		"%s#%d,%d",
		strings.TrimSuffix(reference.uri, "#"),
		position.Line+1,
		position.Character+1,
	)
}
