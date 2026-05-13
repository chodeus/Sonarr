package anilist

// queryList is the GraphQL document the AniList provider POSTs.
// Mirrors C# `AniListAPI.QueryList` at `AniListAPI.cs:18-52`. The
// AniList API caps results to 50 items per page; pagination is
// driven by the response's PageInfo.HasNextPage flag.
const queryList = `query ($id: String, $statusType: [MediaListStatus], $page: Int) {
    Page(page: $page) {
        pageInfo {
            currentPage
            lastPage
            hasNextPage
        }
        mediaList(userName: $id, type: ANIME, status_in: $statusType) {
            status
            progress
            media {
                id
                format
                title {
                    userPreferred
                    romaji
                }
                status
                episodes
                startDate {
                    year
                    month
                    day
                }
                endDate {
                    year
                    month
                    day
                }
            }
        }
    }
}`

// queryVariables ports the C# `BuildQuery` variables block at
// `AniListAPI.cs:67-71` for the QueryList shape.
type queryVariables struct {
	ID         string   `json:"id"`
	StatusType []string `json:"statusType"`
	Page       int      `json:"page"`
}

// queryBody is the outer GraphQL POST body.
type queryBody struct {
	Query     string         `json:"query"`
	Variables queryVariables `json:"variables"`
}

// pageResponse mirrors C# `MediaPageResponse` at `AniListAPI.cs:132-135`.
type pageResponse struct {
	Data pageData `json:"data"`
}

// pageData mirrors C# `MediaPageData` at `:127-130`.
type pageData struct {
	Page mediaPage `json:"Page"`
}

// mediaPage mirrors C# `MediaPage` at `:120-125`.
type mediaPage struct {
	PageInfo  pageInfo    `json:"pageInfo"`
	MediaList []mediaList `json:"mediaList"`
}

// pageInfo mirrors C# `PageInfo` at `:76-83`.
type pageInfo struct {
	CurrentPage int  `json:"currentPage"`
	LastPage    int  `json:"lastPage"`
	HasNextPage bool `json:"hasNextPage"`
}

// mediaList mirrors C# `MediaList` at `:111-118`.
type mediaList struct {
	Media    mediaInfo `json:"media"`
	Status   string    `json:"status"`
	Progress int       `json:"progress"`
}

// mediaInfo mirrors C# `MediaInfo` at `:92-109`. StartDate/EndDate
// are dropped — Sonarr doesn't consume them and the YMDBlock shape
// would need a custom unmarshaler (all three fields nullable).
type mediaInfo struct {
	ID       int         `json:"id"`
	Status   string      `json:"status"`
	Format   string      `json:"format"`
	Episodes *int        `json:"episodes"`
	Title    mediaTitles `json:"title"`
}

// mediaTitles mirrors C# `MediaTitles` at `:85-90`.
type mediaTitles struct {
	UserPreferred string `json:"userPreferred"`
	UserRomaji    string `json:"romaji"`
}

// authToken mirrors C# `RefreshRequestResponse` at
// `AniListTypes.cs:33-42`. Returned by the renew endpoint after
// refresh.
type authToken struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}
