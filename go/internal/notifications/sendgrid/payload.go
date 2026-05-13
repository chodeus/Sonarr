package sendgrid

// Payload ports the C# `SendGridPayload` at
// `Notifications/SendGrid/SendGridPayload.cs:5-16`.
type Payload struct {
	From             Email             `json:"from"`
	Content          []Content         `json:"content"`
	Personalizations []Personalization `json:"personalizations"`
}

// Content ports the C# `SendGridContent` at `:18-22`.
type Content struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// Email ports the C# `SendGridEmail` at `:24-27`.
type Email struct {
	Email string `json:"email"`
}

// Personalization ports the C# `SendGridPersonalization` at
// `:29-38`.
type Personalization struct {
	To      []Email `json:"to"`
	Subject string  `json:"subject"`
}
