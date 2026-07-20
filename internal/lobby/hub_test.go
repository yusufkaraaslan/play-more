package lobby

import (
	"encoding/json"
	"testing"
	"time"
)

// drain empties a session's outbound queue and returns the frames.
func drain(s *Session) []ServerMsg {
	var out []ServerMsg
	for {
		select {
		case m := <-s.send:
			out = append(out, m)
		default:
			return out
		}
	}
}

// last returns the most recent frame of the given type, or nil.
func last(frames []ServerMsg, typ string) *ServerMsg {
	for i := len(frames) - 1; i >= 0; i-- {
		if frames[i].Type == typ {
			return &frames[i]
		}
	}
	return nil
}

func register(t *testing.T, h *Hub, user string) *Session {
	t.Helper()
	s, err := h.Register(user, user, "")
	if err != nil {
		t.Fatalf("register %s: %v", user, err)
	}
	return s
}

func TestCreateJoinReadyStartFlow(t *testing.T) {
	h := NewHub()
	host := register(t, h, "alice")
	guest := register(t, h, "bob")

	if err := h.Create(host, "game1", MaxPlayers); err != nil {
		t.Fatalf("create: %v", err)
	}
	state := last(drain(host), "lobby")
	if state == nil || state.Lobby == nil {
		t.Fatal("host got no lobby snapshot after create")
	}
	code := state.Lobby.Code
	if len(code) != codeLen {
		t.Fatalf("bad code %q", code)
	}
	if state.Lobby.HostID != "alice" || len(state.Lobby.Players) != 1 {
		t.Fatalf("bad initial state: %+v", state.Lobby)
	}

	// Join is case-insensitive.
	if err := h.Join(guest, "  "); err != ErrLobbyNotFound {
		t.Fatalf("expected not-found for garbage code, got %v", err)
	}
	if err := h.Join(guest, code); err != nil {
		t.Fatalf("join: %v", err)
	}
	hostView := last(drain(host), "lobby")
	if hostView == nil || len(hostView.Lobby.Players) != 2 {
		t.Fatalf("host did not see joiner: %+v", hostView)
	}

	// Start before guest is ready must fail.
	if err := h.Start(host); err != ErrNotReady {
		t.Fatalf("expected ErrNotReady, got %v", err)
	}
	// Guest cannot start at all.
	if err := h.Start(guest); err != ErrNotHost {
		t.Fatalf("expected ErrNotHost, got %v", err)
	}

	if err := h.Ready(guest, true); err != nil {
		t.Fatalf("ready: %v", err)
	}
	if err := h.Start(host); err != nil {
		t.Fatalf("start: %v", err)
	}
	launch := last(drain(guest), "launch")
	if launch == nil || !launch.Lobby.Started {
		t.Fatalf("guest got no launch frame")
	}
	if last(drain(host), "launch") == nil {
		t.Fatal("host got no launch frame")
	}

	// Started lobbies reject new joiners.
	late := register(t, h, "carol")
	if err := h.Join(late, code); err != ErrLobbyStarted {
		t.Fatalf("expected ErrLobbyStarted, got %v", err)
	}
}

func TestHostLeaveMigratesHost(t *testing.T) {
	h := NewHub()
	host := register(t, h, "alice")
	guest := register(t, h, "bob")

	if err := h.Create(host, "game1", MaxPlayers); err != nil {
		t.Fatal(err)
	}
	code := last(drain(host), "lobby").Lobby.Code
	if err := h.Join(guest, code); err != nil {
		t.Fatal(err)
	}
	drain(guest)

	h.Unregister(host) // host disconnects

	// Guest should get a lobby state update (not closed) showing they're now host.
	state := last(drain(guest), "lobby")
	if state == nil {
		t.Fatal("guest did not get lobby state update after host migration")
	}
	if state.Lobby.HostID != "bob" {
		t.Fatalf("host not migrated: host_id=%s, want bob", state.Lobby.HostID)
	}
	if len(state.Lobby.Players) != 1 {
		t.Fatalf("players=%d, want 1", len(state.Lobby.Players))
	}
	// The remaining player should be marked as host.
	if !state.Lobby.Players[0].Host {
		t.Fatal("remaining player not marked as host")
	}
	// Guest should still be in the lobby.
	if guest.lobby == nil {
		t.Fatal("guest detached from lobby after host migration")
	}
	if guest.lobby.Host != guest {
		t.Fatal("guest is not the new host in the lobby struct")
	}
	// Online count should reflect 1 remaining player.
	if h.OnlineCount("game1") != 1 {
		t.Fatalf("online count=%d, want 1", h.OnlineCount("game1"))
	}
	// Lobby should still be joinable (if not started).
	if err := h.Join(guest, code); err != nil {
		t.Fatalf("re-join after migration failed: %v", err)
	}
}

