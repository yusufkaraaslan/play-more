package lobby

import (
	"crypto/rand"
	"errors"
	"sync"
	"time"
)

// Limits. Deliberately consts (not flags) — tune here if a deployment
// ever needs different numbers.
const (
	MaxPlayers      = 8               // players per lobby
	MaxConnsPerUser = 4               // concurrent /ws connections per user
	MaxLobbies      = 500             // global live-lobby cap (memory bound)
	SendBuffer      = 64              // per-connection outbound queue; overflow = slow consumer, disconnect
	IdleTTL         = 2 * time.Hour   // lobbies with no activity for this long are reaped
	cleanupEvery    = 1 * time.Minute // reaper cadence
)

var (
	ErrTooManyConns   = errors.New("too many connections")
	ErrTooManyLobbies = errors.New("server is at capacity, try again later")
	ErrLobbyNotFound  = errors.New("lobby not found")
	ErrLobbyFull      = errors.New("lobby is full")
	ErrLobbyStarted   = errors.New("game already started")
	ErrNotInLobby     = errors.New("not in a lobby")
	ErrNotHost        = errors.New("only the host can start the game")
	ErrNotReady       = errors.New("not all players are ready")
)

// Session is one connected /ws client. The HTTP handler owns the
// websocket; the hub only sees this abstraction — outbound frames go
// through the send channel (drained by the handler's writer goroutine),
// and done is closed to force a disconnect (slow consumer, abuse).
type Session struct {
	UserID    string
	Username  string
	AvatarURL string

	hub   *Hub
	lobby *Lobby // guarded by hub.mu
	ready bool   // guarded by hub.mu
	send  chan ServerMsg
	done  chan struct{}
	once  sync.Once
}

// Out is the outbound frame queue for the handler's writer goroutine.
func (s *Session) Out() <-chan ServerMsg { return s.send }

// Done is closed when the hub wants this connection gone.
func (s *Session) Done() <-chan struct{} { return s.done }

// Close force-disconnects the session (idempotent). The handler's
// reader/writer loops exit on Done.
func (s *Session) Close() { s.once.Do(func() { close(s.done) }) }

// Send queues a frame to this session from outside the hub (handler
// error replies). Same non-blocking semantics as trySend.
func (s *Session) Send(msg ServerMsg) { s.trySend(msg) }

// trySend queues a frame without blocking. A full queue means the
// client isn't draining (dead network, or a peer flooding a game whose
// player tabbed out) — disconnect it rather than block the hub.
func (s *Session) trySend(msg ServerMsg) {
	select {
	case s.send <- msg:
	case <-s.done:
	default:
		s.Close()
	}
}

// Hub is the in-memory lobby registry. One global instance (Default)
// lives for the process lifetime; lobbies are ephemeral and never
// touch the database. A single mutex guards everything — operations
// are tiny map/slice updates, and contention is bounded by MaxLobbies.
type Hub struct {
	mu        sync.Mutex
	lobbies   map[string]*Lobby // code → lobby
	userConns map[string]int    // user ID → live /ws connection count
	gameCount map[string]int    // game ID → players currently in a lobby
}

// Default is the process-wide hub used by the HTTP handlers.
var Default = NewHub()

func NewHub() *Hub {
	return &Hub{
		lobbies:   make(map[string]*Lobby),
		userConns: make(map[string]int),
		gameCount: make(map[string]int),
	}
}

// Register creates a Session for a newly-upgraded connection.
func (h *Hub) Register(userID, username, avatarURL string) (*Session, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.userConns[userID] >= MaxConnsPerUser {
		return nil, ErrTooManyConns
	}
	h.userConns[userID]++
	return &Session{
		UserID:    userID,
		Username:  username,
		AvatarURL: avatarURL,
		hub:       h,
		send:      make(chan ServerMsg, SendBuffer),
		done:      make(chan struct{}),
	}, nil
}

// Unregister removes a session when its connection ends: leaves its
// lobby (closing the lobby if it hosted one) and releases the per-user
// connection slot.
func (h *Hub) Unregister(s *Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.leaveLocked(s)
	if h.userConns[s.UserID] <= 1 {
		delete(h.userConns, s.UserID)
	} else {
		h.userConns[s.UserID]--
	}
	s.Close()
}

// Create opens a new lobby for gameID with s as host. If s is already
// in a lobby it leaves first (auto-leave beats erroring: it makes
// reconnect/retry flows self-healing). The caller (handler) is
// responsible for validating the game itself — existence, published,
// multiplayer flag — so the hub stays free of DB imports.
func (h *Hub) Create(s *Session, gameID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.lobbies) >= MaxLobbies {
		return ErrTooManyLobbies
	}
	h.leaveLocked(s)
	code, err := h.newCodeLocked()
	if err != nil {
		return err
	}
	l := &Lobby{
		Code:       code,
		GameID:     gameID,
		Host:       s,
		Members:    []*Session{s},
		LastActive: time.Now(),
	}
	h.lobbies[code] = l
	h.gameCount[gameID]++
	s.lobby = l
	s.ready = false
	l.broadcastState()
	return nil
}

