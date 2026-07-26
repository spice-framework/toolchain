package lsp

import (
	"bytes"
	"unicode"
	"unicode/utf8"
)

const semanticDecoratorToken = 0

type semanticTokensParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
}

type semanticTokensResult struct {
	Data []uint32 `json:"data"`
}

type semanticToken struct {
	line   int
	start  int
	length int
	kind   int
}

func (server *Server) semanticTokens(message rpcMessage) error {
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
	source, _, found := server.featureSnapshot(params.TextDocument.URI)
	if !found {
		return server.writer.response(
			message.ID,
			semanticTokensResult{Data: []uint32{}},
		)
	}
	return server.writer.response(
		message.ID,
		semanticTokensResult{Data: encodeSemanticTokens(
			annotationSemanticTokens(source.content),
		)},
	)
}

func annotationSemanticTokens(content []byte) []semanticToken {
	var tokens []semanticToken
	for line, start := 0, 0; start <= len(content); line++ {
		end := bytes.IndexByte(content[start:], '\n')
		last := end < 0
		if last {
			end = len(content)
		} else {
			end += start
		}
		lineContent := bytes.TrimSuffix(content[start:end], []byte{'\r'})
		if token, found := annotationSemanticToken(lineContent, line); found {
			tokens = append(tokens, token)
		}
		if last {
			break
		}
		start = end + 1
	}
	return tokens
}

func annotationSemanticToken(
	lineContent []byte,
	line int,
) (semanticToken, bool) {
	offset := skipHorizontalWhitespace(lineContent, 0)
	if !bytes.HasPrefix(lineContent[offset:], []byte("//")) {
		return semanticToken{}, false
	}
	offset = skipHorizontalWhitespace(lineContent, offset+2)
	if offset >= len(lineContent) || lineContent[offset] != '@' {
		return semanticToken{}, false
	}
	end := annotationNameEnd(lineContent, offset+1)
	if end == offset+1 {
		return semanticToken{}, false
	}
	return semanticToken{
		line:   line,
		start:  utf16Length(lineContent[:offset]),
		length: utf16Length(lineContent[offset:end]),
		kind:   semanticDecoratorToken,
	}, true
}

func skipHorizontalWhitespace(content []byte, offset int) int {
	for offset < len(content) &&
		(content[offset] == ' ' || content[offset] == '\t') {
		offset++
	}
	return offset
}

func annotationNameEnd(content []byte, start int) int {
	end := start
	for end < len(content) {
		character, size := utf8.DecodeRune(content[end:])
		if character == utf8.RuneError && size == 1 {
			break
		}
		if character != '.' &&
			character != '_' &&
			!isAnnotationIdentifierCharacter(character) {
			break
		}
		end += size
	}
	return end
}

func isAnnotationIdentifierCharacter(character rune) bool {
	return unicode.IsLetter(character) || unicode.IsDigit(character)
}

func encodeSemanticTokens(tokens []semanticToken) []uint32 {
	data := make([]uint32, 0, len(tokens)*5)
	previousLine := 0
	previousStart := 0
	for _, token := range tokens {
		deltaLine := token.line - previousLine
		deltaStart := token.start
		if deltaLine == 0 {
			deltaStart -= previousStart
		}
		data = append(
			data,
			uint32(deltaLine),    // #nosec G115 -- source positions are non-negative and document-bounded.
			uint32(deltaStart),   // #nosec G115 -- source positions are non-negative and document-bounded.
			uint32(token.length), // #nosec G115 -- token lengths are document-bounded.
			uint32(token.kind),   // #nosec G115 -- token kinds are fixed non-negative constants.
			0,
		)
		previousLine = token.line
		previousStart = token.start
	}
	return data
}
