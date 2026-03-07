package trakt

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"wuu2/internal/config"
	"wuu2/internal/model"
)

type historyItem struct {
	WatchedAt string `json:"watched_at"`
	Type      string `json:"type"`
	Movie     struct {
		Title string `json:"title"`
		IDs   struct {
			IMDB string `json:"imdb"`
		} `json:"ids"`
	} `json:"movie"`
	Episode struct {
		Season int    `json:"season"`
		Number int    `json:"number"`
		Title  string `json:"title"`
		IDs    struct {
			IMDB string `json:"imdb"`
		} `json:"ids"`
	} `json:"episode"`
	Show struct {
		Title string `json:"title"`
		IDs   struct {
			IMDB string `json:"imdb"`
		} `json:"ids"`
	} `json:"show"`
}

func Update(cfg config.Config, snapshot *model.Wuu2) {
	client := &http.Client{}

	req, _ := http.NewRequest("GET", "https://api.trakt.tv/users/wilderyns/history?page=1&limit=1", nil)
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("trakt-api-version", "2")
	req.Header.Add("trakt-api-key", cfg.TraktID)

	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("Errored when sending request to the server")
		return
	}
	defer func(body io.ReadCloser) {
		err := body.Close()
		if err != nil {
			fmt.Println("Error closing response body")
		}
	}(resp.Body)

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("Error reading response body")
		return
	}

	var history []historyItem
	if err := json.Unmarshal(respBody, &history); err != nil {
		fmt.Println("Error unmarshalling Trakt response")
		return
	}

	if len(history) == 0 {
		snapshot.Trakt = nil
		return
	}

	item := history[0]
	traktItem := model.Trakt{
		WatchedAt: item.WatchedAt,
		Type:      item.Type,
	}

	switch item.Type {
	case "movie":
		traktItem.Title = item.Movie.Title
		traktItem.IMDB = item.Movie.IDs.IMDB
	case "episode":
		traktItem.Title = item.Show.Title
		if item.Episode.Season > 0 {
			traktItem.Season = strconv.Itoa(item.Episode.Season)
		}
		if item.Episode.Number > 0 {
			traktItem.Episode = strconv.Itoa(item.Episode.Number)
		}
		if item.Show.IDs.IMDB != "" {
			traktItem.IMDB = item.Show.IDs.IMDB
		} else {
			traktItem.IMDB = item.Episode.IDs.IMDB
		}
	default:
		traktItem.Title = item.Movie.Title
		traktItem.IMDB = item.Movie.IDs.IMDB
	}

	snapshot.Trakt = []model.Trakt{traktItem}
}