func TestHostLeaveLastMemberClosesLobby(t *testing.T) {
	h := NewHub()
	host := register(t, h, "alice")

	if err := h.Create(host, "game1", MaxPlayers); err != nil {
		t.Fatal(err)
	}
	code := last(drain(host), "lobby").Lobby.Code

	h.Unregister(host) // host disconnects — was the only member

	if h.OnlineCount("game1") != 0 {
		t.Fatalf("online count=%d, want 0", h.OnlineCount("game1"))
	}
	if err := h.Join(host, code); err != ErrLobbyNotFound {
		t.Fatalf("dead lobby still joinable: %v", err)
	}
}

func TestHostMigrationDuringStartedGame(t *testing.T) {
	h := NewHub()
	host := register(t, h, "alice")
	guest := register(t, h, "bob")

	h.Create(host, "game1", MaxPlayers)
	code := last(drain(host), "lobby").Lobby.Code
	h.Join(guest, code)
	drain(host)
	guest.ready = true
	h.Ready(guest, true)
	drain(host)
	drain(guest)

	// Start the game, then host leaves.
	if err := h.Start(host); err != nil {
		t.Fatal(err)
	}
	drain(guest) // consume launch frame

	h.Unregister(host) // host disconnects mid-game

	// Guest should get a lobby state update showing they're the new host.
	state := last(drain(guest), "lobby")
	if state == nil {
		t.Fatal("guest did not get state update after host migration mid-game")
	}
	if state.Lobby.HostID != "bob" {
		t.Fatalf("host not migrated: host_id=%s, want bob", state.Lobby.HostID)
	}
	if !state.Lobby.Started {
		t.Fatal("lobby lost started flag after host migration")
	}
	if guest.lobby == nil || guest.lobby.Host != guest {
		t.Fatal("guest not promoted to host in lobby struct")
	}
}

func TestMemberLeaveBroadcasts(t *testing.T) {
	h := NewHub()
	host := register(t, h, "alice")
	guest := register(t, h, "bob")

	h.Create(host, "game1", MaxPlayers)
	code := last(drain(host), "lobby").Lobby.Code
	h.Join(guest, code)
	drain(host)

	h.Leave(guest)
	state := last(drain(host), "lobby")
	if state == nil || len(state.Lobby.Players) != 1 {
		t.Fatalf("host did not see member leave: %+v", state)
	}
	if h.OnlineCount("game1") != 1 {
		t.Fatalf("online count = %d, want 1", h.OnlineCount("game1"))
	}
}

func TestLobbyFull(t *testing.T) {
	h := NewHub()
	host := register(t, h, "host")
	h.Create(host, "game1", MaxPlayers)
	code := last(drain(host), "lobby").Lobby.Code
	for i := 1; i < MaxPlayers; i++ {
		s := register(t, h, string(rune('a'+i)))
		if err := h.Join(s, code); err != nil {
			t.Fatalf("join %d: %v", i, err)
		}
	}
	extra := register(t, h, "extra")
	if err := h.Join(extra, code); err != ErrLobbyFull {
		t.Fatalf("expected ErrLobbyFull, got %v", err)
	}
}

