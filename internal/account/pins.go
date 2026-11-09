package account

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

// maxCatalog caps GET {site}/api/labs: the whole lab catalog, larger than
// the other answers this package reads.
const maxCatalog = 16 << 20

// OfficialLabPins is GET {site}/api/labs read for one thing: the commit
// each official training repository was published at (the catalog's
// "sourceSha", set when Astrona synced it from GitHub), by repository name
// in lower case. A training without a full commit id has no pin — its labs
// are not pre-trusted. The request carries no credentials.
func (c *Client) OfficialLabPins(ctx context.Context, site string) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, site+"/api/labs", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Trainings []struct {
			Repo      string `json:"repo"`
			SourceSha string `json:"sourceSha"`
		} `json:"trainings"`
	}
	if _, err := doLimited(c.HTTP, req, &out, maxCatalog); err != nil {
		return nil, fmt.Errorf("could not read %s's lab catalog: %w", site, err)
	}
	pins := map[string]string{}
	for _, t := range out.Trainings {
		repo := strings.ToLower(strings.TrimSpace(t.Repo))
		sha := strings.ToLower(strings.TrimSpace(t.SourceSha))
		if repo == "" || !fullCommitID(sha) {
			continue
		}
		pins[repo] = sha
	}
	return pins, nil
}

// fullCommitID accepts a full SHA-1 or SHA-256 git object id.
func fullCommitID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
