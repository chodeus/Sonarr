package provider

import "strings"

// Config is the typed Settings interface every provider's Settings struct
// must implement. C# cite:
// src/NzbDrone.Core/ThingiProvider/IProviderConfig.cs:1-9.
//
// The C# IProviderConfig has a single Validate() method returning
// NzbDroneValidationResult (a thin extension of FluentValidation.Results.
// ValidationResult). The Go port mirrors that with ValidationResult below.
//
// Per-family settings types (NewznabSettings, TransmissionSettings, etc.)
// implement Config in their family packages once Phase 4 ports each one.
// The provider package only defines the contract.
type Config interface {
	Validate() ValidationResult
}

// ValidationResult is the FluentValidation port. C# cite:
// src/NzbDrone.Core/Validation/NzbDroneValidationFailure.cs +
// NzbDroneValidationState.cs.
//
// Encodes:
//   - Errors: list of property-keyed messages
//   - IsValid: convenience getter for the C# FluentValidation idiom
//
// The C# class wraps the FluentValidation result and adds severity-tier
// classification (IsWarning) on individual failures. The Go port uses a
// per-failure Severity field — defaults to MessageError to match the C#
// "non-warning failures are real errors" behaviour.
type ValidationResult struct {
	Errors []ValidationFailure
}

// IsValid reports whether the result has no error-severity failures.
// Warnings do NOT block validation in Sonarr — that's the FluentValidation
// + NzbDroneValidationState contract. Mirrors the C# getter through
// NzbDroneValidationResult.IsValid.
func (r ValidationResult) IsValid() bool {
	for _, e := range r.Errors {
		if e.Severity == MessageError {
			return false
		}
	}
	return true
}

// HasWarnings reports whether the result has at least one warning failure.
func (r ValidationResult) HasWarnings() bool {
	for _, e := range r.Errors {
		if e.Severity == MessageWarning {
			return true
		}
	}
	return false
}

// String renders the failures for diagnostics; not a stable API, but
// useful for logging/test assertions.
func (r ValidationResult) String() string {
	if len(r.Errors) == 0 {
		return "valid"
	}
	parts := make([]string, len(r.Errors))
	for i, e := range r.Errors {
		parts[i] = e.String()
	}
	return strings.Join(parts, "; ")
}

// ValidationFailure is one entry in a ValidationResult. C# cite:
// src/NzbDrone.Core/Validation/NzbDroneValidationFailure.cs:5-29.
//
// The C# type extends FluentValidation.Results.ValidationFailure and
// adds IsWarning + DetailedDescription + InfoLink. The Go port collapses
// IsWarning into the Severity enum (MessageError is the FluentValidation
// default, MessageWarning corresponds to IsWarning=true).
type ValidationFailure struct {
	PropertyName        string
	Message             string
	Severity            MessageType // default MessageError; MessageWarning for non-blocking
	DetailedDescription string
	InfoLink            string
}

// String renders one failure for diagnostics.
func (f ValidationFailure) String() string {
	prefix := f.Severity.String()
	if f.PropertyName == "" {
		return prefix + ": " + f.Message
	}
	return prefix + ": " + f.PropertyName + ": " + f.Message
}

// NewValidationFailure builds a default-severity (MessageError) failure.
func NewValidationFailure(propertyName, message string) ValidationFailure {
	return ValidationFailure{PropertyName: propertyName, Message: message, Severity: MessageError}
}

// NewValidationWarning builds a warning-severity failure.
func NewValidationWarning(propertyName, message string) ValidationFailure {
	return ValidationFailure{PropertyName: propertyName, Message: message, Severity: MessageWarning}
}

// ValidResult is a tiny constructor for "no errors" — common enough that
// avoiding a `provider.ValidationResult{}` literal at call sites is worth
// the helper.
func ValidResult() ValidationResult { return ValidationResult{} }
