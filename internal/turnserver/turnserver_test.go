package turnserver_test

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/pion/turn/v4"

	"github.com/yusufkaraaslan/play-more/internal/turnserver"
)

const testSecret = "test-shared-secret-not-a-real-one"

func TestMint_RoundTrips(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	creds := turnserver.Mint(testSecret, "user-abc", 10*time.Minute, now)

	if !strings.HasPrefix(creds.Username, "1700000600:") {
		t.Fatalf("username should embed the expiry, got %q", creds.Username)
	}
	userID, ok := turnserver.CheckUsername(creds.Username, now)
	if !ok {
		t.Fatal("freshly minted credential should validate")
	}
	if userID != "user-abc" {
		t.Fatalf("user id = %q, want user-abc", userID)
	}
	if !turnserver.VerifyPassword(testSecret, creds.Username, creds.Password) {
		t.Fatal("minted password should verify against the same secret")
	}
}

func TestCheckUsername_RejectsExpired(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	creds := turnserver.Mint(testSecret, "user-abc", 5*time.Minute, now)

	// One second past expiry.
	if _, ok := turnserver.CheckUsername(creds.Username, now.Add(5*time.Minute+time.Second)); ok {
		t.Fatal("expired credential must not validate")
	}
	// Exactly at expiry is still inside the window.
	if _, ok := turnserver.CheckUsername(creds.Username, now.Add(5*time.Minute)); !ok {
		t.Fatal("credential should still be valid at the expiry instant")
	}
}

func TestCheckUsername_RejectsMalformed(t *testing.T) {
	now := time.Now()
	for _, bad := range []string{
		"",
		"nocolon",
		":user",           // empty expiry
		"notanumber:user", // unparseable expiry
		"1700000000",      // no separator
	} {
		if _, ok := turnserver.CheckUsername(bad, now); ok {
			t.Errorf("username %q should be rejected", bad)
		}
	}
}

// A user ID containing a colon would otherwise let the caller inject
// their own expiry field into the username, since ':' is the
// separator. PlayMore IDs are UUIDs so this never fires today — the
// test exists so it can't start firing silently later.
func TestMint_StripsColonsFromUserID(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	creds := turnserver.Mint(testSecret, "9999999999:evil", 10*time.Minute, now)

	userID, ok := turnserver.CheckUsername(creds.Username, now)
	if !ok {
		t.Fatal("credential should validate")
	}
	if strings.Contains(userID, ":") {
		t.Fatalf("user id should have colons stripped, got %q", userID)
	}
	// The expiry must still be the one we chose, not the injected one.
	if _, stillOK := turnserver.CheckUsername(creds.Username, now.Add(11*time.Minute)); stillOK {
		t.Fatal("injected expiry must not extend the credential lifetime")
	}
}

func TestVerifyPassword_RejectsWrongSecret(t *testing.T) {
	now := time.Now()
	creds := turnserver.Mint(testSecret, "user-abc", time.Minute, now)

	if turnserver.VerifyPassword("a-different-secret", creds.Username, creds.Password) {
		t.Fatal("credential must not verify under a different shared secret")
	}
	if turnserver.VerifyPassword(testSecret, creds.Username, "wrong-password") {
		t.Fatal("wrong password must not verify")
	}
}

func TestConfig_Validate(t *testing.T) {
	base := func() turnserver.Config {
		return turnserver.Config{
			Listen:   "0.0.0.0:3478",
			PublicIP: "203.0.113.10",
			Secret:   testSecret,
			MinPort:  49152,
			MaxPort:  49251,
		}
	}

	t.Run("valid config passes with no warning", func(t *testing.T) {
		cfg := base()
		warn, err := cfg.Validate()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if warn != "" {
			t.Fatalf("unexpected warning: %s", warn)
		}
	})

	t.Run("missing secret", func(t *testing.T) {
		cfg := base()
		cfg.Secret = ""
		if _, err := cfg.Validate(); err == nil {
			t.Fatal("expected error for missing secret")
		}
	})

	t.Run("missing public ip", func(t *testing.T) {
		cfg := base()
		cfg.PublicIP = ""
		if _, err := cfg.Validate(); err == nil {
			t.Fatal("expected error for missing public IP")
		}
	})

	t.Run("unparseable public ip", func(t *testing.T) {
		cfg := base()
		cfg.PublicIP = "not-an-ip"
		if _, err := cfg.Validate(); err == nil {
			t.Fatal("expected error for malformed public IP")
		}
	})

	t.Run("ipv6 public ip is rejected", func(t *testing.T) {
		cfg := base()
		cfg.PublicIP = "2001:db8::1"
		if _, err := cfg.Validate(); err == nil {
			t.Fatal("expected error for IPv6 public IP")
		}
	})

	t.Run("inverted port range", func(t *testing.T) {
		cfg := base()
		cfg.MinPort, cfg.MaxPort = 49251, 49152
		if _, err := cfg.Validate(); err == nil {
			t.Fatal("expected error for inverted port range")
		}
	})

	// A private relay address is legal but gathers candidates that
	// never connect from outside the LAN — the failure mode this
	// warning exists to make debuggable.
	t.Run("private public-ip warns but does not fail", func(t *testing.T) {
		cfg := base()
		cfg.PublicIP = "192.168.1.50"
		warn, err := cfg.Validate()
		if err != nil {
			t.Fatalf("private IP should not be fatal: %v", err)
		}
		if warn == "" {
			t.Fatal("private IP should produce a warning")
		}
	})
}

