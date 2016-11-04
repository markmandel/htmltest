package htmltest

import (
	"github.com/wjdp/htmltest/htmldoc"
	"github.com/wjdp/htmltest/issues"
	"github.com/wjdp/htmltest/refcache"
	"golang.org/x/net/html"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
)

func CheckLink(document *htmldoc.Document, node *html.Node) {
	attrs := extractAttrs(node.Attr, []string{"href", "rel", "data-proofer-ignore"})

	// Do not check canonical links
	if attrs["rel"] == "canonical" {
		return
	}
	// Ignore if data-proofer-ignore set
	if attrPresent(node.Attr, "data-proofer-ignore") {
		return
	}

	// Check href present, fail for link nodes
	if !attrPresent(node.Attr, "href") {
		switch node.Data {
		case "a":
			issues.AddIssue(issues.Issue{
				Level:    issues.DEBUG,
				Message:  "anchor without href",
				Document: document,
			})
			return
		case "link":
			issues.AddIssue(issues.Issue{
				Level:    issues.ERROR,
				Message:  "link tag missing href",
				Document: document,
			})
			return
		}
	}

	// Create reference
	// TODO move above attrPresent href block, pass reference into issues
	ref := htmldoc.NewReference(document, node, attrs["href"])

	// Blank href
	if attrs["href"] == "" {
		issues.AddIssue(issues.Issue{
			Level:     issues.ERROR,
			Message:   "href blank",
			Reference: ref,
		})
		return
	}

	// href="#"
	if attrs["href"] == "#" {
		issues.AddIssue(issues.Issue{
			Level:     issues.ERROR,
			Message:   "empty hash",
			Reference: ref,
		})
		return
	}

	// Route reference check
	switch ref.Scheme {
	case "http":
		if Opts.EnforceHTTPS {
			issues.AddIssue(issues.Issue{
				Level:     issues.ERROR,
				Message:   "is not an HTTPS target",
				Reference: ref,
			})
		}
		CheckExternal(ref)
	case "https":
		CheckExternal(ref)
	case "file":
		CheckInternal(ref)
	case "mailto":
		CheckMailto(ref)
	case "tel":
		CheckTel(ref)
	}

	// TODO: Other schemes
	// What to do about unknown schemes, could be perfectly valid or a typo.
	// Perhaps show a warning, which can be suppressed per-scheme in options.
	// Preload with a couple of common ones, ftp &c.

}

func CheckExternal(ref *htmldoc.Reference) {
	if !Opts.CheckExternal {
		issues.AddIssue(issues.Issue{
			Level:     issues.DEBUG,
			Message:   "skipping",
			Reference: ref,
		})
		return
	}

	urlStr := htmldoc.URLString(ref)
	if Opts.StripQueryString && !InList(Opts.StripQueryExcludes, urlStr) {
		urlStr = htmldoc.URLStripQueryString(urlStr)
	}
	var statusCode int

	if refcache.CachedURLStatus(urlStr) != 0 {
		// If we have the result in cache, return that
		statusCode = refcache.CachedURLStatus(urlStr)
	} else {
		// log.Println("Ext", ref.Document.Path, htmldoc.URLString(ref))
		urlUrl, err := url.Parse(urlStr)
		req := &http.Request{
			Method: "GET",
			URL:    urlUrl,
			Header: map[string][]string{
				"Range": {"bytes=0-63"}, // If server supports prevents body being sent
			},
		}
		_ = req
		resp, err := httpClient.Do(req)
		// resp, err := httpClient.Get(urlStr)

		if err != nil {
			if strings.Contains(err.Error(), "dial tcp") {
				// Remove long prefix
				prefix := "Get " + urlStr + ": dial tcp: lookup "
				cleanedMessage := strings.TrimPrefix(err.Error(), prefix)
				// Add error
				issues.AddIssue(issues.Issue{
					Level:     issues.ERROR,
					Message:   cleanedMessage,
					Reference: ref,
				})
				return
			}
			if strings.Contains(err.Error(), "Client.Timeout") {
				issues.AddIssue(issues.Issue{
					Level:     issues.ERROR,
					Message:   "request exceeded our ExternalTimeout",
					Reference: ref,
				})
				return
			}

			// Unhandled client error, return generic error
			issues.AddIssue(issues.Issue{
				Level:     issues.ERROR,
				Message:   err.Error(),
				Reference: ref,
			})
			log.Println("Unhandled httpClient error:", err.Error())
			return
		}
		// Save cached result
		refcache.SetCachedURLStatus(urlStr, resp.StatusCode)
		statusCode = resp.StatusCode
		// if statusCode == 200 { log.Println(urlStr) }
	}

	switch statusCode {
	case http.StatusOK: //, http.StatusPartialContent:
		issues.AddIssue(issues.Issue{
			Level:     issues.DEBUG,
			Message:   http.StatusText(statusCode),
			Reference: ref,
		})
	case http.StatusPartialContent:
		issues.AddIssue(issues.Issue{
			Level:     issues.DEBUG,
			Message:   http.StatusText(statusCode),
			Reference: ref,
		})
	default:
		// log.Println(urlStr)
		issues.AddIssue(issues.Issue{
			Level:     issues.ERROR,
			Message:   http.StatusText(statusCode),
			Reference: ref,
		})
	}

	// TODO check a hash id exists in external page if present in reference (URL.Fragment)

}

func CheckInternal(ref *htmldoc.Reference) {
	if !Opts.CheckInternal {
		issues.AddIssue(issues.Issue{
			Level:     issues.DEBUG,
			Message:   "skipping",
			Reference: ref,
		})
		return
	}
	// log.Println("CheckInternal", ref.Document.Path, htmldoc.AbsolutePath(ref))

	CheckFile(ref, htmldoc.AbsolutePath(ref))
}

func CheckFile(ref *htmldoc.Reference, fPath string) {
	// fPath should be relative to site root
	checkPath := path.Join(Opts.DirectoryPath, fPath)
	f, err := os.Stat(checkPath)
	if os.IsNotExist(err) {
		issues.AddIssue(issues.Issue{
			Level:     issues.ERROR,
			Message:   "target does not exist",
			Reference: ref,
		})
		return
	}
	checkErr(err) // Crash on other errors

	if f.IsDir() {
		if !strings.HasSuffix(ref.Path, "/") {
			issues.AddIssue(issues.Issue{
				Level:     issues.ERROR,
				Message:   "target is a directory, href lacks trailing slash",
				Reference: ref,
			})
			return
		}

		issues.AddIssue(issues.Issue{
			Level:     issues.DEBUG,
			Message:   "target is a directory",
			Reference: ref,
		})
		CheckFile(ref, path.Join(fPath, Opts.DirectoryIndex))
		return
	}
}

func CheckMailto(ref *htmldoc.Reference) {
	if !Opts.CheckMailto {
		return
	}
	if len(ref.URL.Opaque) == 0 {
		issues.AddIssue(issues.Issue{
			Level:     issues.ERROR,
			Message:   "mailto is empty",
			Reference: ref,
		})
		return
	}
	if !strings.Contains(ref.URL.Opaque, "@") {
		issues.AddIssue(issues.Issue{
			Level:     issues.ERROR,
			Message:   "contains an invalid email address",
			Reference: ref,
		})
		return
	}
}

func CheckTel(ref *htmldoc.Reference) {
	if !Opts.CheckTel {
		return
	}
	if len(ref.URL.Opaque) == 0 {
		issues.AddIssue(issues.Issue{
			Level:     issues.ERROR,
			Message:   "tel is empty",
			Reference: ref,
		})
		return
	}
}
