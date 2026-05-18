// Package validation accumulates per-field validation failures and
// emits them in the wire shape Sonarr's C# tree produces via
// FluentValidation. The C# write-side controllers compose
// `IValidator<T>` chains like:
//
//	RuleFor(c => c.Name).NotEmpty();
//	RuleFor(c => c.Port).GreaterThan(0);
//	RuleFor(c => c.RootFolderPath).SetValidator(_pathValidator);
//
// and the pipeline at `Sonarr.Http/ErrorManagement/SonarrErrorPipeline.cs:46`
// writes `STJson.ToJson(validationException.Errors)` — a JSON array
// of `[{propertyName, errorMessage, severity}, …]`.
//
// Go's lack of expression-level reflection makes the fluent
// `RuleFor(x => x.Prop)` form unwieldy; this package opts for a
// Go-idiomatic per-field accumulator instead. Typical use:
//
//	errs := validation.New()
//	errs.Required("name", req.Name)
//	errs.Positive("port", req.Port)
//	errs.Range("priority", req.Priority, 0, 100)
//	if !errs.OK() {
//	    httperr.WriteValidation(w, errs.Failures())
//	    return
//	}
//
// Built-in rules cover the common C# `[FluentValidation]` annotations
// (NotEmpty, GreaterThan, Length, Matches, Must). Custom rules use
// [Errors.Add] directly. Property names are emitted to the wire
// verbatim — pass them in the same casing the C# resource uses
// (typically PascalCase, e.g. "Name", "Port", "RootFolderPath") so
// the React UI's field-by-field error binding works.
package validation

import (
	"regexp"

	"sonarr.tv/sonarr/internal/httperr"
)

// Errors accumulates validation failures during a single request
// validation pass. The zero value is ready to use; prefer [New] for
// stylistic consistency with the rest of the codebase.
type Errors struct {
	failures []httperr.Failure
}

// New returns an empty Errors. The returned pointer is not
// goroutine-safe — validation passes are inherently per-request.
func New() *Errors {
	return &Errors{}
}

// Add appends a single failure with the default severity ("error").
// Use this for custom rules that the helpers don't cover; for the
// standard rules, prefer [Required] / [Positive] / [Range] / etc.
func (e *Errors) Add(propertyName, errorMessage string) {
	e.failures = append(e.failures, httperr.Failure{
		PropertyName: propertyName,
		ErrorMessage: errorMessage,
		Severity:     "error",
	})
}

// AddSeverity is like [Add] but lets the caller pick the severity
// ("error" / "warning" / "info"). Most write paths only ever emit
// "error"; warnings are surfaced by health-check style validators.
func (e *Errors) AddSeverity(propertyName, errorMessage, severity string) {
	if severity == "" {
		severity = "error"
	}
	e.failures = append(e.failures, httperr.Failure{
		PropertyName: propertyName,
		ErrorMessage: errorMessage,
		Severity:     severity,
	})
}

// OK reports whether no failures have been accumulated. Callers
// typically write `if !errs.OK() { httperr.WriteValidation(...) }`
// at the end of a validation block.
func (e *Errors) OK() bool {
	return len(e.failures) == 0
}

// Failures returns the accumulated failures in insertion order.
// The returned slice is owned by the caller — modifying it doesn't
// affect this Errors. Returns nil when OK() is true; callers passing
// the result to httperr.WriteValidation will get `[]` on the wire
// (the WriteValidation helper normalises nil to an empty array).
func (e *Errors) Failures() []httperr.Failure {
	if len(e.failures) == 0 {
		return nil
	}
	out := make([]httperr.Failure, len(e.failures))
	copy(out, e.failures)
	return out
}

// Len returns the number of failures accumulated so far. Useful for
// scoping conditional logic (e.g., "only run rule B if rule A passed").
func (e *Errors) Len() int {
	return len(e.failures)
}

// ---------- built-in rules ----------