func TestICEServersFor_ShapeAndFreshness(t *testing.T) {
	srv := startTestServer(t)

	entries := srv.ICEServersFor("user-abc")
	if len(entries) != 2 {
		t.Fatalf("expected a stun: and a turn: entry, got %d", len(entries))
	}

	stunURL, _ := entries[0]["urls"].(string)
	if !strings.HasPrefix(stunURL, "stun:127.0.0.1:") {
		t.Errorf("first entry should be stun on the bound port, got %q", stunURL)
	}
	// Port 0 was requested; the advertised port must be the real one.
	if strings.HasSuffix(stunURL, ":0") {
		t.Errorf("advertised port must be the bound port, not the requested :0 (%q)", stunURL)
	}

	turnEntry := entries[1]
	turnURL, _ := turnEntry["urls"].(string)
	if !strings.HasPrefix(turnURL, "turn:127.0.0.1:") || !strings.HasSuffix(turnURL, "?transport=udp") {
		t.Errorf("unexpected turn URL %q", turnURL)
	}
	username, _ := turnEntry["username"].(string)
	password, _ := turnEntry["credential"].(string)
	if username == "" || password == "" {
		t.Fatal("turn entry must carry credentials")
	}
	if !turnserver.VerifyPassword(testSecret, username, password) {
		t.Error("issued credential should verify against the server secret")
	}

	// Different users must not receive the same credential.
	other := srv.ICEServersFor("user-xyz")
	otherUser, _ := other[1]["username"].(string)
	if otherUser == username {
		t.Error("credentials must be scoped per user")
	}
}

func TestICEServersFor_NilServer(t *testing.T) {
	var srv *turnserver.Server
	if got := srv.ICEServersFor("user-abc"); got != nil {
		t.Fatalf("nil server should return nil, got %v", got)
	}
}

// The real test: drive the running relay with pion's TURN client and
// confirm a valid credential actually gets an allocation while a
// forged one does not. Credential-scheme bugs pass unit tests and
// fail here.
func TestAllocate_EndToEnd(t *testing.T) {
	srv := startTestServer(t)
	addr := turnAddr(t, srv)

	t.Run("valid credential gets a relay allocation", func(t *testing.T) {
		creds := turnserver.Mint(testSecret, "user-abc", time.Minute, time.Now())
		client, closeClient := newClient(t, addr, creds.Username, creds.Password)
		defer closeClient()

		relayConn, err := client.Allocate()
		if err != nil {
			t.Fatalf("Allocate with a valid credential failed: %v", err)
		}
		defer relayConn.Close() //nolint:errcheck

		relayed := relayConn.LocalAddr().String()
		_, portStr, err := net.SplitHostPort(relayed)
		if err != nil {
			t.Fatalf("relayed address %q is not host:port: %v", relayed, err)
		}
		if portStr == "0" {
			t.Errorf("relay allocation returned port 0 (%q)", relayed)
		}
	})

	t.Run("forged credential is refused", func(t *testing.T) {
		creds := turnserver.Mint("the-wrong-secret", "user-abc", time.Minute, time.Now())
		client, closeClient := newClient(t, addr, creds.Username, creds.Password)
		defer closeClient()

		if _, err := client.Allocate(); err == nil {
			t.Fatal("Allocate should fail when the credential was signed with another secret")
		}
	})

	t.Run("expired credential is refused", func(t *testing.T) {
		creds := turnserver.Mint(testSecret, "user-abc", time.Minute, time.Now().Add(-2*time.Minute))
		client, closeClient := newClient(t, addr, creds.Username, creds.Password)
		defer closeClient()

		if _, err := client.Allocate(); err == nil {
			t.Fatal("Allocate should fail for an expired credential")
		}
	})
}

