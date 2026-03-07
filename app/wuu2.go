package main

type Trakt struct {
	WatchedAt string
	Type      string
	Title     string
	IMDB      string
	Season    string
	Episode   string
}

type Wow struct {
	LastCheck    string
	LastModified string
	LastOnline   string
	Online       bool
	Character    string
	Realm        string
	Location     string
	X            float32
	Y            float32
	Z            float32
	Facing       float32
	AvatarURL    string
	InsetURL     string
	MainrawURL   string
	ArmoryURL    string
}

type AppleMusic struct {
	LastChange string
	Song       string
	SongLink   string
	Artist     string
	ArtistLink string
	Album      string
	AlbumLink  string
}

type Spotify struct {
	LastChange string
	Song       string
	SongLink   string
	Artist     string
	ArtistLink string
	Album      string
	AlbumLink  string
}

type Steam struct {
	LastChange  string
	Game        string
	GameLink    string
	AvatarURL   string
	HoursPlayed int
}

type Information struct {
	TotalRequests   uint64 `json:"totalRequests"`
	ServerStartTime string `json:"serverStartTime"`
}

type Wuu2 struct {
	Trakt      []Trakt
	Wow        []Wow
	AppleMusic []AppleMusic
	Spotify    []Spotify
	Steam      []Steam
}
