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
	LastCheck string
	Online    bool
	Character string
	Realm     string
	Location  string
	X         float32
	Y         float32
	Z         float32
	Facing    float32
}

type Wuu2 struct {
	Trakt []Trakt
	Wow   []Wow
}