// The relay port range is finite, so one account must not be able to
// allocate all of it. The quota is keyed on the account, not the TURN
// username — usernames embed an expiry and a client gets a fresh one
// from every /rtc-config call, so username-keyed counting would let a
// caller reset its own quota just by re-minting.
func TestAllocationQuota_IsPerAccountNotPerUsername(t *testing.T) {
	srv := startTestServer(t)
	addr := turnAddr(t, srv)

	// Two credentials for the same account, minted a second apart so
	// their usernames (and therefore embedded expiries) differ.
	base := time.Now()
	first := turnserver.Mint(testSecret, "same-user", time.Minute, base)
	second := turnserver.Mint(testSecret, "same-user", time.Minute, base.Add(time.Second))
	if first.Username == second.Username {
		t.Fatal("test needs two distinct usernames for the same account")
	}

	var held []interface{ Close() error }
	defer func() {
		for _, c := range held {
			_ = c.Close()
		}
	}()

	// Exhaust the quota using the first credential.
	limit := turnserver.DefaultMaxAllocationsPerUser
	for i := 0; i < limit; i++ {
		client, closeClient := newClient(t, addr, first.Username, first.Password)
		defer closeClient()
		relay, err := client.Allocate()
		if err != nil {
			t.Fatalf("allocation %d/%d should have been permitted: %v", i+1, limit, err)
		}
		held = append(held, relay)
	}

	// A fresh username for the SAME account must not get a new budget.
	client, closeClient := newClient(t, addr, second.Username, second.Password)
	defer closeClient()
	if _, err := client.Allocate(); err == nil {
		t.Fatal("re-minting a credential must not reset the per-account allocation quota")
	}
}

// A different account must still be served once another has hit its
// quota — the cap bounds one abuser, it does not close the relay.
func TestAllocationQuota_OtherAccountsUnaffected(t *testing.T) {
	srv := startTestServer(t)
	addr := turnAddr(t, srv)

	hog := turnserver.Mint(testSecret, "hog", time.Minute, time.Now())
	var held []interface{ Close() error }
	defer func() {
		for _, c := range held {
			_ = c.Close()
		}
	}()

	for i := 0; i < turnserver.DefaultMaxAllocationsPerUser; i++ {
		client, closeClient := newClient(t, addr, hog.Username, hog.Password)
		defer closeClient()
		relay, err := client.Allocate()
		if err != nil {
			t.Fatalf("hog allocation %d failed: %v", i+1, err)
		}
		held = append(held, relay)
	}

	victim := turnserver.Mint(testSecret, "victim", time.Minute, time.Now())
	client, closeClient := newClient(t, addr, victim.Username, victim.Password)
	defer closeClient()
	relay, err := client.Allocate()
	if err != nil {
		t.Fatalf("a second account should still be served: %v", err)
	}
	_ = relay.Close()
}

// startTestServer binds the relay on loopback with an OS-assigned
// control port, so tests never collide on 3478 or need privileges.
func startTestServer(t *testing.T) *turnserver.Server {
	t.Helper()
	srv, err := turnserver.Start(turnserver.Config{
		Listen:   "127.0.0.1:0",
		PublicIP: "127.0.0.1",
		Realm:    "playmore-test",
		Secret:   testSecret,
		MinPort:  49300,
		MaxPort:  49350,
	})
	if err != nil {
		t.Fatalf("start test TURN server: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

// turnAddr recovers the relay's real listen address from the ICE
// entries it advertises — the only public view of the bound port.
func turnAddr(t *testing.T, srv *turnserver.Server) string {
	t.Helper()
	entries := srv.ICEServersFor("probe")
	stunURL, _ := entries[0]["urls"].(string)
	return strings.TrimPrefix(stunURL, "stun:")
}

func newClient(t *testing.T, addr, username, password string) (*turn.Client, func()) {
	t.Helper()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("client socket: %v", err)
	}
	client, err := turn.NewClient(&turn.ClientConfig{
		STUNServerAddr: addr,
		TURNServerAddr: addr,
		Conn:           conn,
		Username:       username,
		Password:       password,
		Realm:          "playmore-test",
	})
	if err != nil {
		_ = conn.Close()
		t.Fatalf("new turn client: %v", err)
	}
	if err := client.Listen(); err != nil {
		client.Close()
		_ = conn.Close()
		t.Fatalf("client listen: %v", err)
	}
	return client, func() {
		client.Close()
		_ = conn.Close()
	}
}
