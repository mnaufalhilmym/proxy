package main

import (
	"bytes"
	"encoding/base64"
	"log"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// rewriteHTML parses the HTML content, traverses the nodes, and for attributes
// such as href, src, action, and formaction, resolves the URL relative to the base URL,
// then rewrites the attribute to use the proxy's path ("/" + base64(encodedURL)).
func rewriteHTML(htmlContent []byte, base *url.URL, origin string) ([]byte, error) {
	doc, err := html.Parse(bytes.NewReader(htmlContent))
	if err != nil {
		return nil, err
	}

	// Attributes to rewrite.
	rewriteAttrs := map[string]bool{
		"href":       true,
		"src":        true,
		"action":     true,
		"formaction": true,
	}

	// traverse recursively walks the HTML node tree and rewrites URL attributes.
	var traverse func(*html.Node)
	traverse = func(n *html.Node) {
		if n.Type == html.ElementNode {
			// Process inline <script> tags.
			if n.Data == "script" {
				// If there is no src attribute, it's inline.
				hasSrc := false
				for _, attr := range n.Attr {
					if strings.ToLower(attr.Key) == "src" {
						hasSrc = true
						break
					}
				}
				if !hasSrc {
					// Process all text nodes inside the script tag.
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						if c.Type == html.TextNode {
							rewritten, err := rewriteJS([]byte(c.Data), base, origin)
							if err == nil {
								c.Data = string(rewritten)
							} else {
								log.Printf("Error rewriting inline script: %v", err)
							}
						}
					}
				}
			}

			for i, attr := range n.Attr {
				if rewriteAttrs[strings.ToLower(attr.Key)] {
					// Do not rewrite data URIs.
					if strings.HasPrefix(attr.Val, "data:") {
						continue
					}
					// Resolve attribute value relative to the base URL.
					resolved, err := base.Parse(attr.Val)
					if err == nil {
						encoded := base64.URLEncoding.EncodeToString([]byte(resolved.String()))
						n.Attr[i].Val = origin + "/" + encoded + "?browse=1"
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			traverse(c)
		}
	}
	traverse(doc)

	// Render the modified HTML back to bytes.
	var buf bytes.Buffer
	if err := html.Render(&buf, doc); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// rewriteCSS rewrites URLs in CSS content, such as those in url(...) and @import rules.
func rewriteCSS(content []byte, base *url.URL, origin string) ([]byte, error) {
	text := string(content)

	// Rewrite url(...) references.
	urlRegex := regexp.MustCompile(`url\(\s*(["']?)([^"')]+)(["']?)\s*\)`)
	text = urlRegex.ReplaceAllStringFunc(text, func(match string) string {
		submatches := urlRegex.FindStringSubmatch(match)
		if len(submatches) < 3 {
			return match
		}
		quote := submatches[1]
		urlPart := submatches[2]
		resolved, err := base.Parse(urlPart)
		if err != nil {
			return match
		}
		encoded := base64.URLEncoding.EncodeToString([]byte(resolved.String()))
		return "url(" + quote + origin + "/" + encoded + "?browse=1" + quote + ")"
	})

	// Rewrite @import statements.
	importRegex := regexp.MustCompile(`@import\s+(["'])([^"']+)(["'])`)
	text = importRegex.ReplaceAllStringFunc(text, func(match string) string {
		submatches := importRegex.FindStringSubmatch(match)
		if len(submatches) < 3 {
			return match
		}
		quote := submatches[1]
		urlPart := submatches[2]
		resolved, err := base.Parse(urlPart)
		if err != nil {
			return match
		}
		encoded := base64.URLEncoding.EncodeToString([]byte(resolved.String()))
		return "@import " + quote + origin + "/" + encoded + "?browse=1" + quote
	})

	return []byte(text), nil
}

// rewriteJS rewrites absolute URL references in JavaScript string literals.
func rewriteJS(content []byte, base *url.URL, origin string) ([]byte, error) {
	text := string(content)

	// This regex matches string literals starting with "http" or "https"
	absRegex := regexp.MustCompile(`(["'])(https?://[^"']+)(["'])`)
	text = absRegex.ReplaceAllStringFunc(text, func(match string) string {
		submatches := absRegex.FindStringSubmatch(match)
		if len(submatches) < 4 {
			return match
		}
		openQuote := submatches[1]
		urlPart := submatches[2]
		closeQuote := submatches[3]
		resolved, err := base.Parse(urlPart)
		if err != nil {
			return match
		}
		encoded := base64.URLEncoding.EncodeToString([]byte(resolved.String()))
		return openQuote + origin + "/" + encoded + "?browse=1" + closeQuote
	})

	// Rewrite dynamic imports with relative paths.
	relImportRegex := regexp.MustCompile(`import\(\s*(["'])(\.{1,2}\/[^"']+)(["'])`)
	text = relImportRegex.ReplaceAllStringFunc(text, func(match string) string {
		submatches := relImportRegex.FindStringSubmatch(match)
		if len(submatches) < 4 {
			return match
		}
		openQuote := submatches[1]
		relPath := submatches[2]
		closeQuote := submatches[3]
		resolved, err := base.Parse(relPath)
		if err != nil {
			return match
		}
		encoded := base64.URLEncoding.EncodeToString([]byte(resolved.String()))
		// Note: The regex stops before the closing parenthesis.
		return "import(" + openQuote + origin + "/" + encoded + "?browse=1" + closeQuote
	})

	// Rewrite static import statements
	// Match static import statements in the form: from "..."
	staticImportRegex := regexp.MustCompile(`from\s*(["'])(\.{1,2}\/[^"']+)(["'])`)
	text = staticImportRegex.ReplaceAllStringFunc(text, func(match string) string {
		submatches := staticImportRegex.FindStringSubmatch(match)
		if len(submatches) < 4 {
			return match
		}
		openQuote, importPath, closeQuote := submatches[1], submatches[2], submatches[3]
		resolved, err := base.Parse(importPath)
		if err != nil {
			return match
		}
		encoded := base64.URLEncoding.EncodeToString([]byte(resolved.String()))
		newURL := origin + "/" + encoded + "?browse=1"
		return "from " + openQuote + newURL + closeQuote
	})

	// Rewrite URL function calls: URL("/blabla") -> URL("https://proxy.hilmy.dev/blabla")
	urlFuncRegex := regexp.MustCompile(`URL\(\s*(["'])(\/[^"']*)(["'])\s*\)`)
	text = urlFuncRegex.ReplaceAllStringFunc(text, func(match string) string {
		submatches := urlFuncRegex.FindStringSubmatch(match)
		if len(submatches) < 4 {
			return match
		}
		openQuote := submatches[1]
		relPath := submatches[2] // this is the relative path (starting with "/")
		closeQuote := submatches[3]
		return "URL(" + openQuote + origin + relPath + closeQuote + ")"
	})

	return []byte(text), nil
}
