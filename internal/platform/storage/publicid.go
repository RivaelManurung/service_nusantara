package storage

import (
	"regexp"
	"strings"
)

// versionSegment matches Cloudinary's cache-busting version, e.g. "v1786610746".
var versionSegment = regexp.MustCompile(`^v\d+$`)

// PublicIDFromURL recovers the provider handle from a delivery URL.
//
// This exists only for the one-time backfill of rows written before the handle
// was stored. It is NOT used at upload time -- there the provider tells us the
// id directly, and parsing a URL to rediscover something we were already handed
// would be strictly worse.
//
// A derived id must still be verified against the provider before it is
// written: this returns a best guess, not a fact.
//
// Shape: https://res.cloudinary.com/<cloud>/image/upload/[<transforms>/][v123/]<public id>.<ext>
func PublicIDFromURL(rawURL string) (string, bool) {
	const marker = "/upload/"

	index := strings.Index(rawURL, marker)
	if index < 0 {
		return "", false
	}

	remainder := strings.Trim(rawURL[index+len(marker):], "/")
	if remainder == "" {
		return "", false
	}

	segments := strings.Split(remainder, "/")

	// Drop leading transformation and version segments. A transformation looks
	// like "w_200,c_fill" or "f_auto"; a real folder name would not normally
	// contain a comma, and a single-letter-underscore prefix is the giveaway.
	start := 0
	for start < len(segments)-1 {
		segment := segments[start]
		if versionSegment.MatchString(segment) || looksLikeTransformation(segment) {
			start++
			continue
		}
		break
	}

	path := strings.Join(segments[start:], "/")
	if path == "" {
		return "", false
	}

	// The public id excludes the format extension, but a dot may also appear in
	// a legitimate id, so only a short trailing extension is removed.
	if dot := strings.LastIndex(path, "."); dot > 0 && len(path)-dot <= 6 && !strings.Contains(path[dot:], "/") {
		path = path[:dot]
	}

	return path, path != ""
}

// looksLikeTransformation reports whether a path segment is a Cloudinary
// transformation rather than part of the asset's folder path.
func looksLikeTransformation(segment string) bool {
	for _, part := range strings.Split(segment, ",") {
		underscore := strings.Index(part, "_")
		// Transformation parameters are "<short key>_<value>", e.g. "w_200".
		if underscore < 1 || underscore > 3 {
			return false
		}
	}
	return true
}
