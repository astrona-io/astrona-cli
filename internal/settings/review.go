package settings

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"time"
)

// ReviewedSetting is what a settings review acknowledges about one setting.
type ReviewedSetting struct {
	Revision       int    `json:"revision"`
	Default        string `json:"default"`
	Description    string `json:"description"`
	ExplicitChoice bool   `json:"explicitChoice"`
}

// Receipt is settings-review.json: the definitions the user last reviewed.
type Receipt struct {
	Format      int                        `json:"format"`
	CLIVersion  string                     `json:"cliVersion"`
	ReviewedAt  time.Time                  `json:"reviewedAt"`
	Definitions map[string]ReviewedSetting `json:"definitions"`
}

// ReviewDefinitions returns the current definitions as a review sees them.
func ReviewDefinitions() map[string]ReviewedSetting {
	defs := make(map[string]ReviewedSetting)
	for _, d := range Definitions() {
		r := ReviewedSetting{Revision: 1, Default: d.Default, Description: d.Description}
		if d.Key == "update-check" {
			r.Description += ". Contacts GitHub for release information; GitHub receives your IP address. Disabled in CI and non-terminal sessions."
		}
		defs[d.Key] = r
	}
	return defs
}

// ReceiptPath returns settings-review.json beside the settings file path.
func ReceiptPath(path string) string {
	return filepath.Join(filepath.Dir(path), "settings-review.json")
}

// LoadReceipt reads the review receipt beside path. A missing receipt is an
// empty one; a damaged receipt is an error.
func LoadReceipt(path string) (Receipt, error) {
	var receipt Receipt
	data, err := readFile(ReceiptPath(path))
	if err != nil || data == nil {
		return receipt, err
	}
	if err := json.Unmarshal(data, &receipt); err != nil || receipt.Format != 1 || receipt.ReviewedAt.IsZero() || receipt.Definitions == nil {
		return receipt, fmt.Errorf("invalid settings review in %s; repair or remove that receipt, then run astrona settings review", ReceiptPath(path))
	}
	return receipt, nil
}

func changedSettings(previous, current map[string]ReviewedSetting) []string {
	var keys []string
	for key, def := range current {
		if old, ok := previous[key]; !ok || old != def {
			keys = append(keys, key)
		}
	}
	for key := range previous {
		if _, ok := current[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// Pending lists, sorted, the settings a review still has to ask about:
// added, changed or removed definitions, and explicit-choice settings with no
// saved value.
func Pending(previous, current map[string]ReviewedSetting, saved Values) []string {
	changes := changedSettings(previous, current)
	for key, def := range current {
		_, explicit := saved[key]
		old, known := previous[key]
		if def.ExplicitChoice && !explicit && known && old == def {
			changes = append(changes, key)
		}
	}
	sort.Strings(changes)
	return changes
}

// LoadReviewValues loads the saved values, tolerating keys that previous
// definitions had and current ones dropped, so a review can remove them.
func LoadReviewValues(path string, previous, current map[string]ReviewedSetting) (Values, error) {
	retired := make(map[string]bool)
	for key := range previous {
		if _, exists := current[key]; !exists {
			retired[key] = true
		}
	}
	return loadWithRetired(path, retired)
}

// ReviewID binds automation approval to both the definitions and saved
// choices. A release number alone cannot identify a settings change in a
// developer build.
func ReviewID(defs map[string]ReviewedSetting, saved Values) (string, error) {
	data, err := json.Marshal(struct {
		Definitions map[string]ReviewedSetting `json:"definitions"`
		Saved       Values                     `json:"saved"`
	}{defs, saved})
	if err != nil {
		return "", fmt.Errorf("encode settings review: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
