// Package schema validates JSON payloads against JSON Schema 2020-12.
//
// A Validator is compiled once from a manifest's inputSchema map and reused
// across requests. Validation happens at API ingress, before the payload is
// forwarded to the worker process.
//
// If the manifest declares no inputSchema the Validator is a no-op and every
// payload passes.
package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ValidationError is returned when a payload does not satisfy the schema.
type ValidationError struct {
	Errs []string
}

func (e *ValidationError) Error() string {
	return "schema validation failed: " + strings.Join(e.Errs, "; ")
}

// Validator validates JSON payloads against a compiled JSON Schema.
// The zero value is valid and acts as a no-op validator.
type Validator struct {
	sch *jsonschema.Schema
}

// Compile builds a Validator from a raw schema map (as stored in
// Manifest.Interface.InputSchema). Returns a no-op Validator when rawSchema
// is nil or empty.
func Compile(rawSchema map[string]any) (*Validator, error) {
	if len(rawSchema) == 0 {
		return &Validator{}, nil
	}

	// Round-trip through JSON to get a json.RawMessage the compiler can read.
	data, err := json.Marshal(rawSchema)
	if err != nil {
		return nil, fmt.Errorf("schema marshal: %w", err)
	}

	c := jsonschema.NewCompiler()
	c.UseLoader(jsonschema.SchemeURLLoader{
		"https": jsonschema.URLLoader{},
		"http":  jsonschema.URLLoader{},
	})

	// Use an in-memory URL so the compiler can reference the schema by URI.
	const uri = "mem:///input"
	if err := c.AddResource(uri, data); err != nil {
		return nil, fmt.Errorf("schema add resource: %w", err)
	}

	sch, err := c.Compile(uri)
	if err != nil {
		return nil, fmt.Errorf("schema compile: %w", err)
	}

	return &Validator{sch: sch}, nil
}

// Validate checks raw JSON bytes against the compiled schema.
// Returns nil when the payload is valid or when the Validator is a no-op.
// Returns *ValidationError on schema violations.
func (v *Validator) Validate(data json.RawMessage) error {
	if v.sch == nil {
		return nil
	}\n
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("payload unmarshal: %w", err)
	}

	if err := v.sch.Validate(doc); err != nil {
		var ve *jsonschema.ValidationError
		if errors.As(err, &ve) {
			return &ValidationError{Errs: flattenErrors(ve)}
		}
		return err
	}
	return nil
}

// IsValidationError reports whether err is a *ValidationError.
func IsValidationError(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}

// flattenErrors recursively collects human-readable messages from a
// jsonschema.ValidationError tree.
func flattenErrors(ve *jsonschema.ValidationError) []string {
	var msgs []string
	if ve.Message != "" {
		msgs = append(msgs, ve.InstanceLocation+": "+ve.Message)
	}
	for _, cause := range ve.Causes {
		msgs = append(msgs, flattenErrors(cause)...)
	}
	return msgs
}
