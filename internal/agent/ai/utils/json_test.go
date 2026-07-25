package utils

import (
	"errors"
	"testing"
)

func TestDecodeStrictJSONRejectsUnknownFieldsAndMultipleValues(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}

	var decoded payload
	if err := DecodeStrictJSON([]byte(`{"name":"ok"}`), &decoded); err != nil {
		t.Fatalf("DecodeStrictJSON(valid) error = %v", err)
	}
	if decoded.Name != "ok" {
		t.Fatalf("decoded.Name = %q, want ok", decoded.Name)
	}
	if err := DecodeStrictJSON([]byte(`{"name":"ok","extra":true}`), &decoded); err == nil {
		t.Fatal("DecodeStrictJSON(unknown field) error = nil")
	}
	if err := DecodeStrictJSON([]byte(`{"name":"first"} {"name":"second"}`), &decoded); err == nil {
		t.Fatal("DecodeStrictJSON(multiple values) error = nil")
	}
}

func TestParseJSONObjectRejectsNullAndNonObjectValues(t *testing.T) {
	object, err := ParseJSONObject([]byte(`{"enabled":true}`))
	if err != nil {
		t.Fatalf("ParseJSONObject(object) error = %v", err)
	}
	if object["enabled"] != true {
		t.Fatalf("object[enabled] = %#v, want true", object["enabled"])
	}
	if _, err := ParseJSONObject([]byte(`null`)); !errors.Is(err, ErrJSONObject) {
		t.Fatalf("ParseJSONObject(null) error = %v, want ErrJSONObject", err)
	}
	if _, err := ParseJSONObject([]byte(`[]`)); err == nil {
		t.Fatal("ParseJSONObject(array) error = nil")
	}
}

func TestCloneJSONValueCopiesSerializableValues(t *testing.T) {
	original := map[string]any{"nested": map[string]any{"value": "source"}}
	cloned := CloneJSONValue(original).(map[string]any)
	cloned["nested"].(map[string]any)["value"] = "clone"

	if got := original["nested"].(map[string]any)["value"]; got != "source" {
		t.Fatalf("original nested value = %q, want source", got)
	}
}

func TestCloneJSONValueKeepsUnserializableValue(t *testing.T) {
	original := map[string]any{"callback": func() {}}
	cloned := CloneJSONValue(original).(map[string]any)
	cloned["copied"] = true

	if got := original["copied"]; got != true {
		t.Fatalf("fallback did not preserve original value: %#v", original)
	}
}
