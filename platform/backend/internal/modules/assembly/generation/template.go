package generation

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"

	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

var (
	placeholderPattern      = regexp.MustCompile(`\{\{(json|identifier):([a-z][a-z0-9_]*(?:\.[a-z][a-z0-9_]*)*)\}\}`)
	identifierPattern       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	stableIdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)
)

func renderStrictTemplate(source []byte, context any) ([]byte, error) {
	if bytes.Contains(source, []byte("{{")) || bytes.Contains(source, []byte("}}")) {
		matches := placeholderPattern.FindAllIndex(source, -1)
		cursor := 0
		var output bytes.Buffer
		for _, match := range matches {
			if bytes.Contains(source[cursor:match[0]], []byte("{{")) || bytes.Contains(source[cursor:match[0]], []byte("}}")) {
				return nil, ErrTemplateInvalid
			}
			output.Write(source[cursor:match[0]])
			parts := placeholderPattern.FindSubmatch(source[match[0]:match[1]])
			value, ok := lookupTemplateValue(context, string(parts[2]))
			if !ok {
				return nil, ErrTemplateValue
			}
			switch string(parts[1]) {
			case "json":
				raw, err := json.Marshal(value)
				if err != nil {
					return nil, ErrTemplateValue
				}
				canonical, err := machinecontract.Canonicalize(raw)
				if err != nil {
					return nil, ErrTemplateValue
				}
				output.Write(canonical)
			case "identifier":
				text, ok := value.(string)
				if !ok || !identifierPattern.MatchString(text) {
					return nil, ErrTemplateValue
				}
				output.WriteString(text)
			default:
				return nil, ErrTemplateInvalid
			}
			cursor = match[1]
		}
		if bytes.Contains(source[cursor:], []byte("{{")) || bytes.Contains(source[cursor:], []byte("}}")) {
			return nil, ErrTemplateInvalid
		}
		output.Write(source[cursor:])
		return output.Bytes(), nil
	}
	return append([]byte(nil), source...), nil
}

func lookupTemplateValue(context any, path string) (any, bool) {
	current := context
	for _, segment := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}
