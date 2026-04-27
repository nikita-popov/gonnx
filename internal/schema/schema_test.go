package schema_test

import (
	"encoding/json"
	"testing"

	"github.com/nikita-popov/gonnx/internal/schema"
)

// resnet50InputSchema mirrors the inputSchema from examples/resnet50/manifest.yaml.
var resnet50InputSchema = map[string]any{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"type":    "object",
	"properties": map[string]any{
		"image": map[string]any{
			"type":            "string",
			"contentEncoding": "base64",
		},
		"top_k": map[string]any{
			"type":    "integer",
			"minimum": 1,
			"maximum": 10,
		},
	},
	"required": []any{"image"},
}

func TestCompile_NilSchema(t *testing.T) {
	v, err := schema.Compile(nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	// no-op validator must accept anything
	if err := v.Validate(json.RawMessage(`{"anything":true}`)); err != nil {
		t.Fatalf("no-op validator rejected payload: %v", err)
	}
}

func TestCompile_EmptySchema(t *testing.T) {
	v, err := schema.Compile(map[string]any{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if err := v.Validate(json.RawMessage(`42`)); err != nil {
		t.Fatalf("no-op validator rejected payload: %v", err)
	}
}

func TestValidate_ValidPayload(t *testing.T) {
	v, err := schema.Compile(resnet50InputSchema)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	payload := json.RawMessage(`{"image": "aGVsbG8=", "top_k": 3}`)
	if err := v.Validate(payload); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
}

func TestValidate_MissingRequired(t *testing.T) {
	v, err := schema.Compile(resnet50InputSchema)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	// 'image' is required but absent
	payload := json.RawMessage(`{"top_k": 3}`)
	err = v.Validate(payload)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !schema.IsValidationError(err) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
}

func TestValidate_WrongType(t *testing.T) {
	v, err := schema.Compile(resnet50InputSchema)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	// top_k must be integer, not string
	payload := json.RawMessage(`{"image": "aGVsbG8=", "top_k": "five"}`)
	err = v.Validate(payload)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !schema.IsValidationError(err) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
}

func TestValidate_OutOfRange(t *testing.T) {
	v, err := schema.Compile(resnet50InputSchema)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	// top_k maximum is 10
	payload := json.RawMessage(`{"image": "aGVsbG8=", "top_k": 99}`)
	err = v.Validate(payload)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
}

func TestValidate_InvalidJSON(t *testing.T) {
	v, err := schema.Compile(resnet50InputSchema)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if err := v.Validate(json.RawMessage(`{not valid json`)); err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestValidate_AdditionalPropertiesAllowed(t *testing.T) {
	v, err := schema.Compile(resnet50InputSchema)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	// Schema does not set additionalProperties:false — extra fields are OK.
	payload := json.RawMessage(`{"image": "aGVsbG8=", "extra": "field"}`)
	if err := v.Validate(payload); err != nil {
		t.Fatalf("unexpected rejection of additional property: %v", err)
	}
}

func TestIsValidationError(t *testing.T) {
	if schema.IsValidationError(nil) {
		t.Fatal("nil should not be a ValidationError")
	}
	var ve *schema.ValidationError
	if !schema.IsValidationError(ve) {
		t.Fatal("*ValidationError should satisfy IsValidationError")
	}
}
