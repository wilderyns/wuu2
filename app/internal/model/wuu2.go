package model

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
	LastChange  string
	Song        string
	SongLink    string
	Artist      string
	ArtistLink  string
	Album       string
	AlbumLink   string
	AlbumArtURL string
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
	CurrentlyInGame    bool
	LastChange         string
	GameName           string
	GameURL            string
	GameIconURL        string
	ProfileAvatarURL   string
	HoursPlayed        int
	EarnedAchievements int
	TotalAchievements  int
}

type RetroAchievements struct {
	LastChange       string
	HardcorePoints   int
	SoftcorePoints   int
	RetroPoints      int
	LastGameID       int
	LastGameTitle    string
	GameIconURL      string
	CurrentlyInGame  bool
	RichPresence     string
	ProfileAvatarURL string
	SiteRank         int
}

type Information struct {
	TotalRequests   uint64 `json:"totalRequests"`
	ServerStartTime string `json:"serverStartTime"`
}

type Wuu2 struct {
	Trakt             []Trakt
	Wow               []Wow
	AppleMusic        []AppleMusic
	Spotify           []Spotify
	Steam             []Steam
	RetroAchievements []RetroAchievements
}
