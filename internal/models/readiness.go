package models

import (
	"strings"
	"unicode/utf8"

	"github.com/yusufkaraaslan/play-more/internal/storage"
)

// The store-page bar. Named constants rather than literals because three
// callers have to agree on them — the readiness endpoint, the publish gate,
// and the deploy CLI's error message — and a bar that drifts between them is
// worse than no bar at all.
//
// Deliberately a *completeness* check, not a quality one: 80 characters of
// nonsense passes. It exists to keep empty placeholder pages out of the store,
// which is a thing software can actually verify. Judging whether a description
// is any good is not.
const (
	MinStoreDescriptionRunes = 80
	MinStoreScreenshots      = 1
	MinStoreTags             = 3
)

// ReadinessItem is one line of the checklist, shaped for direct rendering:
// the frontend shows Label, and Hint on the ones still outstanding.
type ReadinessItem struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Done  bool   `json:"done"`
	Hint  string `json:"hint,omitempty"`
}

// StoreReadiness reports whether a game's store page is complete enough to
// publish, item by item. Exempt is NOT consulted here — this answers "is the
// page complete", and the caller decides what that implies. Keeping the two
// apart is what lets the dashboard nag grandfathered games without implying
// they are about to be taken down.
type StoreReadinessResult struct {
	Ready bool            `json:"ready"`
	Items []ReadinessItem `json:"items"`
	// Missing carries the labels of unmet items so an API error or a CLI can
	// render the reason without walking Items.
	Missing []string `json:"missing,omitempty"`
}

// StoreReadiness evaluates g against the store-page bar. It reads only fields
// already present on a scanned Game, so callers never need an extra query.
func StoreReadiness(g *Game) StoreReadinessResult {
	// Runes, not bytes: a Turkish or Japanese description hits a byte-length
	// threshold long before it says as much as an English one would.
	descLen := utf8.RuneCountInString(strings.TrimSpace(g.Description))

	shots := 0
	for _, s := range g.Screenshots {
		if strings.TrimSpace(s) != "" {
			shots++
		}
	}
	tags := 0
	for _, t := range g.Tags {
		if strings.TrimSpace(t) != "" {
			tags++
		}
	}

	items := []ReadinessItem{
		{
			Key:   "description",
			Label: "Description",
			Done:  descLen >= MinStoreDescriptionRunes,
			Hint:  "Write at least 80 characters describing your game.",
		},
		{
			Key:   "cover",
			Label: "Cover image",
			Done:  strings.TrimSpace(g.CoverPath) != "",
			Hint:  "Upload a cover image — it's what players see in the store grid.",
		},
		{
			Key:   "screenshots",
			Label: "Screenshot",
			Done:  shots >= MinStoreScreenshots,
			Hint:  "Add at least one screenshot of actual gameplay.",
		},
		{
			Key:   "tags",
			Label: "Tags",
			Done:  tags >= MinStoreTags,
			Hint:  "Add at least 3 tags so players can find your game.",
		},
	}

	res := StoreReadinessResult{Ready: true, Items: items}
	for _, it := range items {
		if !it.Done {
			res.Ready = false
			res.Missing = append(res.Missing, it.Label)
		}
	}
	return res
}

// IsStoreGateExempt reports whether a game predates the store-page gate and is
// therefore always publishable.
//
// Queried on demand rather than carried on Game: the flag is policy, not
// content, and only the publish transition and the readiness endpoint ever
// need it — adding it to the struct would mean touching every SELECT that
// feeds scanGame for no benefit at the other call sites.
func IsStoreGateExempt(gameID string) bool {
	var exempt bool
	// A missing row scans to the zero value (not exempt), which is the safe
	// default: an unknown game does not get to skip the gate.
	storage.DB.QueryRow(`SELECT store_gate_exempt FROM games WHERE id = ?`, gameID).Scan(&exempt)
	return exempt
}
