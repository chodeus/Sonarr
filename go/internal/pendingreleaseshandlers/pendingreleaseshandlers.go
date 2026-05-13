// Package pendingreleaseshandlers wires events from upstream
// publishers (decisionengine, series) onto the pendingreleases.Service
// remove-side surface. Mirrors the IHandle<TEvent> declarations on
// C# PendingReleaseService at `PendingReleaseService.cs:651-693`.
//
// Lives in its own package (rather than inside pendingreleases) to
// dodge a parser → pendingreleases → decisionengine → parser import
// cycle that would otherwise pin the dispatcher methods to the
// pendingreleases package's narrow Input/GrabbedInput shapes only.
// Same architectural pattern as historyhandlers / blocklisthandlers.
//
// Slice 4.BZ (C-cleanup sub-arc C7b-ii of queue [C7]). Handler
// functions are typed against narrow [Service*] interfaces that the
// pendingreleases.Service satisfies via Go's structural typing;
// production wiring (currently deferred) supplies the real Service.
package pendingreleaseshandlers
