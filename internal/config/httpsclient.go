package config

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// maxHTTPSRedirects matches net/http's own default redirect cap, which a
// custom CheckRedirect replaces and so has to re-impose itself.
const maxHTTPSRedirects = 10

// HTTPSOnlyClient returns an *http.Client for fetching remote content astrona
// executes, applies or trusts (scripts, manifests, lab configs, base images,
// release binaries). Checking the first URL for https:// isn't enough on its
// own: Go's default client follows an https→http redirect, so the content
// could still arrive over plain HTTP. This client refuses any redirect whose
// target isn't https://, and keeps Go's 10-redirect cap.
func HTTPSOnlyClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:       timeout,
		CheckRedirect: httpsOnlyRedirect,
	}
}

func httpsOnlyRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxHTTPSRedirects {
		return errors.New("stopped after 10 redirects")
	}
	if req.URL.Scheme != "https" {
		return fmt.Errorf("refusing redirect to non-https URL '%s': only https:// sources are allowed", req.URL.Redacted())
	}
	return nil
}
