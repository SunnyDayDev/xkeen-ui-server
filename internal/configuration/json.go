package configuration

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
)

func parseJSON(raw json.RawMessage, location Issue) (any, *Issue) {
	for offset := 0; offset < len(raw); {
		r, size := utf8.DecodeRune(raw[offset:])
		if r == utf8.RuneError && size == 1 {
			position := int64(offset)
			location.Code, location.Offset = "invalid_json", &position
			return nil, &location
		}
		offset += size
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	// Validate grammar first so syntax offsets refer to the complete input.
	// RawMessage preserves duplicate keys for the token walk below.
	var validated json.RawMessage
	if err := dec.Decode(&validated); err != nil {
		offset := int64(len(raw))
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			offset = syntax.Offset - 1
		}
		location.Code, location.Offset = "invalid_json", &offset
		return nil, &location
	}
	for offset := dec.InputOffset(); offset < int64(len(raw)); offset++ {
		switch raw[offset] {
		case ' ', '\t', '\r', '\n':
			continue
		}
		location.Code, location.Offset = "invalid_json", &offset
		return nil, &location
	}
	dec = json.NewDecoder(bytes.NewReader(validated))
	dec.UseNumber()
	value, issue := readValue(dec, "")
	if issue != nil {
		issue.ElementID, issue.Source, issue.VariableName = location.ElementID, location.Source, location.VariableName
	}
	return value, issue
}

// readValue only reads a document whose JSON grammar has been validated.
func readValue(dec *json.Decoder, path string) (any, *Issue) {
	token, _ := dec.Token()
	switch token {
	case json.Delim('{'):
		object := make(map[string]any)
		for dec.More() {
			keyToken, _ := dec.Token()
			key := keyToken.(string)
			childPath := appendPath(path, key)
			if _, exists := object[key]; exists {
				return nil, &Issue{Code: "duplicate_json_key", Path: &childPath}
			}
			value, issue := readValue(dec, childPath)
			if issue != nil {
				return nil, issue
			}
			object[key] = value
		}
		_, _ = dec.Token()
		return object, nil
	case json.Delim('['):
		array := make([]any, 0)
		for dec.More() {
			value, issue := readValue(dec, appendPath(path, strconv.Itoa(len(array))))
			if issue != nil {
				return nil, issue
			}
			array = append(array, value)
		}
		_, _ = dec.Token()
		return array, nil
	default:
		return token, nil
	}
}

func appendPath(parent, part string) string {
	part = strings.ReplaceAll(part, "~", "~0")
	return parent + "/" + strings.ReplaceAll(part, "/", "~1")
}
