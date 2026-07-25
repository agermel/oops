package utils

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

var ErrJSONObject = errors.New("json value must be an object")

// DecodeStrictJSON decodes exactly one JSON value and rejects unknown fields.
func DecodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("json contains multiple values")
		}
		return err
	}
	return nil
}

// ParseJSONObject decodes a JSON object into a map.
func ParseJSONObject(data []byte) (map[string]any, error) {
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, ErrJSONObject
	}
	return object, nil
}

// IsJSONObject reports whether data contains a non-null JSON object.
func IsJSONObject(data []byte) bool {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return false
	}
	return object != nil
}

// CloneJSONValue copies JSON-compatible values and retains values JSON cannot encode.
func CloneJSONValue(value any) any {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var cloned any
	if err := json.Unmarshal(data, &cloned); err != nil {
		return value
	}
	return cloned
}