// Required fails if value is the empty string. Mirrors the C#
// `.NotEmpty()` rule on string properties. Returns true when the
// value is present (so callers can chain dependent checks: `if
// errs.Required("name", req.Name) { errs.MaxLen("name", req.Name, 64) }`).
func (e *Errors) Required(propertyName, value string) bool {
	if value == "" {
		e.Add(propertyName, "'"+propertyName+"' is required.")
		return false
	}
	return true
}

// Positive fails if value <= 0. Mirrors `.GreaterThan(0)` on
// integer properties — the most common Sonarr integer-validation
// rule (Port, Priority, ID-foreign-keys).
func (e *Errors) Positive(propertyName string, value int) bool {
	if value <= 0 {
		e.Add(propertyName, "'"+propertyName+"' must be greater than 0.")
		return false
	}
	return true
}

// NonNegative fails if value < 0. Mirrors `.GreaterThanOrEqualTo(0)`.
func (e *Errors) NonNegative(propertyName string, value int) bool {
	if value < 0 {
		e.Add(propertyName, "'"+propertyName+"' must be greater than or equal to 0.")
		return false
	}
	return true
}

// Range fails if value < min || value > max. Mirrors
// `.InclusiveBetween(min, max)` on numeric properties.
func (e *Errors) Range(propertyName string, value, min, max int) bool {
	if value < min || value > max {
		e.failures = append(e.failures, httperr.Failure{
			PropertyName: propertyName,
			ErrorMessage: rangeMsg(propertyName, min, max),
			Severity:     "error",
		})
		return false
	}
	return true
}

// MaxLen fails if len(value) > max. Mirrors `.MaximumLength(max)`
// on string properties.
func (e *Errors) MaxLen(propertyName, value string, max int) bool {
	if len(value) > max {
		e.failures = append(e.failures, httperr.Failure{
			PropertyName: propertyName,
			ErrorMessage: maxLenMsg(propertyName, max),
			Severity:     "error",
		})
		return false
	}
	return true
}

// MinLen fails if len(value) < min. Mirrors `.MinimumLength(min)`.
func (e *Errors) MinLen(propertyName, value string, min int) bool {
	if len(value) < min {
		e.failures = append(e.failures, httperr.Failure{
			PropertyName: propertyName,
			ErrorMessage: minLenMsg(propertyName, min),
			Severity:     "error",
		})
		return false
	}
	return true
}

// Pattern fails if value does not match the regex. Compile the
// pattern once at package init for hot paths — this helper accepts
// a precompiled *regexp.Regexp to keep validation cheap. Mirrors
// `.Matches(pattern)`.
func (e *Errors) Pattern(propertyName, value string, pattern *regexp.Regexp) bool {
	if pattern == nil || !pattern.MatchString(value) {
		e.failures = append(e.failures, httperr.Failure{
			PropertyName: propertyName,
			ErrorMessage: "'" + propertyName + "' is not in the expected format.",
			Severity:     "error",
		})
		return false
	}
	return true
}

// Must fails if ok is false, with the caller-supplied error message.
// Mirrors `.Must(predicate)`. The C# pipeline pairs Must with
// WithMessage; this helper takes the message inline because Go's
// type system doesn't make chained-after-the-fact message override
// ergonomic.
func (e *Errors) Must(propertyName string, ok bool, errorMessage string) bool {
	if !ok {
		e.Add(propertyName, errorMessage)
		return false
	}
	return true
}

// ---------- internals ----------

// rangeMsg / maxLenMsg / minLenMsg keep the FluentValidation-style
// message text close to the C# wire output. They use simple
// concatenation rather than fmt.Sprintf because every byte counts in
// a hot validation path and the messages don't need formatting
// flexibility.
func rangeMsg(propertyName string, min, max int) string {
	return "'" + propertyName + "' must be between " + itoa(min) + " and " + itoa(max) + " (inclusive)."
}

func maxLenMsg(propertyName string, max int) string {
	return "The length of '" + propertyName + "' must be " + itoa(max) + " characters or fewer."
}

func minLenMsg(propertyName string, min int) string {
	return "The length of '" + propertyName + "' must be at least " + itoa(min) + " characters."
}

// itoa is a strconv.Itoa equivalent avoiding the strconv import
// in the hot path. Negative values are supported because Range and
// friends accept signed ints.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
