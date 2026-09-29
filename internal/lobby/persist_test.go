package lobby

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func persistenceDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "lobby.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE lobbies (code TEXT PRIMARY KEY, game_id TEXT, max_players INTEGER, started BOOLEAN, metadata TEXT, member_ids TEXT, former_member_ids TEXT, last_active TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestPersistenceSnapshotsAndOrderedDelete(t *testing.T) {
	db := persistenceDB(t)
	h := NewHub()
	h.UseDatabase(db)
	host := register(t, h, "host")
	guest := register(t, h, "guest")
	if err := h.Create(host, "game", 8); err != nil {
		t.Fatal(err)
	}
	code := host.lobby.Code
	for i := 0; i < 100; i++ {
		if err := h.Join(guest, code); err != nil {
			t.Fatal(err)
		}
		h.SetMetadata(host, []byte(`{"map":"arena"}`))
		h.Leave(guest)
		drain(host)
		drain(guest)
	}
	h.Leave(host)
	h.UseDatabase(nil) // ordered drain: no queued upsert may resurrect the lobby
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM lobbies`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("deleted lobby resurrected: %d", count)
	}
}

func TestShutdownPersistsBeforeDisconnect(t *testing.T) {
	db := persistenceDB(t)
	h := NewHub()
	h.UseDatabase(db)
	host := register(t, h, "host")
	if err := h.Create(host, "game", 4); err != nil {
		t.Fatal(err)
	}
	h.Shutdown()
	h.Unregister(host) // socket teardown must not delete the restart snapshot
	var members string
	if err := db.QueryRow(`SELECT member_ids FROM lobbies`).Scan(&members); err != nil {
		t.Fatal(err)
	}
	if members != `["host"]` {
		t.Fatalf("lost members on shutdown: %s", members)
	}
}