func TestRelayBroadcastAndTargeted(t *testing.T) {
	h := NewHub()
	a := register(t, h, "a")
	b := register(t, h, "b")
	c := register(t, h, "c")

	h.Create(a, "game1", MaxPlayers)
	code := last(drain(a), "lobby").Lobby.Code
	h.Join(b, code)
	h.Join(c, code)
	drain(a)
	drain(b)
	drain(c)

	payload := json.RawMessage(`{"x":1}`)

	// Broadcast: everyone but the sender.
	if err := h.Relay(a, "", payload); err != nil {
		t.Fatal(err)
	}
	if last(drain(a), "msg") != nil {
		t.Fatal("sender received its own broadcast")
	}
	bm := last(drain(b), "msg")
	if bm == nil || bm.From != "a" || string(bm.Data) != `{"x":1}` {
		t.Fatalf("b got bad relay: %+v", bm)
	}
	if last(drain(c), "msg") == nil {
		t.Fatal("c missed broadcast")
	}

	// Targeted: only the named player.
	if err := h.Relay(b, "c", payload); err != nil {
		t.Fatal(err)
	}
	if last(drain(a), "msg") != nil {
		t.Fatal("a received a message targeted at c")
	}
	if last(drain(c), "msg") == nil {
		t.Fatal("c missed targeted message")
	}

	// Relay outside a lobby fails.
	solo := register(t, h, "solo")
	if err := h.Relay(solo, "", payload); err != ErrNotInLobby {
		t.Fatalf("expected ErrNotInLobby, got %v", err)
	}
}

func TestAutoLeaveOnCreateAndJoin(t *testing.T) {
	h := NewHub()
	a := register(t, h, "a")
	b := register(t, h, "b")

	h.Create(a, "game1", MaxPlayers)
	firstCode := last(drain(a), "lobby").Lobby.Code

	// Creating a second lobby closes the first (a hosted it).
	if err := h.Create(a, "game2", MaxPlayers); err != nil {
		t.Fatal(err)
	}
	if _, alive := h.lobbies[firstCode]; alive {
		t.Fatal("first lobby survived host re-create")
	}
	if h.OnlineCount("game1") != 0 || h.OnlineCount("game2") != 1 {
		t.Fatalf("counts wrong: game1=%d game2=%d", h.OnlineCount("game1"), h.OnlineCount("game2"))
	}

	// b joins a's lobby, then creates its own — must leave a's lobby.
	secondCode := last(drain(a), "lobby").Lobby.Code
	h.Join(b, secondCode)
	if err := h.Create(b, "game3", MaxPlayers); err != nil {
		t.Fatal(err)
	}
	drain(a)
	if h.OnlineCount("game2") != 1 {
		t.Fatalf("b still counted in game2: %d", h.OnlineCount("game2"))
	}
}

func TestConnCapPerUser(t *testing.T) {
	h := NewHub()
	for i := 0; i < MaxConnsPerUser; i++ {
		register(t, h, "alice")
	}
	if _, err := h.Register("alice", "alice", ""); err != ErrTooManyConns {
		t.Fatalf("expected ErrTooManyConns, got %v", err)
	}
	// Other users unaffected.
	register(t, h, "bob")
}

func TestSlowConsumerDisconnected(t *testing.T) {
	// Control frames (lobby snapshots) overflow → force-close.
	h := NewHub()
	a := register(t, h, "a")
	b := register(t, h, "b")
	h.Create(a, "game1", MaxPlayers)
	code := last(drain(a), "lobby").Lobby.Code
	h.Join(b, code)
	drain(b) // b receives its join snapshot, now stops draining

	// Flood with lobby snapshots (control frames that can't be dropped).
	for i := 0; i < SendBuffer+2; i++ {
		h.SetMetadata(a, json.RawMessage(`{"i":1}`))
		drain(a) // prevent a's own buffer from overflowing
	}

	select {
	case <-b.Done():
		// force-closed as expected
	case <-time.After(time.Second):
		t.Fatal("slow consumer was not force-closed on control-frame overflow")
	}
}

