package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	_ "github.com/joho/godotenv"
)

type traktHistoryItem struct {
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

func getTrakt(config Config, wuu2 *Wuu2) {
	client := &http.Client{}

	req, _ := http.NewRequest("GET", "https://api.trakt.tv/users/wilderyns/history?page=1&limit=1", nil)

	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("trakt-api-version", "2")
	req.Header.Add("trakt-api-key", config.TraktID)

	resp, err := client.Do(req)

	if err != nil {
		fmt.Println("Errored when sending request to the server")
		return
	}

	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			fmt.Println("Error closing response body")
		}
	}(resp.Body)

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("Error reading response body")
		return
	}

	var history []traktHistoryItem
	if err := json.Unmarshal(respBody, &history); err != nil {
		fmt.Println("Error unmarshalling Trakt response")
		return
	}

	for _, item := range history {
		traktItem := Trakt{
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
			// Best-effort fallback if Trakt adds or returns other item types.
			traktItem.Title = item.Movie.Title
			traktItem.IMDB = item.Movie.IDs.IMDB
		}

		wuu2.Trakt = append(wuu2.Trakt, traktItem)
	}
}
