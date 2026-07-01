package models_test

import (
	"path/filepath"
	"testing"

	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
	"github.com/yusufkaraaslan/play-more/internal/testutil"
)

// TestBuild_RetentionNeverDeletesActive is the regression test
// for the bug where the GC sweep inside CreateBuild would
// delete an active build when the game had MaxBuildsPerGame+1
// active builds (one per channel, etc.).
func TestBuild_RetentionNeverDeletesActive(t *testing.T) {
	_ = testutil.NewTestServer(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "RetentionGame")

	// Create the first build, then immediately activate it.
	// Now subsequent CreateBuilds should leave the first one
	// in place even though the per-game cap (5) is exceeded —
	// the fix is that the retention sweep excludes is_active=1.
	first, err := models.CreateBuild(
		gameID,
		filepath.Join(t.TempDir(), "build"),
		"index.html",
		100,
		"",
		"",
		string(models.BuildChannelBeta),
		owner.ID,
	)
	if err != nil {
		t.Fatalf("first CreateBuild: %v", err)
	}
	if err := models.SetActiveBuild(first.ID, gameID, owner.ID); err != nil {
		t.Fatalf("SetActiveBuild: %v", err)
	}

	// Now add 6 more builds on the same channel. Without the
	// fix, the first (active) one would be GC'd.
	for i := 0; i < 6; i++ {
		if _, err := models.CreateBuild(
			gameID,
			filepath.Join(t.TempDir(), "build"),
			"index.html",
			100,
			"",
			"",
			string(models.BuildChannelBeta),
			owner.ID,
		); err != nil {
			t.Fatalf("subsequent CreateBuild #%d: %v", i, err)
		}
	}

	// The first build must still exist and still be active.
	got, err := models.GetBuild(first.ID, gameID, owner.ID)
	if err != nil {
		t.Fatalf("first build gone after retention sweep: %v", err)
	}
	if !got.IsActive {
		t.Errorf("first build lost is_active after sweep: %+v", got)
	}

	// Cleanup: silence unused-import warning if any future edit drops `storage`.
	_ = storage.MaxFileSize
}
