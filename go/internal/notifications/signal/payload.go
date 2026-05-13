package signal

// Payload ports the C# `SignalPayload` at
// `Notifications/Signal/SignalPayload.cs:3-8`.
type Payload struct {
	Message    string   `json:"message"`
	Number     string   `json:"number"`
	Recipients []string `json:"recipients"`
}
