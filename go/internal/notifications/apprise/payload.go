package apprise

// Payload ports the C# `ApprisePayload` at
// `Notifications/Apprise/ApprisePayload.cs:3-17`.
//
// Type is the lowercase string the Apprise API consumes (per the
// C# `[EnumMember(Value="info|success|warning|failure")]` attributes
// on AppriseNotificationType). The Go port serializes
// [NotificationType.String] directly into the field.
type Payload struct {
	Urls       string `json:"urls,omitempty"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	Attachment string `json:"attachment,omitempty"`
	Type       string `json:"type"`
	Tag        string `json:"tag,omitempty"`
}
