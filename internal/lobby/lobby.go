package lobby

import "time"

// Lobby is one live game lobby. All fields are guarded by the owning
// Hub's mutex — Lobby has no lock of its own.
type Lobby struct {
	Code       string
	GameID     string
	Host       *Session
	Members    []*Session // includes Host, in join order
	Started    bool
	Metadata   []byte // opaque JSON — game settings (map, difficulty, etc.)
	LastActive time.Time
}

// snapshot builds the client-facing State. Caller holds hub.mu.
func (l *Lobby) snapshot() *State {
	players := make([]Player, 0, len(l.Members))
	for _, m := range l.Members {
		players = append(players, Player{
			ID:        m.UserID,
			Username:  m.Username,
			AvatarURL: m.AvatarURL,
			Ready:     m.ready,
			Host:      m == l.Host,
		})
	}
	return &State{
		Code:     l.Code,
		GameID:   l.GameID,
		HostID:   l.Host.UserID,
		Started:  l.Started,
		Players:  players,
		Metadata: l.Metadata,
	}
}

// broadcastState sends the current snapshot to every member. Caller
// holds hub.mu.
func (l *Lobby) broadcastState() {
	state := l.snapshot()
	for _, m := range l.Members {
		m.trySend(ServerMsg{Type: "lobby", Lobby: state})
	}
}
