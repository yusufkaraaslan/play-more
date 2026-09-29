package lobby

import (
	"database/sql"
	"encoding/json"
	"log"
	"time"

	"github.com/yusufkaraaslan/play-more/internal/storage"
)

// Each hub owns an ordered persistence queue. Only immutable snapshots cross
// into the writer goroutine; deletes share the queue so an older upsert cannot
// resurrect a deleted lobby. The writer captures its DB instead of reading the
// mutable package-global storage.DB.
type lobbyWrite struct {
	code, gameID, metadata, members, former, lastActive string
	maxPlayers                                          int
	started, deleted                                    bool
}
type lobbyStore struct {
	db     *sql.DB
	writes chan lobbyWrite
	done   chan struct{}
}

// UseDatabase attaches persistence before serving connections. Passing nil
// drains and stops the previous writer, useful during shutdown and in tests.
func (h *Hub) UseDatabase(db *sql.DB) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.store != nil {
		h.store.close()
		h.store = nil
	}
	if db == nil {
		return
	}
	store := &lobbyStore{db: db, writes: make(chan lobbyWrite, 256), done: make(chan struct{})}
	h.store = store
	go store.run()
}
func (s *lobbyStore) close() { close(s.writes); <-s.done }
func (s *lobbyStore) run() {
	defer close(s.done)
	for w := range s.writes {
		var err error
		if w.deleted {
			_, err = s.db.Exec(`DELETE FROM lobbies WHERE code = ?`, w.code)
		} else {
			_, err = s.db.Exec(
				`INSERT INTO lobbies (code, game_id, max_players, started, metadata, member_ids, former_member_ids, last_active)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
				 ON CONFLICT(code) DO UPDATE SET max_players=excluded.max_players,
				 started=excluded.started, metadata=excluded.metadata, member_ids=excluded.member_ids,
				 former_member_ids=excluded.former_member_ids, last_active=excluded.last_active`,
				w.code, w.gameID, w.maxPlayers, w.started, w.metadata, w.members, w.former, w.lastActive)
		}
		if err != nil {
			log.Printf("lobby persistence error for %s: %v", w.code, err)
		}
	}
}

// Called with h.mu held. Backpressure bounds memory without dropping state.
func (h *Hub) persistLobby(l *Lobby) {
	if h.store == nil {
		return
	}
	members := make([]string, 0, len(l.Members))
	for _, m := range l.Members {
		members = append(members, m.UserID)
	}
	former := make([]string, 0, len(l.FormerMembers))
	for id := range l.FormerMembers {
		former = append(former, id)
	}
	memberJSON, _ := json.Marshal(members)
	formerJSON, _ := json.Marshal(former)
	h.store.writes <- lobbyWrite{code: l.Code, gameID: l.GameID,
		maxPlayers: l.MaxPlayers, started: l.Started, metadata: string(l.Metadata),
		members: string(memberJSON), former: string(formerJSON),
		lastActive: l.LastActive.UTC().Format(time.RFC3339)}
}
func (h *Hub) deleteLobby(code string) {
	if h.store != nil {
		h.store.writes <- lobbyWrite{code: code, deleted: true}
	}
}

// RestoreLobbies loads active lobbies from the DB into the hub. Called
// once on server startup. Lobbies with last_active within IdleTTL are
// restored — their members are gone (WebSocket disconnected) but
// FormerMembers is populated so players can rejoin.
func (h *Hub) RestoreLobbies() {
	h.UseDatabase(storage.DB)
	if storage.DB == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	cutoff := time.Now().Add(-IdleTTL).UTC().Format(time.RFC3339)
	rows, err := storage.DB.Query(
		`SELECT code, game_id, max_players, started, metadata, member_ids, former_member_ids FROM lobbies WHERE last_active > ?`,
		cutoff,
	)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var code, gameID, metaStr, memberJSON, formerJSON string
		var started, maxPlayers int
		if err := rows.Scan(&code, &gameID, &maxPlayers, &started, &metaStr, &memberJSON, &formerJSON); err != nil {
			continue
		}
		if maxPlayers < 2 || maxPlayers > MaxPlayers {
			maxPlayers = MaxPlayers
		}
		var memberIDs, formerIDs []string
		json.Unmarshal([]byte(memberJSON), &memberIDs)
		json.Unmarshal([]byte(formerJSON), &formerIDs)

		l := &Lobby{
			Code:          code,
			GameID:        gameID,
			MaxPlayers:    maxPlayers,
			Started:       started == 1,
			Metadata:      []byte(metaStr),
			Members:       nil, // no sessions — players must reconnect
			FormerMembers: make(map[string]bool),
			LastActive:    time.Now(),
		}
		// All previous members + former members can rejoin.
		for _, id := range memberIDs {
			l.FormerMembers[id] = true
		}
		for _, id := range formerIDs {
			l.FormerMembers[id] = true
		}
		h.lobbies[code] = l
	}
}