func TestSlowConsumerDropsStaleRelayFrames(t *testing.T) {
	// Relay (msg) frames overflow → drop-oldest, NOT force-close.
	h := NewHub()
	a := register(t, h, "a")
	b := register(t, h, "b")
	h.Create(a, "game1", MaxPlayers)
	code := last(drain(a), "lobby").Lobby.Code
	h.Join(b, code)
	drain(a)
	drain(b) // b receives its join snapshot, then stops draining

	// Flood with more than SendBuffer relay frames.
	payload := json.RawMessage(`{"tick":1}`)
	for i := 0; i < SendBuffer*4; i++ {
		h.Relay(a, "", payload)
	}

	// b must NOT be force-closed — stale frames were dropped instead.
	select {
	case <-b.Done():
		t.Fatal("session was force-closed on relay overflow; should drop frames instead")
	case <-time.After(200 * time.Millisecond):
		// still connected — good
	}

	// Drain: b should receive at most SendBuffer frames (the most recent),
	// not all of them. Every received frame must be the relay payload.
	frames := drain(b)
	relayCount := 0
	for _, f := range frames {
		if f.Type == "msg" {
			relayCount++
		}
	}
	if relayCount > SendBuffer {
		t.Fatalf("received %d relay frames, want ≤ %d (buffer size)", relayCount, SendBuffer)
	}
}

func TestReapIdle(t *testing.T) {
	h := NewHub()
	a := register(t, h, "a")
	h.Create(a, "game1", MaxPlayers)
	code := last(drain(a), "lobby").Lobby.Code

	h.mu.Lock()
	h.lobbies[code].LastActive = time.Now().Add(-IdleTTL - time.Minute)
	h.mu.Unlock()

	h.reapIdle()

	closed := last(drain(a), "closed")
	if closed == nil || closed.Reason != "expired" {
		t.Fatalf("no expired notification: %+v", closed)
	}
	if h.OnlineCount("game1") != 0 {
		t.Fatal("online count not released on reap")
	}
}

func TestCloseGameLobbies(t *testing.T) {
	h := NewHub()
	// Two lobbies on game1, one on game2.
	a := register(t, h, "a")
	b := register(t, h, "b")
	c := register(t, h, "c")
	h.Create(a, "game1", MaxPlayers)
	h.Create(b, "game1", MaxPlayers)
	h.Create(c, "game2", MaxPlayers)
	drain(a)
	drain(b)
	drain(c)

	// A joiner in one of the game1 lobbies must also be evicted.
	code := ""
	h.mu.Lock()
	for cd, l := range h.lobbies {
		if l.GameID == "game1" && l.Host == a {
			code = cd
		}
	}
	h.mu.Unlock()
	guest := register(t, h, "guest")
	h.Join(guest, code)
	drain(guest)

	n := h.CloseGameLobbies("game1", "multiplayer_disabled")
	if n != 2 {
		t.Fatalf("closed %d lobbies, want 2", n)
	}
	if cm := last(drain(a), "closed"); cm == nil || cm.Reason != "multiplayer_disabled" {
		t.Fatalf("host a not notified: %+v", cm)
	}
	if cm := last(drain(guest), "closed"); cm == nil || cm.Reason != "multiplayer_disabled" {
		t.Fatalf("guest not notified: %+v", cm)
	}
	if h.OnlineCount("game1") != 0 {
		t.Fatalf("game1 count = %d, want 0", h.OnlineCount("game1"))
	}
	// game2 lobby is untouched.
	if h.OnlineCount("game2") != 1 {
		t.Fatalf("game2 count = %d, want 1 (should be untouched)", h.OnlineCount("game2"))
	}
	if a.lobby != nil || guest.lobby != nil {
		t.Fatal("members still attached to a closed lobby")
	}
}

func TestUnregisterReleasesConnSlot(t *testing.T) {
	h := NewHub()
	sessions := make([]*Session, 0, MaxConnsPerUser)
	for i := 0; i < MaxConnsPerUser; i++ {
		sessions = append(sessions, register(t, h, "alice"))
	}
	h.Unregister(sessions[0])
	// Slot freed — a new connection fits again.
	register(t, h, "alice")
}

