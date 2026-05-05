package healthcheck

import (
	"context"
	"reflect"
)

// Check is the IProvideHealthCheck port. Implementations live next
// to this file (one per concrete check) or under sibling packages
// when a check needs services we don't want to import here.
//
// Mirrors `src/NzbDrone.Core/HealthCheck/IProvideHealthCheck.cs` plus
// the abstract `HealthCheckBase.cs` defaults.
//
// Convention:
//
//   - Name() returns the canonical struct name (e.g. "RootFolderCheck").
//     This is the key the orchestrator uses to dedupe results, so two
//     checks with the same Name will share a result slot. Mirrors the
//     C# `result.Source.Name`.
//   - Run(ctx) HealthCheck returns ResultOK on success and a populated
//     HealthCheck on failure. Returning ResultOK is what the
//     orchestrator translates into "remove from cache + publish
//     RestoredEvent". Mirrors `IProvideHealthCheck.Check()`.
//   - CheckOnStartup() reports whether the check participates in the
//     ApplicationStartedEvent batch. Default true via the StartupAnd
//     ScheduledDefaults helper.
//   - CheckOnSchedule() reports whether the scheduled (CheckHealth
//     command, scheduler tick) batch fires the check. Default true.
type Check interface {
	Name() string
	Run(ctx context.Context) HealthCheck
	CheckOnStartup() bool
	CheckOnSchedule() bool
}

// EventDrivenCheck is the optional interface a check implements to
// declare event triggers. Replaces the C# `[CheckOn(typeof(X))]`
// attribute pattern: the orchestrator calls EventTriggers() once at
// registration and indexes each binding under reflect.TypeOf(EventType).
//
// A check implementing both `Check` and `EventDrivenCheck` runs in
// three modes: startup batch (when CheckOnStartup() is true), scheduled
// batch (when CheckOnSchedule() is true), AND on each declared event
// type (subject to the binding's Condition + Filter).
type EventDrivenCheck interface {
	Check
	EventTriggers() []EventTrigger
}

// CheckOnCondition mirrors the C# enum at `CheckOnAttribute.cs:18-23`.
// It gates whether a check fires for an event based on the check's
// previous result.
type CheckOnCondition int

const (
	// CheckOnAlways fires regardless of previous result. Default.
	CheckOnAlways CheckOnCondition = 0
	// CheckOnFailedOnly fires only when the check is currently failing
	// (a result row exists in the cache for this check). Used by
	// success-style transitions where we only want to re-check after
	// a fix.
	CheckOnFailedOnly CheckOnCondition = 1
	// CheckOnSuccessfulOnly fires only when the check is NOT currently
	// failing. Used to detect newly-introduced failures.
	CheckOnSuccessfulOnly CheckOnCondition = 2
)

// EventTrigger is one declared event binding for an EventDrivenCheck.
// Mirrors a single `[CheckOn(typeof(X), CheckOnCondition.Y)]` attribute
// attached to a C# health-check class.
type EventTrigger struct {
	// EventType is the reflected event type. The orchestrator does a
	// reflect.TypeOf(message) lookup in its event-driven map; this
	// type must therefore match exactly (assignability is NOT
	// considered, mirroring the C# `eventDrivenHealthChecks.TryGetValue`
	// dictionary semantics).
	//
	// Idiom: `reflect.TypeOf(events.SeriesMovedEvent{})`.
	EventType reflect.Type
	// Condition gates the trigger by the check's current pass/fail
	// state. See CheckOnCondition.
	Condition CheckOnCondition
	// Filter is the optional `ICheckOnCondition[T]` equivalent — a
	// per-event predicate that decides whether THIS instance of the
	// event should fire the check, beyond the previous-result gate.
	// Receives the bus-delivered `any` value; checks should type-
	// assert. Nil means "always fire (subject to Condition)".
	//
	// Mirrors `EventDrivenHealthCheck<TEvent>.EventFilter` at
	// `EventDrivenHealthCheck.cs:36-39`.
	Filter func(event any) bool
}
