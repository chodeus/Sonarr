package webhook

import "sonarr.tv/sonarr/internal/notifications"

// builders.go exposes the per-hook payload constructors as exported
// helpers so other webhook-shaped providers (Notifiarr in slice
// 4.CF, future Discord overrides, etc.) can reuse them instead of
// duplicating the struct-literal composition.
//
// Mirrors the C# `WebhookBase<TSettings>` BuildXPayload abstract-
// base methods at `Notifications/Webhook/WebhookBase.cs:32-218`.
// The Go-port replaces the inheritance idiom with plain functions
// since concrete providers (Webhook, Notifiarr) hold per-provider
// state separately and only share the payload-construction logic.

// BuildGrabPayload mirrors C# `WebhookBase.BuildOnGrabPayload` at
// `Notifications/Webhook/WebhookBase.cs:32-50`.
func BuildGrabPayload(msg notifications.GrabMessage) GrabPayload {
	return GrabPayload{
		BasePayload:        BasePayload{EventType: EventTypeGrab},
		Series:             msg.Series,
		Episodes:           msg.Episode,
		Release:            msg.Episode,
		DownloadClient:     msg.DownloadClientName,
		DownloadClientType: msg.DownloadClientType,
		DownloadID:         msg.DownloadID,
	}
}

// BuildDownloadPayload mirrors C# `BuildOnDownloadPayload` at
// `WebhookBase.cs:52-85`.
func BuildDownloadPayload(msg notifications.DownloadMessage) ImportPayload {
	return ImportPayload{
		BasePayload:  BasePayload{EventType: EventTypeDownload},
		Series:       msg.Series,
		EpisodeFile:  msg.EpisodeFile,
		IsUpgrade:    len(msg.OldFiles) > 0,
		DeletedFiles: msg.OldFiles,
		DownloadID:   msg.DownloadID,
		Release:      msg.Release,
	}
}

// BuildImportCompletePayload mirrors C# `BuildOnImportCompletePayload`
// at `WebhookBase.cs:87-108`.
func BuildImportCompletePayload(msg notifications.ImportCompleteMessage) ImportCompletePayload {
	return ImportCompletePayload{
		BasePayload:     BasePayload{EventType: EventTypeDownload},
		Series:          msg.Series,
		Episodes:        msg.Episodes,
		EpisodeFiles:    msg.EpisodeFiles,
		DownloadID:      msg.DownloadID,
		SourcePath:      msg.SourcePath,
		DestinationPath: msg.DestinationPath,
		Release:         msg.Release,
	}
}

// BuildRenamePayload mirrors C# `BuildOnRenamePayload` at
// `WebhookBase.cs:147-157`.
func BuildRenamePayload(msg notifications.RenameMessage) RenamePayload {
	return RenamePayload{
		BasePayload:         BasePayload{EventType: EventTypeRename},
		Series:              msg.Series,
		RenamedEpisodeFiles: msg.RenamedFiles,
	}
}

// BuildEpisodeFileDeletePayload mirrors C# `BuildOnEpisodeFileDelete`
// at `WebhookBase.cs:110-122`.
func BuildEpisodeFileDeletePayload(msg notifications.EpisodeDeleteMessage) EpisodeDeletePayload {
	return EpisodeDeletePayload{
		BasePayload:  BasePayload{EventType: EventTypeEpisodeFileDelete},
		Series:       msg.Series,
		EpisodeFile:  msg.EpisodeFile,
		DeleteReason: msg.Reason.String(),
	}
}

// BuildSeriesAddPayload mirrors C# `BuildOnSeriesAdd` at
// `WebhookBase.cs:124-133`.
func BuildSeriesAddPayload(msg notifications.SeriesAddMessage) SeriesAddPayload {
	return SeriesAddPayload{
		BasePayload: BasePayload{EventType: EventTypeSeriesAdd},
		Series:      msg.Series,
	}
}

// BuildSeriesDeletePayload mirrors C# `BuildOnSeriesDelete` at
// `WebhookBase.cs:135-145`.
func BuildSeriesDeletePayload(msg notifications.SeriesDeleteMessage) SeriesDeletePayload {
	return SeriesDeletePayload{
		BasePayload:  BasePayload{EventType: EventTypeSeriesDelete},
		Series:       msg.Series,
		DeletedFiles: msg.DeletedFiles,
	}
}

// BuildHealthPayload mirrors C# `BuildHealthPayload` at
// `WebhookBase.cs:159-170`. The C# builder explicitly omits
// `ApplicationUrl`; [HealthPayload] correspondingly does not embed
// [BasePayload] so the `applicationUrl` key is absent from the wire
// body (see the HealthPayload doc-comment for the
// `NullValueHandling.Ignore` rationale).
func BuildHealthPayload(msg notifications.HealthCheckMessage) HealthPayload {
	return HealthPayload{
		EventType: EventTypeHealth,
		Level:     msg.Check.Type.String(),
		Message:   msg.Check.Message,
		Type:      msg.Check.Source,
		WikiURL:   msg.Check.WikiURL,
	}
}

// BuildHealthRestoredPayload mirrors C# `BuildHealthRestoredPayload`
// at `WebhookBase.cs:172-183`. Identical shape to BuildHealthPayload
// with a distinct EventType.
func BuildHealthRestoredPayload(msg notifications.HealthCheckMessage) HealthPayload {
	return HealthPayload{
		EventType: EventTypeHealthRestored,
		Level:     msg.Check.Type.String(),
		Message:   msg.Check.Message,
		Type:      msg.Check.Source,
		WikiURL:   msg.Check.WikiURL,
	}
}

// BuildApplicationUpdatePayload mirrors C# `BuildApplicationUpdatePayload`
// at `WebhookBase.cs:185-195`.
func BuildApplicationUpdatePayload(msg notifications.ApplicationUpdateMessage) ApplicationUpdatePayload {
	return ApplicationUpdatePayload{
		BasePayload:     BasePayload{EventType: EventTypeApplicationUpdate},
		Message:         msg.Message,
		PreviousVersion: msg.PreviousVersion,
		NewVersion:      msg.NewVersion,
	}
}

// BuildManualInteractionPayload mirrors C# `BuildManualInteractionRequiredPayload`
// at `WebhookBase.cs:197-218`.
func BuildManualInteractionPayload(msg notifications.ManualInteractionRequiredMessage) ManualInteractionPayload {
	return ManualInteractionPayload{
		BasePayload:    BasePayload{EventType: EventTypeManualInteractionRequired},
		Series:         msg.Series,
		Episodes:       msg.Episode,
		DownloadID:     msg.DownloadID,
		Release:        msg.Release,
		DownloadClient: "",
	}
}

// BuildTestPayload mirrors C# `BuildTestPayload` at the inline
// `Webhook.Test` body. Returns a deterministic Grab-shaped payload
// the proxy posts for connection verification.
func BuildTestPayload() GrabPayload {
	return GrabPayload{
		BasePayload: BasePayload{EventType: EventTypeTest},
		Series: map[string]any{
			"id":     1,
			"title":  "Test Title",
			"path":   "C:\\testpath",
			"tvdbId": 1234,
			"tags":   []string{"test-tag"},
		},
		Episodes: []map[string]any{
			{
				"id":            123,
				"episodeNumber": 1,
				"seasonNumber":  1,
				"title":         "Test title",
			},
		},
	}
}
