package main

import (
	"errors"
	"net/http"
	"strings"
	"time"
)

func parseDateTimeString(raw string) (time.Time, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, errors.New("empty time value")
	}

	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, nil
	}

	if parsed, err := http.ParseTime(value); err == nil {
		return parsed, nil
	}

	// Handle RFC1123-like timestamps with single-digit day values.
	if parsed, err := time.Parse("Mon, 2 Jan 2006 15:04:05 MST", value); err == nil {
		return parsed, nil
	}

	return time.Time{}, errors.New("unsupported time format")
}
