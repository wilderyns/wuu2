package main

import (
	"crypto/subtle"
	"html/template"
	"net/http"
	"strings"
)

type authGateViewData struct {
	Title   string
	Message string
	Action  string
}

var authGateTemplate = template.Must(template.New("auth_gate").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Title}}</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 2rem; }
    form { max-width: 420px; display: grid; gap: 0.75rem; }
    input, button { font-size: 1rem; padding: 0.65rem; }
    .note { color: #666; }
  </style>
</head>
<body>
  <h1>Enter Security Code</h1>
  <p class="note">{{.Message}}</p>
  <form method="post" action="{{.Action}}">
    <input type="password" name="code" autocomplete="one-time-code" placeholder="Security code" required>
    <button type="submit">Continue</button>
  </form>
</body>
</html>`))

func withAuthSecurityGate(config Config, flowName string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		expected := strings.TrimSpace(config.AuthSecurityCode)
		if expected == "" {
			next(w, r)
			return
		}

		provided := extractAuthGateCode(r)
		if authGateCodeMatches(expected, provided) {
			next(w, r)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_ = authGateTemplate.Execute(w, authGateViewData{
			Title:   gateTitle(flowName),
			Message: gateMessage(flowName),
			Action:  r.URL.Path,
		})
	}
}

func extractAuthGateCode(r *http.Request) string {
	provided := strings.TrimSpace(r.URL.Query().Get("code"))
	if provided != "" {
		return provided
	}

	if err := r.ParseForm(); err != nil {
		return ""
	}
	return strings.TrimSpace(r.FormValue("code"))
}

func authGateCodeMatches(expected string, provided string) bool {
	if expected == "" || provided == "" {
		return false
	}
	if len(expected) != len(provided) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1
}

func gateTitle(flowName string) string {
	name := strings.TrimSpace(flowName)
	if name == "" {
		return "Auth Gate"
	}
	return name + " Gate"
}

func gateMessage(flowName string) string {
	name := strings.TrimSpace(flowName)
	if name == "" {
		return "A security code is required before continuing."
	}
	return "A security code is required before starting " + name + "."
}
