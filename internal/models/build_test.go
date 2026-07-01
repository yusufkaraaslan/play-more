package models_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
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

// TestBuild_RetentionRemovesOnDiskDirs is the regression test for
// the disk leak: when the retention sweep deletes an old inactive
// build row, its extracted files under the game's builds/ tree
// must be removed too — not left orphaned on disk forever.
func TestBuild_RetentionRemovesOnDiskDirs(t *testing.T) {
	_ = testutil.NewTestServer(t)

	// Point storage at a real temp dir so BuildDir paths exist and
	// removeBuildDirsUnderGame can act on them.
	prevGames := storage.GamesDir
	storage.GamesDir = t.TempDir()
	t.Cleanup(func() { storage.GamesDir = prevGames })

	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "RetentionDisk")

	// Create several inactive builds, each with a real on-disk dir
	// under the game's builds/ tree (mirrors the reupload handler).
	type made struct {
		id  string
		dir string
	}
	var builds []made
	for i := 0; i < 8; i++ {
		buildID := "build_" + uuid.NewString()
		dir := storage.BuildDir(gameID, buildID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir build dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("x"), 0o644); err != nil {
			t.Fatalf("write build file: %v", err)
		}
		b, err := models.CreateBuild(gameID, dir, "builds/"+buildID+"/index.html", 1, "", "", string(models.BuildChannelStable), owner.ID)
		if err != nil {
			t.Fatalf("CreateBuild #%d: %v", i, err)
		}
		builds = append(builds, made{id: b.ID, dir: dir})
	}

	// The oldest inactive build is well past the cap and must be
	// gone from BOTH the DB and disk.
	oldest := builds[0]
	if _, err := models.GetBuild(oldest.id, gameID, owner.ID); err == nil {
		t.Errorf("oldest build should have been GC'd from the DB")
	}
	if _, err := os.Stat(oldest.dir); !os.IsNotExist(err) {
		t.Errorf("oldest build dir should have been removed from disk, stat err = %v", err)
	}

	// The most recent build must survive on disk.
	newest := builds[len(builds)-1]
	if _, err := os.Stat(newest.dir); err != nil {
		t.Errorf("newest build dir must still exist, stat err = %v", err)
	}
}

// TestPreviousActiveBuild_PicksPreceding is the regression test
// for the rollback-moves-forward bug: PreviousActiveBuild must
// return the build immediately BEFORE the active one, never a
// newer inactive build.
func TestPreviousActiveBuild_PicksPreceding(t *testing.T) {
	_ = testutil.NewTestServer(t)
	owner := testutil.SeedUser(t, nil, testutil.SeedUserOpts{EmailVerified: true})
	gameID := testutil.SeedGame(t, nil, owner.ID, "RollbackOrder") // build #1, active stable

	dir := func() string { return filepath.Join(t.TempDir(), "b") }

	// Build #2 (stable), then activate it → #1 inactive, #2 active.
	b2, err := models.CreateBuild(gameID, dir(), "index.html", 1, "", "", string(models.BuildChannelStable), owner.ID)
	if err != nil {
		t.Fatalf("CreateBuild #2: %v", err)
	}
	if err := models.SetActiveBuild(b2.ID, gameID, owner.ID); err != nil {
		t.Fatalf("activate #2: %v", err)
	}

	// Build #3 (stable), left inactive and NEWER than the active #2.
	if _, err := models.CreateBuild(gameID, dir(), "index.html", 1, "", "", string(models.BuildChannelStable), owner.ID); err != nil {
		t.Fatalf("CreateBuild #3: %v", err)
	}

	prev, err := models.PreviousActiveBuild(gameID, string(models.BuildChannelStable))
	if err != nil {
		t.Fatalf("PreviousActiveBuild: %v", err)
	}
	if prev.BuildNumber != 1 {
		t.Errorf("rollback target must be build #1 (the one before active #2), got #%d", prev.BuildNumber)
	}
}
