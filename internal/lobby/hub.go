package lobby

import (
	"crypto/rand"
	"errors"
	"strings"
	"sync"
	"time"
)

// Limits. Deliberately consts (not flags) — tune here if a deployment
// ever needs different numbers.
const (
	MaxPlayers      = 8               // players per lobby
	MaxSpectators   = 16              // spectators per lobby (don't count toward MaxPlayers)
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
	ErrSpectator      = errors.New("spectators cannot send game messages")
)

// Session is one connected /ws client. The HTTP handler owns the
// websocket; the hub only sees this abstraction — outbound frames go
// through the send channel (drained by the handler's writer goroutine),
// and done is closed to force a disconnect (slow consumer, abuse).
type Session struct {
	UserID    string
	Username  string
	AvatarURL string

	hub       *Hub
	lobby     *Lobby // guarded by hub.mu
	ready     bool   // guarded by hub.mu
	spectator bool   // guarded by hub.mu — read-only observer
	send      chan ServerMsg
	done      chan struct{}
	once      sync.Once
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
// player tabbed out). For relayed "msg" frames (game state, superseded
// by the next frame), drop the oldest queued frame rather than kick the
// player mid-match — a backgrounded tab recovers and gets fresh state.
// For control frames (lobby, launch, closed, …) loss would desync
// membership state, so force-close the session on overflow as before.
func (s *Session) trySend(msg ServerMsg) {
	if msg.Type == "msg" {
		select {
		case s.send <- msg:
		case <-s.done:
		default:
			// Buffer full — evict the oldest frame to make room.
			select {
			case <-s.send:
			default:
			}
			select {
			case s.send <- msg:
			case <-s.done:
			default:
				// Still full (writer hasn't drained): drop this frame.
			}
		}
		return
	}
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
	mu          sync.Mutex
	lobbies     map[string]*Lobby // code → lobby
	userConns   map[string]int    // user ID → live /ws connection count
	gameCount   map[string]int    // game ID → players currently in a lobby
	matchQueues map[string][]*Session // game ID → queued sessions (for matchmaking)
}

// Default is the process-wide hub used by the HTTP handlers.
var Default = NewHub()

func NewHub() *Hub {
	return &Hub{
		lobbies:     make(map[string]*Lobby),
		userConns:   make(map[string]int),
		gameCount:   make(map[string]int),
		matchQueues: make(map[string][]*Session),
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
	h.removeFromQueueLocked(s)
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
// maxPlayers is the optional per-lobby cap (clamped [2, MaxPlayers]).
// Values ≤ 0 or > MaxPlayers silently become MaxPlayers.
func (h *Hub) Create(s *Session, gameID string, maxPlayers int) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if maxPlayers < 2 || maxPlayers > MaxPlayers {
		maxPlayers = MaxPlayers
	}
	if len(h.lobbies) >= MaxLobbies {
		return ErrTooManyLobbies
	}
	h.leaveLocked(s)
	code, err := h.newCodeLocked()
	if err != nil {
		return err
	}
	l := &Lobby{
		Code:          code,
		GameID:        gameID,
		Host:          s,
		Members:       []*Session{s},
		MaxPlayers:    maxPlayers,
		FormerMembers: make(map[string]bool),
		LastActive:    time.Now(),
	}
	h.lobbies[code] = l
	h.gameCount[gameID]++
	s.lobby = l
	s.ready = false
	l.broadcastState()
	persistLobby(l)
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
		// Started lobbies block new joins — but former members can rejoin.
		if !l.FormerMembers[s.UserID] {
			return ErrLobbyStarted
		}
		// Rejoin: remove from former members, fall through to add.
		delete(l.FormerMembers, s.UserID)
	}
	// Count non-spectator members (spectators don't take a player slot).
	playerCount := 0
	for _, m := range l.Members {
		if !m.spectator {
			playerCount++
		}
	}
	if playerCount >= l.MaxPlayers {
		return ErrLobbyFull
	}
	if s.lobby == l {
		l.broadcastState() // re-join of same lobby: just resend state
		return nil
	}
	h.leaveLocked(s)
	// If this is a restored lobby (host == nil from RestoreLobbies),
	// the first joiner becomes the host.
	if l.Host == nil {
		l.Host = s
		s.ready = true
	}
	l.Members = append(l.Members, s)
	l.LastActive = time.Now()
	h.gameCount[l.GameID]++
	s.lobby = l
	s.ready = false
	l.broadcastState()
	persistLobby(l)
	return nil
}

// JoinSpectator adds s to a lobby as a read-only observer. Spectators
// bypass the started check and player cap. They receive relayed
// messages but can't send (Relay returns ErrSpectator).
func (h *Hub) JoinSpectator(s *Session, code string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	l, ok := h.lobbies[normalizeCode(code)]
	if !ok {
		return ErrLobbyNotFound
	}
	// Cap spectators to prevent relay amplification DoS.
	spectatorCount := 0
	for _, m := range l.Members {
		if m.spectator {
			spectatorCount++
		}
	}
	if spectatorCount >= MaxSpectators {
		return ErrLobbyFull
	}
	if s.lobby == l {
		l.broadcastState()
		return nil
	}
	h.leaveLocked(s)
	s.spectator = true
	l.Members = append(l.Members, s)
	l.LastActive = time.Now()
	s.lobby = l
	l.broadcastState()
	persistLobby(l)
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

// SetMetadata updates the lobby's metadata (game settings like map,
// difficulty, mode). Host-only. Broadcasts the new state to all members.
func (h *Hub) SetMetadata(s *Session, metadata []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := s.lobby
	if l == nil {
		return ErrNotInLobby
	}
	if l.Host != s {
		return ErrNotHost
	}
	l.Metadata = metadata
	l.LastActive = time.Now()
	l.broadcastState()
	persistLobby(l)
	return nil
}

// SetPublic toggles the lobby's visibility in the public lobby browser.
// Host-only.
func (h *Hub) SetPublic(s *Session, public bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := s.lobby
	if l == nil {
		return ErrNotInLobby
	}
	if l.Host != s {
		return ErrNotHost
	}
	l.Public = public
	l.LastActive = time.Now()
	persistLobby(l)
	return nil
}

// Matchmake adds s to the matchmaking queue for gameID. When the queue
// reaches playerCount, a lobby is auto-created, all queued players are
// joined, and the game is launched immediately. Players receive
// "matchmaking" messages with queue status updates.
func (h *Hub) Matchmake(s *Session, gameID string, playerCount int) {
	if playerCount < 2 || playerCount > MaxPlayers {
		playerCount = 2
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	// Remove from any existing queue or lobby.
	h.removeFromQueueLocked(s)
	h.leaveLocked(s)

	// Add to queue.
	queue := h.matchQueues[gameID]
	queue = append(queue, s)
	h.matchQueues[gameID] = queue
	s.lobby = nil // not in a lobby yet, just queued

	// Notify all queued players of the count.
	for _, q := range queue {
		q.trySend(ServerMsg{
			Type:       "matchmaking",
			QueueSize:  len(queue),
			TargetCount: playerCount,
		})
	}

	// If enough players, create lobby + launch.
	if len(queue) >= playerCount {
		matched := queue[:playerCount]
		h.matchQueues[gameID] = queue[playerCount:]

		// Create lobby with first player as host.
		code, err := h.newCodeLocked()
		if err != nil {
			for _, m := range matched {
				m.trySend(ServerMsg{Type: "error", Error: "matchmaking failed, try again"})
			}
			return
		}
		l := &Lobby{
			Code:          code,
			GameID:        gameID,
			Host:          matched[0],
			Members:       matched,
			MaxPlayers:    playerCount,
			FormerMembers: make(map[string]bool),
			LastActive:    time.Now(),
		}
		h.lobbies[code] = l
		h.gameCount[gameID] += len(matched)
		for i, m := range matched {
			m.lobby = l
			m.spectator = false
			m.ready = i != 0 // host is implicitly ready, others auto-ready
		}

		// Start immediately.
		l.Started = true
		state := l.snapshot()
		persistLobby(l)
		for _, m := range l.Members {
			m.trySend(ServerMsg{Type: "launch", Lobby: state})
		}
	}
}

// CancelMatchmake removes s from the matchmaking queue.
func (h *Hub) CancelMatchmake(s *Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.removeFromQueueLocked(s)
}

// removeFromQueueLocked removes s from all match queues. Callers hold h.mu.
func (h *Hub) removeFromQueueLocked(s *Session) {
	for gameID, queue := range h.matchQueues {
		for i, q := range queue {
			if q == s {
				queue = append(queue[:i], queue[i+1:]...)
				if len(queue) == 0 {
					delete(h.matchQueues, gameID)
				} else {
					h.matchQueues[gameID] = queue
					// Notify remaining players of updated count.
					for _, rq := range queue {
						rq.trySend(ServerMsg{Type: "matchmaking", QueueSize: len(queue)})
					}
				}
				return
			}
		}
	}
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
		if m != l.Host && !m.spectator && !m.ready {
			return ErrNotReady
		}
	}
	l.Started = true
	l.LastActive = time.Now()
	state := l.snapshot()
	persistLobby(l)
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
	if s.spectator {
		return ErrSpectator
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

// PublicLobbyInfo is a safe (no Session pointers) view of a public lobby.
type PublicLobbyInfo struct {
	Code        string `json:"code"`
	PlayerCount int    `json:"player_count"`
	MaxPlayers  int    `json:"max_players"`
	Started     bool   `json:"started"`
	HostName    string `json:"host_name"`
}

// ListPublicLobbies returns public, non-started lobbies for a game.
// Used by the lobby browser API (GET /api/v1/games/:id/lobbies).
func (h *Hub) ListPublicLobbies(gameID string) []PublicLobbyInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []PublicLobbyInfo
	for _, l := range h.lobbies {
		if l.GameID != gameID || !l.Public || l.Started {
			continue
		}
		players := 0
		hostName := ""
		for _, m := range l.Members {
			if !m.spectator {
				players++
			}
			if m == l.Host {
				hostName = m.Username
			}
		}
		out = append(out, PublicLobbyInfo{
			Code:        l.Code,
			PlayerCount: players,
			MaxPlayers:  l.MaxPlayers,
			Started:     l.Started,
			HostName:    hostName,
		})
	}
	return out
}

// OnlineCount reports how many players are currently in a lobby
// (waiting or in-game) for the given game.
func (h *Hub) OnlineCount(gameID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.gameCount[gameID]
}

// CloseGameLobbies tears down every live lobby for a game and tells its
// members why. Called when a developer clears a game's multiplayer flag
// or deletes it, so a lobby can't outlive the eligibility that created
// it (the event-driven answer to that TOCTOU — cheaper and more timely
// than re-checking the DB on every relayed frame). Returns the number
// of lobbies closed.
func (h *Hub) CloseGameLobbies(gameID, reason string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	// Collect first: closeLobbyLocked deletes from h.lobbies, and we
	// don't want to mutate the map mid-range even though Go permits it.
	var doomed []*Lobby
	for _, l := range h.lobbies {
		if l.GameID == gameID {
			doomed = append(doomed, l)
		}
	}
	for _, l := range doomed {
		h.closeLobbyLocked(l, reason)
	}
	return len(doomed)
}

// leaveLocked detaches s from its lobby. If s is the host, the next
// member (by join order) is promoted to host — the lobby continues.
// If s is the last member, the lobby is closed. Callers hold h.mu.
func (h *Hub) leaveLocked(s *Session) {
	l := s.lobby
	if l == nil {
		return
	}
	wasSpectator := s.spectator
	s.lobby = nil
	s.ready = false
	s.spectator = false

	// Remove s from the member list.
	for i, m := range l.Members {
		if m == s {
			l.Members = append(l.Members[:i], l.Members[i+1:]...)
			break
		}
	}

	// Track former members so they can rejoin a started lobby.
	if l.Started && l.FormerMembers != nil {
		l.FormerMembers[s.UserID] = true
	}

	// If s was the host, promote the next non-spectator member (or close if none).
	if l.Host == s {
		var nextHost *Session
		for _, m := range l.Members {
			if !m.spectator {
				nextHost = m
				break
			}
		}
		if nextHost == nil {
			// No non-spectator members left — close the lobby.
			h.decGameCountLocked(l.GameID)
			h.closeLobbyLocked(l, "host_left")
			return
		}
		l.Host = nextHost
		l.Host.ready = true // host is implicitly ready
	}

	// Only decrement game count for non-spectators (spectators were never counted).
	if !wasSpectator {
		h.decGameCountLocked(l.GameID)
	}
	l.LastActive = time.Now()
	l.broadcastState()
	persistLobby(l)
}

// closeLobbyLocked destroys a lobby and notifies members. Members stay
// connected — a "closed" frame is not a disconnect; the same socket
// can create or join another lobby.
func (h *Hub) closeLobbyLocked(l *Lobby, reason string) {
	delete(h.lobbies, l.Code)
	deleteLobby(l.Code)
	for _, m := range l.Members {
		// Only decrement for non-spectators (spectators were never counted).
		if !m.spectator {
			h.decGameCountLocked(l.GameID)
		}
		m.lobby = nil
		m.ready = false
		m.spectator = false
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

// Shutdown notifies all lobby members that the server is restarting,
// then closes all lobbies. Called from main.go on SIGTERM/SIGINT.
// Players see "server_restarting" and know to reconnect.
func (h *Hub) Shutdown() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, l := range h.lobbies {
		for _, m := range l.Members {
			m.trySend(ServerMsg{Type: "closed", Reason: "server_restarting"})
		}
		deleteLobby(l.Code)
	}
	h.lobbies = make(map[string]*Lobby)
	h.gameCount = make(map[string]int)
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
	code = strings.TrimSpace(code)
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
