package healthcheck

import (
	"context"
	"strings"
)

// PackageGlobalMessageCheckName is the canonical Source identifier
// (matches the C# class name; appears in the SignalR / JSON API).
const PackageGlobalMessageCheckName = "PackageGlobalMessageCheck"

// PackageGlobalMessageProvider narrows the deployment-info Provider
// to the single field this check consumes. The Go-port
// `*deploymentinfo.Provider` satisfies it directly; tests pass a
// stub.
type PackageGlobalMessageProvider interface {
	PackageGlobalMessage() string
}

// PackageGlobalMessageCheck surfaces the OS-packager-set global
// message as a healthcheck. Empty / whitespace-only messages pass;
// non-empty messages produce a result whose severity is keyed by the
// prefix:
//
//   - "Error:" → [ResultError] (prefix stripped from the surfaced
//     message)
//   - "Warn:"  → [ResultWarning] (prefix stripped)
//   - any other prefix → [ResultNotice]
//
// Mirrors `src/NzbDrone.Core/HealthCheck/Checks/PackageGlobalMessageCheck.cs:1-46`.
//
// Audit fix B.5 (unblocked by the slice-5.J34 DeploymentInfoProvider
// port).
type PackageGlobalMessageCheck struct {
	prov PackageGlobalMessageProvider
}

// NewPackageGlobalMessageCheck builds the check. Returns nil when the
// provider is nil — call sites in the healthcheck registry should
// gate on a nil return.
func NewPackageGlobalMessageCheck(prov PackageGlobalMessageProvider) *PackageGlobalMessageCheck {
	if prov == nil {
		return nil
	}
	return &PackageGlobalMessageCheck{prov: prov}
}

// Name returns the canonical identifier. Mirrors the C#
// `result.Source.Name` reflection at `HealthCheckService.cs:97`.
func (*PackageGlobalMessageCheck) Name() string { return PackageGlobalMessageCheckName }

// CheckOnStartup defaults true (HealthCheckBase default).
func (*PackageGlobalMessageCheck) CheckOnStartup() bool { return true }

// CheckOnSchedule defaults true (HealthCheckBase default).
func (*PackageGlobalMessageCheck) CheckOnSchedule() bool { return true }

// Run returns the healthcheck result. Empty / whitespace message →
// passing. Otherwise produces a Notice/Warning/Error per the prefix
// inspection.
func (c *PackageGlobalMessageCheck) Run(_ context.Context) HealthCheck {
	if c == nil {
		return OK(PackageGlobalMessageCheckName)
	}
	raw := c.prov.PackageGlobalMessage()
	if strings.TrimSpace(raw) == "" {
		return OK(PackageGlobalMessageCheckName)
	}

	message := raw
	severity := ResultNotice
	switch {
	case strings.HasPrefix(message, "Error:"):
		message = strings.TrimSpace(message[len("Error:"):])
		severity = ResultError
	case strings.HasPrefix(message, "Warn:"):
		message = strings.TrimSpace(message[len("Warn:"):])
		severity = ResultWarning
	}

	return New(PackageGlobalMessageCheckName, severity, ReasonPackage, message, "#package-maintainer-message")
}
