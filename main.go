package main

import (
	"encoding/base64"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
)

func proxyHandler(w http.ResponseWriter, r *http.Request) {
	// Expect the encoded URL in the first path segment.
	// For example: /aHR0cHM6Ly9leGFtcGxlLmNvbQ==
	encodedURL := strings.TrimPrefix(r.URL.Path, "/")
	if encodedURL == "" {
		http.Error(w, "Missing encoded URL", http.StatusBadRequest)
		return
	}

	// Decode the base64-encoded URL.
	decodedBytes, err := base64.URLEncoding.DecodeString(encodedURL)
	if err != nil {
		http.Error(w, "Invalid base64 encoding: "+err.Error(), http.StatusBadRequest)
		return
	}
	upstreamURL := string(decodedBytes)

	// Validate the upstream URL.
	parsedURL, err := url.Parse(upstreamURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		http.Error(w, "Invalid upstream URL", http.StatusBadRequest)
		return
	}

	// Log the incoming request.
	log.Printf("Incoming request: %s %s from %s, proxying to %s", r.Method, r.URL.String(), r.RemoteAddr, upstreamURL)

	// Determine if the "browse" query parameter is set.
	browseEnabled := r.URL.Query().Get("browse") != ""

	// Create a new request to the upstream server.
	// Note: r.Body is already an io.ReadCloser, so it streams the body.
	req, err := http.NewRequest(r.Method, upstreamURL, r.Body)
	if err != nil {
		http.Error(w, "Failed to create upstream request: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Copy all headers except "Host".
	for key, values := range r.Header {
		keyLower := strings.ToLower(key)
		if keyLower == "host" {
			continue
		}
		if browseEnabled && keyLower == "accept-encoding" {
			continue
		}
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	// Send the request upstream.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "Upstream request failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// Log the upstream response status.
	log.Printf("Upstream response: %d for %s", resp.StatusCode, upstreamURL)

	// Build the proxy origin.
	origin := "http://" + r.Host
	if r.TLS != nil {
		origin = "https://" + r.Host
	}

	// Helper function to copy headers, excluding Content-Length if browsing is enabled.
	copyHeaders := func() {
		for key, values := range resp.Header {
			if browseEnabled && strings.ToLower(key) == "content-length" {
				continue
			}
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
	}

	// Conditionally rewrite content if browsing is enabled.
	contentType := resp.Header.Get("Content-Type")
	if browseEnabled && strings.HasPrefix(contentType, "text/html") {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			http.Error(w, "Error reading upstream HTML", http.StatusInternalServerError)
			return
		}
		rewritten, err := rewriteHTML(bodyBytes, parsedURL, origin)
		if err != nil {
			http.Error(w, "Error rewriting HTML: "+err.Error(), http.StatusInternalServerError)
			return
		}
		copyHeaders()
		w.WriteHeader(resp.StatusCode)
		w.Write(rewritten)
	} else if browseEnabled && strings.HasPrefix(contentType, "text/css") {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			http.Error(w, "Error reading upstream CSS", http.StatusInternalServerError)
			return
		}
		rewritten, err := rewriteCSS(bodyBytes, parsedURL, origin)
		if err != nil {
			http.Error(w, "Error rewriting CSS: "+err.Error(), http.StatusInternalServerError)
			return
		}
		copyHeaders()
		w.WriteHeader(resp.StatusCode)
		w.Write(rewritten)
	} else if browseEnabled && (strings.HasPrefix(contentType, "application/javascript") || strings.HasPrefix(contentType, "text/javascript")) {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			http.Error(w, "Error reading upstream JavaScript", http.StatusInternalServerError)
			return
		}
		rewritten, err := rewriteJS(bodyBytes, parsedURL, origin)
		if err != nil {
			http.Error(w, "Error rewriting JavaScript: "+err.Error(), http.StatusInternalServerError)
			return
		}
		copyHeaders()
		w.WriteHeader(resp.StatusCode)
		w.Write(rewritten)
	} else {
		// For non-rewritten content, simply copy the response headers and stream the body.
		copyHeaders()
		w.WriteHeader(resp.StatusCode)

		// Stream the response body to the client.
		if _, err := io.Copy(w, resp.Body); err != nil {
			log.Printf("Error streaming response: %v", err)
		}
	}
}

func main() {
	http.HandleFunc("/", proxyHandler)
	log.Println("Listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