// Join adds s to the lobby identified by code (auto-leaving any
// current lobby first, same rationale as Create).
func (h *Hub) Join(s *Session, code string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	l, ok := h.lobbies[normalizeCode(code)]
	if !ok {
		return ErrLobbyNotFound
	}
	if l.Started {
		return ErrLobbyStarted
	}
	if len(l.Members) >= MaxPlayers {
		return ErrLobbyFull
	}
	if s.lobby == l {
		l.broadcastState() // re-join of same lobby: just resend state
		return nil
	}
	h.leaveLocked(s)
	l.Members = append(l.Members, s)
	l.LastActive = time.Now()
	h.gameCount[l.GameID]++
	s.lobby = l
	s.ready = false
	l.broadcastState()
	return nil
}

// Leave removes s from its lobby (closing it if s hosts it).
func (h *Hub) Leave(s *Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.leaveLocked(s)
}

// Ready updates s's ready flag and broadcasts the new state.
func (h *Hub) Ready(s *Session, ready bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := s.lobby
	if l == nil {
		return ErrNotInLobby
	}
	s.ready = ready
	l.LastActive = time.Now()
	l.broadcastState()
	return nil
}

// Start launches the lobby's game. Host only; every other member must
// be ready (the host's click is their ready signal). Solo start is
// allowed — useful for developers testing their protocol integration.
func (h *Hub) Start(s *Session) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := s.lobby
	if l == nil {
		return ErrNotInLobby
	}
	if l.Host != s {
		return ErrNotHost
	}
	if l.Started {
		return ErrLobbyStarted
	}
	for _, m := range l.Members {
		if m != l.Host && !m.ready {
			return ErrNotReady
		}
	}
	l.Started = true
	l.LastActive = time.Now()
	state := l.snapshot()
	for _, m := range l.Members {
		m.trySend(ServerMsg{Type: "launch", Lobby: state})
	}
	return nil
}

// Relay forwards a game payload from s to lobby peers. Empty `to`
// broadcasts to every other member; otherwise only the named player
// receives it. The payload is opaque to the server.
func (h *Hub) Relay(s *Session, to string, data []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := s.lobby
	if l == nil {
		return ErrNotInLobby
	}
	l.LastActive = time.Now()
	msg := ServerMsg{Type: "msg", From: s.UserID, Data: data}
	for _, m := range l.Members {
		if m == s {
			continue
		}
		if to != "" && m.UserID != to {
			continue
		}
		m.trySend(msg)
	}
	return nil
}

// OnlineCount reports how many players are currently in a lobby
// (waiting or in-game) for the given game.
func (h *Hub) OnlineCount(gameID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.gameCount[gameID]
}

// leaveLocked detaches s from its lobby. Host leaving closes the whole
// lobby (v1: no host migration — the lobby dies with its creator).
// Callers hold h.mu.
func (h *Hub) leaveLocked(s *Session) {
	l := s.lobby
	if l == nil {
		return
	}
	s.lobby = nil
	s.ready = false
	if l.Host == s {
		h.closeLobbyLocked(l, "host_left")
		return
	}
	for i, m := range l.Members {
		if m == s {
			l.Members = append(l.Members[:i], l.Members[i+1:]...)
			break
		}
	}
	h.decGameCountLocked(l.GameID)
	l.LastActive = time.Now()
	l.broadcastState()
}

// closeLobbyLocked destroys a lobby and notifies members. Members stay
// connected — a "closed" frame is not a disconnect; the same socket
// can create or join another lobby.
func (h *Hub) closeLobbyLocked(l *Lobby, reason string) {
	delete(h.lobbies, l.Code)
	for _, m := range l.Members {
		m.lobby = nil
		m.ready = false
		h.decGameCountLocked(l.GameID)
		m.trySend(ServerMsg{Type: "closed", Reason: reason})
	}
	l.Members = nil
}

func (h *Hub) decGameCountLocked(gameID string) {
	if h.gameCount[gameID] <= 1 {
		delete(h.gameCount, gameID)
	} else {
		h.gameCount[gameID]--
	}
}

// StartCleanup launches the idle-lobby reaper. Follows the same
// pattern as middleware.StartRateLimitCleanup: a goroutine that ticks
// until stop is closed (main passes middleware.ShutdownCh).
func (h *Hub) StartCleanup(stop <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(cleanupEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				h.reapIdle()
			case <-stop:
				return
			}
		}
	}()
}

func (h *Hub) reapIdle() {
	h.mu.Lock()
	defer h.mu.Unlock()
	cutoff := time.Now().Add(-IdleTTL)
	for _, l := range h.lobbies {
		if l.LastActive.Before(cutoff) {
			h.closeLobbyLocked(l, "expired")
		}
	}
}

// codeAlphabet omits visually ambiguous characters (0/O, 1/I) so codes
// survive being read aloud or scribbled on paper. 32^6 ≈ 1.07e9 codes.
const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

const codeLen = 6

// newCodeLocked generates an unused lobby code. Caller holds h.mu.
func (h *Hub) newCodeLocked() (string, error) {
	for attempt := 0; attempt < 50; attempt++ {
		b := make([]byte, codeLen)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		for i := range b {
			b[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
		}
		code := string(b)
		if _, taken := h.lobbies[code]; !taken {
			return code, nil
		}
	}
	// 50 straight collisions at ≤500 live lobbies over a 1e9 space
	// means the RNG is broken, not that we're unlucky.
	return "", errors.New("could not allocate a lobby code")
}

func normalizeCode(code string) string {
	up := make([]byte, 0, len(code))
	for i := 0; i < len(code); i++ {
		c := code[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		up = append(up, c)
	}
	return string(up)
}
