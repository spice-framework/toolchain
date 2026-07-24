// Package parser parses Spice annotations embedded in valid Go comments.
package parser

import (
	"fmt"
	"go/token"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/StevenBuglione/spice/annotation"
)

// ParseComment parses a single comment line. The bool reports whether the line
// contains a Spice annotation. Both "//@Name" and "// @Name" are accepted.
func ParseComment(input string, position token.Position) (annotation.Annotation, bool, error) {
	raw := input
	input = strings.TrimSpace(input)
	if strings.HasPrefix(input, "//") {
		input = strings.TrimSpace(strings.TrimPrefix(input, "//"))
	}
	if !strings.HasPrefix(input, "@") {
		return annotation.Annotation{}, false, nil
	}

	p := &commentParser{input: input, position: position}
	parsed, err := p.parseAnnotation()
	if err != nil {
		return annotation.Annotation{}, true, err
	}
	parsed.Raw = raw
	return parsed, true, nil
}

type commentParser struct {
	input    string
	offset   int
	position token.Position
}

func (p *commentParser) parseAnnotation() (annotation.Annotation, error) {
	if !p.consume('@') {
		return annotation.Annotation{}, p.errorf("annotation must start with @")
	}

	name := p.parseName()
	if name == "" {
		return annotation.Annotation{}, p.errorf("annotation name is required")
	}

	result := annotation.Annotation{Name: name, Position: p.position}
	p.skipSpace()
	if p.eof() {
		return result, nil
	}
	if !p.consume('(') {
		return annotation.Annotation{}, p.errorf("expected '(' after annotation name")
	}

	p.skipSpace()
	if p.consume(')') {
		p.skipSpace()
		if !p.eof() {
			return annotation.Annotation{}, p.errorf("unexpected trailing content")
		}
		return result, nil
	}

	seenNamed := false
	seenNames := map[string]struct{}{}
	for {
		p.skipSpace()
		start := p.offset
		candidate := p.parseIdentifier()
		p.skipSpace()

		var argument annotation.Argument
		if candidate != "" && p.consume('=') {
			seenNamed = true
			if _, exists := seenNames[candidate]; exists {
				return annotation.Annotation{}, p.errorf("duplicate argument %q", candidate)
			}
			seenNames[candidate] = struct{}{}
			argument.Name = candidate
			p.skipSpace()
			value, err := p.parseValue()
			if err != nil {
				return annotation.Annotation{}, err
			}
			argument.Value = value
		} else {
			p.offset = start
			if seenNamed {
				return annotation.Annotation{}, p.errorf("positional arguments cannot follow named arguments")
			}
			value, err := p.parseValue()
			if err != nil {
				return annotation.Annotation{}, err
			}
			argument.Value = value
		}
		result.Arguments = append(result.Arguments, argument)

		p.skipSpace()
		if p.consume(')') {
			break
		}
		if !p.consume(',') {
			return annotation.Annotation{}, p.errorf("expected ',' or ')' after argument")
		}
		p.skipSpace()
		if p.peek() == ')' {
			return annotation.Annotation{}, p.errorf("trailing commas are not supported")
		}
	}

	p.skipSpace()
	if !p.eof() {
		return annotation.Annotation{}, p.errorf("unexpected trailing content")
	}
	return result, nil
}

func (p *commentParser) parseValue() (annotation.Value, error) {
	p.skipSpace()
	if p.eof() {
		return annotation.Value{}, p.errorf("argument value is required")
	}

	switch p.peek() {
	case '"', '`':
		value, err := p.parseString()
		if err != nil {
			return annotation.Value{}, err
		}
		return annotation.Value{Kind: annotation.KindString, String: value}, nil
	case '[':
		return p.parseList()
	}

	start := p.offset
	identifier := p.parseIdentifier()
	if identifier == "true" || identifier == "false" {
		return annotation.Value{Kind: annotation.KindBoolean, Boolean: identifier == "true"}, nil
	}
	if identifier != "" {
		return annotation.Value{Kind: annotation.KindIdentifier, Identifier: identifier}, nil
	}

	p.offset = start
	integer := p.parseInteger()
	if integer != "" {
		value, err := strconv.ParseInt(integer, 10, 64)
		if err != nil {
			return annotation.Value{}, p.errorf("invalid integer %q", integer)
		}
		return annotation.Value{Kind: annotation.KindInteger, Integer: value}, nil
	}

	return annotation.Value{}, p.errorf("unsupported argument value")
}

