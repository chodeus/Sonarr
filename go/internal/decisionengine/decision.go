package decisionengine

// Rejection ports the C# `DownloadRejection` at
// `src/NzbDrone.Core/DecisionEngine/DownloadRejection.cs:1-7`. The C#
// class is a thin specialisation of `Rejection<TRejectionReason>`
// pinned to `DownloadRejectionReason`; the Go port collapses both
// onto a single struct because there's no second instantiation
// downstream — the generic in C# was speculative.
//
// Construct via [NewRejection] or [NewTemporaryRejection]. Both
// fields are values; copying a Rejection is cheap.
type Rejection struct {
	// Reason is the categorical reason a Specification rejected the
	// candidate. Routed to the v3 API and used by the React frontend
	// to look up a localised template.
	Reason RejectionReason
	// Message is the human-readable rendering of Reason, with
	// per-rejection parameters interpolated. Spec-side ports build
	// this via fmt.Sprintf at the rejection site.
	Message string
	// Kind tags the rejection as Permanent (drop) or Temporary
	// (re-queue for retry).
	Kind RejectionKind
}

// NewRejection builds a Permanent rejection. Mirrors the C# default
// at `Rejection.cs:9` (`type = RejectionType.Permanent`).
func NewRejection(reason RejectionReason, message string) Rejection {
	return Rejection{Reason: reason, Message: message, Kind: RejectionPermanent}
}

// NewTemporaryRejection builds a Temporary rejection — the candidate
// is added to the pending-releases table for later retry. Used by
// MinimumAge / DelayProfile / Queue specs.
func NewTemporaryRejection(reason RejectionReason, message string) Rejection {
	return Rejection{Reason: reason, Message: message, Kind: RejectionTemporary}
}

// String returns the C# `Rejection.ToString` shape: `[<kind>] <message>`.
// Mirrors `:23-26`.
func (r Rejection) String() string {
	return "[" + r.Kind.String() + "] " + r.Message
}

// SpecDecision is the value a Specification returns. Mirrors the C#
// `DownloadSpecDecision` at
// `src/NzbDrone.Core/DecisionEngine/DownloadSpecDecision.cs:1-32`.
//
// Construct via [Accept] or [Reject]. Direct field access is
// allowed (the Go port doesn't replicate the C# private-setter
// invariant — Accepted is set only via the factories so the
// invariant holds by construction).
//
// The C# tree allocates a singleton `AcceptDownloadSpecDecision` and
// returns it from every `Accept()` call (`:9, 13`). The Go port does
// the same — a single package-level value var keeps the allocation
// out of the per-spec hot path.
type SpecDecision struct {
	// Accepted is true when the spec passes; the other fields are
	// meaningless (zero values).
	Accepted bool
	// Reason is the rejection category. Meaningful only when Accepted
	// is false.
	Reason RejectionReason
	// Message is the rendered rejection message. Meaningful only when
	// Accepted is false.
	Message string
}

// acceptedSpecDecision mirrors the C# singleton at `:7`. Returned
// verbatim from every Accept() call.
var acceptedSpecDecision = SpecDecision{Accepted: true}

// Accept returns the singleton "spec passes" SpecDecision. Mirrors
// the C# `Accept()` factory at `:13-15`.
func Accept() SpecDecision {
	return acceptedSpecDecision
}

// Reject builds a "spec failed" SpecDecision with the given reason +
// message. Mirrors the C# `Reject(reason, message)` overload at `:22-29`.
// Callers that need fmt.Sprintf-style interpolation pre-format the
// message; the Go port doesn't replicate the C# `params object[]`
// overload because Go's fmt.Sprintf at the call site is no less ergonomic.
func Reject(reason RejectionReason, message string) SpecDecision {
	return SpecDecision{Accepted: false, Reason: reason, Message: message}
}

// RejectTemporary builds a "spec failed temporarily" SpecDecision.
// The C# tree models temporary rejections via the RejectionType on
// the outer Rejection rather than the SpecDecision; the Go port
// preserves the C# shape (SpecDecision carries no Kind) and the
// orchestrator's translation step (Specification.Kind() ⇒
// Rejection.Kind) is what tags the eventual Rejection.
//
// This factory exists for documentation parity but currently behaves
// identically to Reject — the Kind tagging happens at the orchestrator
// boundary, not here.
func RejectTemporary(reason RejectionReason, message string) SpecDecision {
	return Reject(reason, message)
}

// Decision is the per-release output of the orchestrator. Mirrors the
// C# `DownloadDecision` at
// `src/NzbDrone.Core/DecisionEngine/DownloadDecision.cs:1-44`.
//
// The C# class carries a `RemoteEpisode` field; the Go port uses
// `Subject any` as a placeholder until the RemoteEpisode-equivalent
// type lands (its own slice — see package doc). When the real type
// arrives, callers either type-switch on Subject or the field types
// over to the concrete pointer type.
//
// Approved / TemporarilyRejected / Rejected are derived properties
// (computed each call rather than cached); the underlying state is
// the Rejections slice alone.
type Decision struct {
	// Subject carries the parsed-release-plus-context value (the C#
	// RemoteEpisode). Typed as `any` until the concrete type lands.
	Subject any
	// Rejections is the list every contributing Specification failure.
	// Empty when the Decision is approved. Order is preservation —
	// the orchestrator appends in spec-evaluation order so the UI can
	// show the failures in execution order.
	Rejections []Rejection
}

// NewDecision constructs an approved Decision (no rejections) bound
// to the given subject. Mirrors the C# constructor at `:30-34` for
// the zero-rejection case.
func NewDecision(subject any) Decision {
	return Decision{Subject: subject}
}

// NewRejectedDecision constructs a Decision carrying the supplied
// rejections. Order is preserved.
func NewRejectedDecision(subject any, rejections ...Rejection) Decision {
	if len(rejections) == 0 {
		return Decision{Subject: subject}
	}
	cp := make([]Rejection, len(rejections))
	copy(cp, rejections)
	return Decision{Subject: subject, Rejections: cp}
}

// Approved reports whether every Specification accepted the
// candidate (i.e. the rejection list is empty). Mirrors C#
// `DownloadDecision.Approved` at `:11`.
func (d Decision) Approved() bool {
	return len(d.Rejections) == 0
}

// TemporarilyRejected reports whether the Decision has rejections AND
// every rejection is Temporary. Used by the orchestrator to route
// the candidate to the pending-releases table for retry.
//
// Mirrors C# `DownloadDecision.TemporarilyRejected` at `:13-19`.
func (d Decision) TemporarilyRejected() bool {
	if len(d.Rejections) == 0 {
		return false
	}
	for _, r := range d.Rejections {
		if r.Kind != RejectionTemporary {
			return false
		}
	}
	return true
}

// Rejected reports whether the Decision has at least one Permanent
// rejection. Distinct from `!Approved()`: a Decision with only
// Temporary rejections is BOTH `!Approved()` AND `!Rejected()` — it's
// `TemporarilyRejected()`.
//
// Mirrors C# `DownloadDecision.Rejected` at `:21-27`.
func (d Decision) Rejected() bool {
	for _, r := range d.Rejections {
		if r.Kind == RejectionPermanent {
			return true
		}
	}
	return false
}
