package myanimelist

// listResponse mirrors C# `MyAnimeListResponse` at
// `MyAnimeListResponses.cs:6-10` — root envelope from
// /users/@me/animelist.
type listResponse struct {
	Data []animeItem `json:"data"`
}

// animeItem mirrors C# `MyAnimeListItem` at `:12-19`.
type animeItem struct {
	Node       animeNode  `json:"node"`
	ListStatus listStatus `json:"list_status"`
}

// animeNode mirrors C# `MyAnimeListItemInfo` at `:27-31`.
type animeNode struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
}

// listStatus mirrors C# `MyAnimeListStatusResult` at `:21-25`.
type listStatus struct {
	Status string `json:"status"`
	Score  int    `json:"score"`
}

// authToken mirrors C# `MyAnimeListAuthToken` at `:42-55`.
// Returned by the renew endpoint after refresh.
type authToken struct {
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}
