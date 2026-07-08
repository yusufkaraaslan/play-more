package lobby

import "encoding/json"

// Wire protocol for the /ws multiplayer lobby endpoint. Clients send
// ClientMsg frames, the server answers with ServerMsg frames. Every
// membership or ready-state change broadcasts a full State snapshot
// (type "lobby") — snapshots keep the client trivially simple compared
// to deltas, and lobbies are small (MaxPlayers) so the cost is nil.

// ClientMsg is a client → server frame.
type ClientMsg struct {
	// Type is one of: create, join, leave, ready, start, msg, set_metadata.
	Type string `json:"type"`
	// GameID — for create: the game to open a lobby for.
	GameID string `json:"game_id,omitempty"`
	// Code — for join: the lobby code to join (case-insensitive).
	Code string `json:"code,omitempty"`
	// Ready — for ready: the new ready state.
	Ready bool `json:"ready,omitempty"`
	// To — for msg: optional target player ID. Empty = broadcast to
	// every other lobby member.
	To string `json:"to,omitempty"`
	// Data — for msg: opaque game payload, relayed verbatim.
	Data json.RawMessage `json:"data,omitempty"`
	// Metadata — for create/set_metadata: opaque JSON object with game
	// settings (map, difficulty, mode, etc.). Host-only on set_metadata.
	Metadata json.RawMessage `json:"metadata,omitempty"`
	// Spectator — for join: if true, join as a read-only observer.
	// Spectators bypass the started check and player cap, but can't send
	// game messages (msg type is rejected).
	Spectator bool `json:"spectator,omitempty"`
	// Public — for create: if true, the lobby is listed in the public
	// lobby browser (GET /api/v1/games/:id/lobbies).
	Public bool `json:"public,omitempty"`
}

// ServerMsg is a server → client frame.
type ServerMsg struct {
	// Type is one of: lobby, launch, msg, closed, error.
	Type string `json:"type"`
	// Lobby — for lobby/launch: full state snapshot.
	Lobby *State `json:"lobby,omitempty"`
	// From — for msg: sender player ID.
	From string `json:"from,omitempty"`
	// Data — for msg: opaque game payload, relayed verbatim.
	Data json.RawMessage `json:"data,omitempty"`
	// Reason — for closed: why the lobby went away (host_left, expired).
	Reason string `json:"reason,omitempty"`
	// Error — for error: human-readable message.
	Error string `json:"error,omitempty"`
}

// Player is the public view of a lobby member.
type Player struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	AvatarURL string `json:"avatar_url"`
	Ready     bool   `json:"ready"`
	Host      bool   `json:"host"`
	Spectator bool   `json:"spectator,omitempty"`
}

// State is a full lobby snapshot as sent to clients.
type State struct {
	Code     string          `json:"code"`
	GameID   string          `json:"game_id"`
	HostID   string          `json:"host_id"`
	Started  bool            `json:"started"`
	Players  []Player        `json:"players"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}
