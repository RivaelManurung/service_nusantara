package middleware

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// CORS reflects only origins that appear in the allow list.
//
// The previous service sent `Access-Control-Allow-Origin: *` on an API that
// authenticates with bearer tokens. That combination is rejected by browsers
// for credentialed requests and, for token-in-header clients, hands every
// website on the internet the ability to call the API from a victim's browser.
func CORS(allowedOrigins []string, maxAge time.Duration) Middleware {
	allowAll := slices.Contains(allowedOrigins, "*")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Non-browser callers send no Origin and need no CORS headers.
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			allowed := allowAll || slices.Contains(allowedOrigins, origin)
			if allowed {
				if allowAll {
					w.Header().Set("Access-Control-Allow-Origin", "*")
				} else {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Access-Control-Allow-Credentials", "true")
					// The response varies per origin, so caches must not reuse it.
					w.Header().Add("Vary", "Origin")
				}
			}

			if r.Method == http.MethodOptions {
				if allowed {
					w.Header().Set("Access-Control-Allow-Methods",
						strings.Join([]string{
							http.MethodGet, http.MethodPost, http.MethodPut,
							http.MethodPatch, http.MethodDelete, http.MethodOptions,
						}, ", "))
					w.Header().Set("Access-Control-Allow-Headers",
						"Authorization, Content-Type, X-Request-ID")
					w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID, Retry-After")
					w.Header().Set("Access-Control-Max-Age", strconv.Itoa(int(maxAge.Seconds())))
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// SecurityHeaders sets conservative defaults for an API that serves JSON only.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Resource-Policy", "same-site")
		next.ServeHTTP(w, r)
	})
}
