package gotify

// Payload ports the C# `GotifyMessage` at
// `Notifications/Gotify/GotifyMessage.cs:5-35`.
type Payload struct {
	Title    string  `json:"title"`
	Message  string  `json:"message"`
	Priority int     `json:"priority"`
	Extras   *Extras `json:"extras,omitempty"`
}

// Extras ports the C# `GotifyExtras` at `GotifyMessage.cs:37-44`.
// The two JSON keys use `::` to namespace the Gotify-defined client
// hint shape (`client::display` for content-type rendering hint,
// `client::notification` for image + click-through URL).
type Extras struct {
	ClientDisplay      *ClientDisplay      `json:"client::display,omitempty"`
	ClientNotification *ClientNotification `json:"client::notification,omitempty"`
}

// ClientDisplay carries the rendering content-type hint —
// "text/plain" or "text/markdown". C# `:46-54`.
type ClientDisplay struct {
	ContentType string `json:"contentType"`
}

// ClientNotification carries the per-notification image + click
// payload. C# `:56-60`.
type ClientNotification struct {
	BigImageURL string                   `json:"bigImageUrl,omitempty"`
	Click       *ClientNotificationClick `json:"click,omitempty"`
}

// ClientNotificationClick carries the click-through URL. C# `:62-70`.
type ClientNotificationClick struct {
	URL string `json:"url"`
}

// setContentType mirrors C# `GotifyMessage.SetContentType` at `:17-22`.
func (p *Payload) setContentType(isMarkdown bool) {
	ct := "text/plain"
	if isMarkdown {
		ct = "text/markdown"
	}
	if p.Extras == nil {
		p.Extras = &Extras{}
	}
	p.Extras.ClientDisplay = &ClientDisplay{ContentType: ct}
}

// setImage mirrors C# `GotifyMessage.SetImage` at `:24-28`.
func (p *Payload) setImage(url string) {
	if p.Extras == nil {
		p.Extras = &Extras{}
	}
	if p.Extras.ClientNotification == nil {
		p.Extras.ClientNotification = &ClientNotification{}
	}
	p.Extras.ClientNotification.BigImageURL = url
}

// setClickURL mirrors C# `GotifyMessage.SetClickUrl` at `:30-34`.
func (p *Payload) setClickURL(url string) {
	if p.Extras == nil {
		p.Extras = &Extras{}
	}
	if p.Extras.ClientNotification == nil {
		p.Extras.ClientNotification = &ClientNotification{}
	}
	p.Extras.ClientNotification.Click = &ClientNotificationClick{URL: url}
}
