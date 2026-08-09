package goreleaseverify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const maxJSONDepth = 64

func decodeStrictJSON(data []byte, destination any) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("json document has trailing content")
		}
		return fmt.Errorf("read trailing JSON content: %w", err)
	}
	return nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := validateUniqueJSONValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return fmt.Errorf("read trailing JSON content: %w", err)
		}
		return errors.New("json document contains trailing content")
	}
	return nil
}

func validateUniqueJSONValue(decoder *json.Decoder, depth int) error {
	if depth > maxJSONDepth {
		return fmt.Errorf("json document exceeds nesting depth %d", maxJSONDepth)
	}
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("read JSON token: %w", err)
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, tokenErr := decoder.Token()
			if tokenErr != nil {
				return fmt.Errorf("read JSON object key: %w", tokenErr)
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("json object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("json object repeats key %q", key)
			}
			seen[key] = struct{}{}
			if err := validateUniqueJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		return consumeDelimiter(decoder, '}')
	case '[':
		for decoder.More() {
			if err := validateUniqueJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		return consumeDelimiter(decoder, ']')
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}

func consumeDelimiter(decoder *json.Decoder, expected json.Delim) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("read JSON closing delimiter: %w", err)
	}
	if token != expected {
		return fmt.Errorf("json compound closes with %q, require %q", token, expected)
	}
	return nil
}
