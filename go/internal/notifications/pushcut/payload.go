package pushcut

// Payload ports the C# `PushcutPayload` at
// `Notifications/Pushcut/PushcutPayload.cs:5-12`.
//
// IsTimeSensitive is a *bool to round-trip the C# `bool?` (the API
// treats absent vs false differently).
type Payload struct {
	Title           string   `json:"title"`
	Text            string   `json:"text"`
	IsTimeSensitive *bool    `json:"isTimeSensitive,omitempty"`
	Image           string   `json:"image,omitempty"`
	Actions         []Action `json:"actions,omitempty"`
}

// Action ports the C# `PushcutAction` at `:14-18`.
type Action struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}
