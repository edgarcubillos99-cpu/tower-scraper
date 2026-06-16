package api

import (
	"crypto/subtle"
	"net/http"
)

// withBasicAuth exige HTTP Basic Auth cuando user y pass no están vacíos.
// Si faltan en el entorno, el handler queda abierto (solo desarrollo).
func withBasicAuth(username, password string, next http.HandlerFunc) http.HandlerFunc {
	if username == "" || password == "" {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || !basicAuthMatches(user, pass, username, password) {
			w.Header().Set("WWW-Authenticate", `Basic realm="Tower Coverage API", charset="UTF-8"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func basicAuthMatches(user, pass, expectedUser, expectedPass string) bool {
	return subtle.ConstantTimeCompare([]byte(user), []byte(expectedUser)) == 1 &&
		subtle.ConstantTimeCompare([]byte(pass), []byte(expectedPass)) == 1
}
