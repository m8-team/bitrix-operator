package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
)

// ValidationIdentity prevents a finished Job from validating a newer rollout.
// Release must be a resolved digest, never a mutable tag. Attempt is the Job UID.
type ValidationIdentity struct {
	ClusterUID string `json:"clusterUID"`
	Generation int64  `json:"generation"`
	Release    string `json:"release"`
	Attempt    string `json:"attempt"`
}

// SiteResult is the bounded termination-message protocol, not a log scraper.
type SiteResult struct {
	Version  string             `json:"version"`
	Identity ValidationIdentity `json:"identity"`
	Sites    []string           `json:"sites"`
	Reason   string             `json:"reason"`
}

// ParseSiteResult rejects stale, malformed, truncated and unexpected results.
// The Job reconciler separately checks owner UID, runtime imageID and container exit status.
func ParseSiteResult(data []byte, expected ValidationIdentity, requested []string) (SiteResult, error) {
	var result SiteResult
	if len(data) == 0 || len(data) > 4096 {
		return result, fmt.Errorf("invalid site result size")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&result); err != nil {
		return SiteResult{}, fmt.Errorf("invalid site result JSON")
	}
	var trailing any
	if dec.Decode(&trailing) != io.EOF {
		return SiteResult{}, fmt.Errorf("trailing site result data")
	}
	if result.Version != ContractVersion || result.Identity != expected || expected.ClusterUID == "" || expected.Generation < 1 || expected.Attempt == "" || !digestPattern.MatchString(expected.Release) {
		return SiteResult{}, fmt.Errorf("stale or invalid site result identity")
	}
	if result.Reason != "Validated" && result.Reason != "SiteNotFound" && result.Reason != "ValidationUnavailable" {
		return SiteResult{}, fmt.Errorf("invalid site result reason")
	}
	if result.Reason == "ValidationUnavailable" && len(result.Sites) != 0 {
		return SiteResult{}, fmt.Errorf("unavailable result contains sites")
	}
	seen := map[string]bool{}
	for _, site := range result.Sites {
		if seen[site] || !slices.Contains(requested, site) {
			return SiteResult{}, fmt.Errorf("unexpected site result")
		}
		seen[site] = true
	}
	if result.Reason == "Validated" && len(seen) != len(requested) {
		return SiteResult{}, fmt.Errorf("incomplete site result")
	}
	if result.Reason == "SiteNotFound" && len(seen) == len(requested) {
		return SiteResult{}, fmt.Errorf("inconsistent site result")
	}
	return result, nil
}