func TestSetMetadata(t *testing.T) {
	h := NewHub()
	host := register(t, h, "alice")
	guest := register(t, h, "bob")

	h.Create(host, "game1", MaxPlayers)
	code := last(drain(host), "lobby").Lobby.Code
	h.Join(guest, code)
	drain(host) // host gets join broadcast
	drain(guest) // guest gets lobby state

	// Host sets metadata.
	meta := json.RawMessage(`{"map":"de_dust2","difficulty":"hard"}`)
	if err := h.SetMetadata(host, meta); err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}

	// Both should get a lobby state update with metadata.
	hostState := last(drain(host), "lobby")
	guestState := last(drain(guest), "lobby")
	if hostState == nil || hostState.Lobby == nil {
		t.Fatal("host got no state update")
	}
	if string(hostState.Lobby.Metadata) != `{"map":"de_dust2","difficulty":"hard"}` {
		t.Fatalf("host metadata = %s, want map/difficulty", string(hostState.Lobby.Metadata))
	}
	if guestState == nil || guestState.Lobby == nil {
		t.Fatal("guest got no state update")
	}
	if string(guestState.Lobby.Metadata) != `{"map":"de_dust2","difficulty":"hard"}` {
		t.Fatalf("guest metadata = %s, want map/difficulty", string(guestState.Lobby.Metadata))
	}

	// Non-host can't set metadata.
	if err := h.SetMetadata(guest, json.RawMessage(`{"hack":true}`)); err != ErrNotHost {
		t.Fatalf("non-host SetMetadata: err=%v, want ErrNotHost", err)
	}
}

func TestRejoinAfterDisconnect(t *testing.T) {
	h := NewHub()
	host := register(t, h, "alice")
	guest := register(t, h, "bob")

	h.Create(host, "game1", MaxPlayers)
	code := last(drain(host), "lobby").Lobby.Code
	h.Join(guest, code)
	drain(host)
	guest.ready = true
	h.Ready(guest, true)
	drain(host)
	drain(guest)

	// Start the game.
	if err := h.Start(host); err != nil {
		t.Fatal(err)
	}
	drain(guest) // consume launch

	// Guest disconnects.
	h.Unregister(guest)
	drain(host) // host gets state update (guest left, host stays)

	// Guest can't join with a fresh session — lobby is started.
	guest2 := register(t, h, "carol")
	if err := h.Join(guest2, code); err != ErrLobbyStarted {
		t.Fatalf("new player joined started lobby: err=%v, want ErrLobbyStarted", err)
	}

	// But the original guest can rejoin (former member).
	guestRejoined := register(t, h, "bob")
	if err := h.Join(guestRejoined, code); err != nil {
		t.Fatalf("rejoin failed: %v", err)
	}

	// Host should see the rejoined player.
	state := last(drain(host), "lobby")
	if state == nil || state.Lobby == nil {
		t.Fatal("host got no state update on rejoin")
	}
	found := false
	for _, p := range state.Lobby.Players {
		if p.ID == "bob" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("rejoined player not in lobby state")
	}

	// Rejoined player should get the lobby state.
	rejoinedState := last(drain(guestRejoined), "lobby")
	if rejoinedState == nil || rejoinedState.Lobby == nil {
		t.Fatal("rejoined player got no lobby state")
	}
	if !rejoinedState.Lobby.Started {
		t.Fatal("rejoined player doesn't see started=true")
	}
}

func TestSpectatorDepartureDoesNotDecrementGameCount(t *testing.T) {
	h := NewHub()
	host := register(t, h, "alice")
	spec := register(t, h, "bobby")

	h.Create(host, "game1", MaxPlayers)
	code := last(drain(host), "lobby").Lobby.Code

	// Join as spectator — must not bump OnlineCount.
	if err := h.JoinSpectator(spec, code); err != nil {
		t.Fatalf("spectator join: %v", err)
	}
	drain(host) // host sees the spectator
	drain(spec)
	if got := h.OnlineCount("game1"); got != 1 {
		t.Fatalf("OnlineCount after spectator join = %d, want 1 (spectators excluded)", got)
	}

	// Spectator leaves — OnlineCount must stay 1, not drop to 0.
	h.Leave(spec)
	drain(host)
	if got := h.OnlineCount("game1"); got != 1 {
		t.Fatalf("OnlineCount after spectator leave = %d, want 1 (spectator never counted)", got)
	}

	// Now the real player leaves — OnlineCount drops to 0.
	h.Leave(host)
	if got := h.OnlineCount("game1"); got != 0 {
		t.Fatalf("OnlineCount after host leave = %d, want 0", got)
	}
}
