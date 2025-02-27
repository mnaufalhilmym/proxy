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
	decodedBytes, err := base64.StdEncoding.DecodeString(encodedURL)
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

	// Create a new request to the upstream server.
	// Note: r.Body is already an io.ReadCloser, so it streams the body.
	req, err := http.NewRequest(r.Method, upstreamURL, r.Body)
	if err != nil {
		http.Error(w, "Failed to create upstream request: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Copy all headers except "Host".
	for key, values := range r.Header {
		if strings.ToLower(key) == "host" {
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

	// Copy upstream response headers.
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Set the status code.
	w.WriteHeader(resp.StatusCode)

	/// Stream the response body to the client.
	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Printf("Error streaming response: %v", err)
	}
}

func main() {
	http.HandleFunc("/", proxyHandler)
	log.Println("Listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