func (p *commentParser) parseList() (annotation.Value, error) {
	if !p.consume('[') {
		return annotation.Value{}, p.errorf("list must start with '['")
	}
	p.skipSpace()
	result := annotation.Value{Kind: annotation.KindList}
	if p.consume(']') {
		return result, nil
	}
	for {
		value, err := p.parseValue()
		if err != nil {
			return annotation.Value{}, err
		}
		result.List = append(result.List, value)
		p.skipSpace()
		if p.consume(']') {
			return result, nil
		}
		if !p.consume(',') {
			return annotation.Value{}, p.errorf("expected ',' or ']' in list")
		}
		p.skipSpace()
		if p.peek() == ']' {
			return annotation.Value{}, p.errorf("trailing commas are not supported in lists")
		}
	}
}

func (p *commentParser) parseString() (string, error) {
	quote := p.peek()
	start := p.offset
	p.offset++
	for !p.eof() {
		current := p.peek()
		p.offset++
		if current == quote {
			raw := p.input[start:p.offset]
			value, err := strconv.Unquote(raw)
			if err != nil {
				return "", p.errorf("invalid string literal: %v", err)
			}
			return value, nil
		}
		if current == '\\' && quote == '"' && !p.eof() {
			p.offset++
		}
	}
	return "", p.errorf("unterminated string literal")
}

func (p *commentParser) parseName() string {
	start := p.offset
	for !p.eof() {
		r, size := utf8.DecodeRuneInString(p.input[p.offset:])
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '.') {
			break
		}
		p.offset += size
	}
	return p.input[start:p.offset]
}

func (p *commentParser) parseIdentifier() string {
	start := p.offset
	if p.eof() {
		return ""
	}
	r, size := utf8.DecodeRuneInString(p.input[p.offset:])
	if !(unicode.IsLetter(r) || r == '_') {
		return ""
	}
	p.offset += size
	for !p.eof() {
		r, size = utf8.DecodeRuneInString(p.input[p.offset:])
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '.' || r == '-' || r == ':' || r == '/') {
			break
		}
		p.offset += size
	}
	return p.input[start:p.offset]
}

func (p *commentParser) parseInteger() string {
	start := p.offset
	if p.peek() == '-' {
		p.offset++
	}
	digits := p.offset
	for !p.eof() && p.peek() >= '0' && p.peek() <= '9' {
		p.offset++
	}
	if digits == p.offset {
		p.offset = start
		return ""
	}
	return p.input[start:p.offset]
}

func (p *commentParser) skipSpace() {
	for !p.eof() {
		r, size := utf8.DecodeRuneInString(p.input[p.offset:])
		if !unicode.IsSpace(r) {
			return
		}
		p.offset += size
	}
}

func (p *commentParser) consume(expected byte) bool {
	if p.eof() || p.input[p.offset] != expected {
		return false
	}
	p.offset++
	return true
}

func (p *commentParser) peek() byte {
	if p.eof() {
		return 0
	}
	return p.input[p.offset]
}

func (p *commentParser) eof() bool {
	return p.offset >= len(p.input)
}

func (p *commentParser) errorf(format string, arguments ...any) error {
	message := fmt.Sprintf(format, arguments...)
	column := p.position.Column
	if column <= 0 {
		column = 1
	}
	column += p.offset
	return fmt.Errorf("%s:%d:%d: %s", p.position.Filename, p.position.Line, column, message)
}
