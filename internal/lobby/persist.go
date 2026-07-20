package lobby

import (
	"encoding/json"
	"log"
	"time"

	"github.com/yusufkaraaslan/play-more/internal/storage"
)

// persistCh is a buffered channel for async lobby persistence. The hub
// sends lobby pointers here instead of writing to the DB synchronously,
// so the global mutex is only held for the channel send (nanoseconds),
// not for the DB write (which could block under disk contention).
var persistCh = make(chan *Lobby, 256)

func init() {
	go persistWorker()
}

func persistWorker() {
	for l := range persistCh {
		persistLobbySync(l)
	}
}

// persistLobby enqueues a lobby for async DB persistence. Non-blocking —
// if the channel is full (extreme contention), the write is dropped
// rather than blocking the hub mutex.
func persistLobby(l *Lobby) {
	if storage.DB == nil {
		return
	}
	select {
	case persistCh <- l:
	default:
		log.Printf("lobby persist channel full, dropping write for %s", l.Code)
	}
}

// persistLobbySync writes the lobby state to the DB. Called by the
// persist worker goroutine.
func persistLobbySync(l *Lobby) {
	if storage.DB == nil {
		return
	}
	memberIDs := make([]string, 0, len(l.Members))
	for _, m := range l.Members {
		memberIDs = append(memberIDs, m.UserID)
	}
	formerIDs := make([]string, 0, len(l.FormerMembers))
	for id := range l.FormerMembers {
		formerIDs = append(formerIDs, id)
	}
	memberJSON, _ := json.Marshal(memberIDs)
	formerJSON, _ := json.Marshal(formerIDs)
	metaStr := ""
	if l.Metadata != nil {
		metaStr = string(l.Metadata)
	}
	_, err := storage.DB.Exec(
		`INSERT INTO lobbies (code, game_id, max_players, started, metadata, member_ids, former_member_ids, last_active)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(code) DO UPDATE SET
		   max_players = excluded.max_players,
		   started = excluded.started,
		   metadata = excluded.metadata,
		   member_ids = excluded.member_ids,
		   former_member_ids = excluded.former_member_ids,
		   last_active = excluded.last_active`,
		l.Code, l.GameID, l.MaxPlayers, l.Started, metaStr, string(memberJSON), string(formerJSON),
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		log.Printf("lobby persist error for %s: %v", l.Code, err)
	}
}

// deleteLobby removes a lobby from the DB. Called when a lobby is closed.
func deleteLobby(code string) {
	if storage.DB == nil {
		return
	}
	storage.DB.Exec(`DELETE FROM lobbies WHERE code = ?`, code)
}

// RestoreLobbies loads active lobbies from the DB into the hub. Called
// once on server startup. Lobbies with last_active within IdleTTL are
// restored — their members are gone (WebSocket disconnected) but
// FormerMembers is populated so players can rejoin.
func (h *Hub) RestoreLobbies() {
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
