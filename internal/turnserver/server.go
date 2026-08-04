// Package turnserver runs an embedded TURN relay inside the PlayMore
// binary, so operators get WebRTC NAT traversal without deploying and
// maintaining a separate coturn instance.
//
// # Why embedded rather than coturn
//
// PlayMore ships as one binary with no runtime dependencies, and
// `./playmore setup` has never touched anything outside .env. Having
// the wizard apt-install a system daemon, write a unit file, and
// manage its lifecycle would be by far the most invasive thing in the
// codebase — and the operator, not us, would own the cleanup. pion is
// pure Go and links straight in, so the whole feature is a flag.
//
// Operators who already run coturn are still first class: the
// credential scheme here is coturn's `use-auth-secret`, so pointing
// --turn-servers at an external coturn with the same shared secret
// works unchanged. See docs/SETUP.md.
//
// # What this does not solve
//
// TURN needs inbound UDP — the listen port plus one relay port per
// concurrent allocation. HTTP-only ingress (Cloudflare Tunnel, most
// PaaS) cannot carry that traffic no matter how this is configured.
// Such deployments need a direct public IP alongside the tunnel, or
// an external TURN service. That's why this is opt-in.
package turnserver

import (
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/pion/logging"
	"github.com/pion/turn/v4"
)

// Default relay port range. Each concurrent relayed peer connection
// consumes exactly one port, so 100 ports means 100 simultaneous
// relayed peers — comfortably above what a 500-lobby cap produces
// when only NAT-restricted players relay. Operators expecting more
// should widen the range and open the matching firewall rules;
// exhaustion surfaces as failed allocations, which pion logs.
const (
	DefaultMinPort = 49152
	DefaultMaxPort = 49251
	DefaultRealm   = "playmore"
)

// DefaultMaxAllocationsPerUser caps how many relay allocations one
// account may hold at once.
//
// Without a cap, a single account can allocate the entire port range
// and deny the relay to everyone else — the range is finite (100 ports
// by default) and allocations survive until they time out. 8 covers the
// worst legitimate case, a full 8-player mesh where every peer needs
// relay, while holding one account to a small share of the range.
const DefaultMaxAllocationsPerUser = 8

// Config is the embedded TURN server's configuration. Zero values are
// filled with the Default* constants where a sane default exists;
// PublicIP and Secret have none and must be set.
type Config struct {
	// Listen is the UDP address the TURN control port binds to,
	// e.g. "0.0.0.0:3478".
	Listen string

	// PublicIP is the address handed to clients in relay candidates.
	// It cannot be auto-detected: the server usually sits behind NAT
	// and only sees its private address, and guessing wrong produces
	// candidates that silently never connect.
	PublicIP string

	// Realm appears in the auth challenge. Cosmetic, but it is mixed
	// into the credential key, so it must match on both sides.
	Realm string

	// Secret is the HMAC shared secret backing ephemeral credentials.
	Secret string

	MinPort uint16
	MaxPort uint16

	// CredentialTTL is how long minted credentials stay valid.
	CredentialTTL time.Duration

	// MaxAllocationsPerUser caps concurrent relay allocations per
	// account. Defaults to DefaultMaxAllocationsPerUser.
	MaxAllocationsPerUser int
}

func (c *Config) withDefaults() {
	if c.Realm == "" {
		c.Realm = DefaultRealm
	}
	if c.MinPort == 0 {
		c.MinPort = DefaultMinPort
	}
	if c.MaxPort == 0 {
		c.MaxPort = DefaultMaxPort
	}
	if c.CredentialTTL <= 0 {
		c.CredentialTTL = DefaultCredentialTTL
	}
	if c.Listen == "" {
		c.Listen = "0.0.0.0:3478"
	}
	if c.MaxAllocationsPerUser <= 0 {
		c.MaxAllocationsPerUser = DefaultMaxAllocationsPerUser
	}
}

// allocTracker counts live relay allocations per account so the quota
// handler can reject the account that tries to monopolise the range.
//
// Keyed on the user ID rather than the TURN username: the username
// embeds an expiry, so a client gets a fresh one from every
// /rtc-config call and could otherwise reset its own quota just by
// re-minting.
type allocTracker struct {
	mu    sync.Mutex
	perID map[string]int
}

func newAllocTracker() *allocTracker {
	return &allocTracker{perID: make(map[string]int)}
}

func (a *allocTracker) allowed(username string, limit int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.perID[userIDFrom(username)] < limit
}

func (a *allocTracker) add(username string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.perID[userIDFrom(username)]++
}

func (a *allocTracker) remove(username string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := userIDFrom(username)
	if n := a.perID[id]; n <= 1 {
		// Delete rather than leave a zero behind, so the map doesn't
		// grow without bound across a long-running process.
		delete(a.perID, id)
	} else {
		a.perID[id] = n - 1
	}
}

// userIDFrom extracts the account portion of a TURN username without
// validating the expiry. Deliberately separate from CheckUsername:
// an allocation is torn down after its credential has expired, and
// the counter must still decrement then or the quota leaks.
func userIDFrom(username string) string {
	if sep := strings.Index(username, ":"); sep >= 0 {
		return username[sep+1:]
	}
	return username
}

// Validate checks the configuration and returns a diagnostic warning
// alongside any fatal error. The warning is for settings that are
// legal but almost certainly wrong in production — a private PublicIP
// is the common one, and it fails in a way (candidates gathered, but
// nothing ever connects) that is miserable to debug from the logs.
func (c *Config) Validate() (warning string, err error) {
	if c.Secret == "" {
		return "", fmt.Errorf("turn: shared secret is required")
	}
	if c.PublicIP == "" {
		return "", fmt.Errorf("turn: --turn-public-ip is required (it cannot be auto-detected behind NAT)")
	}
	ip := net.ParseIP(c.PublicIP)
	if ip == nil {
		return "", fmt.Errorf("turn: --turn-public-ip %q is not a valid IP address", c.PublicIP)
	}
	if ip.To4() == nil {
		return "", fmt.Errorf("turn: --turn-public-ip %q is IPv6; only IPv4 relay addresses are supported", c.PublicIP)
	}
	if c.MinPort > c.MaxPort {
		return "", fmt.Errorf("turn: relay port range %d-%d is inverted", c.MinPort, c.MaxPort)
	}
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		return "", fmt.Errorf("turn: --turn-listen %q is not host:port: %w", c.Listen, err)
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() {
		warning = fmt.Sprintf(
			"turn: --turn-public-ip %s is not publicly routable — clients outside this network will gather relay candidates that never connect",
			c.PublicIP)
	}
	return warning, nil
}

// Server is a running embedded TURN relay.
type Server struct {
	cfg  Config
	turn *turn.Server
	conn net.PacketConn

	// port is the port actually bound, not the one requested. They
	// differ whenever Listen asks for :0, and advertising the
	// requested port there would hand clients a dead ":0" candidate.
	port string
}

// Start binds the UDP control port and starts serving TURN. The
// caller owns the returned Server and must Close it on shutdown.
func Start(cfg Config) (*Server, error) {
	cfg.withDefaults()
	warning, err := cfg.Validate()
	if err != nil {
		return nil, err
	}
	if warning != "" {
		log.Printf("⚠ %s", warning)
	}

	conn, err := net.ListenPacket("udp4", cfg.Listen)
	if err != nil {
		return nil, fmt.Errorf("turn: bind %s: %w", cfg.Listen, err)
	}
	_, boundPort, err := net.SplitHostPort(conn.LocalAddr().String())
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("turn: resolve bound port: %w", err)
	}

	relayGen := &turn.RelayAddressGeneratorPortRange{
		RelayAddress: net.ParseIP(cfg.PublicIP),
		Address:      "0.0.0.0",
		MinPort:      cfg.MinPort,
		MaxPort:      cfg.MaxPort,
	}

	loggerFactory := logging.NewDefaultLoggerFactory()
	tracker := newAllocTracker()

	ts, err := turn.NewServer(turn.ServerConfig{
		Realm:         cfg.Realm,
		LoggerFactory: loggerFactory,
		AuthHandler:   authHandler(cfg),
		QuotaHandler: func(username, realm string, srcAddr net.Addr) bool {
			if tracker.allowed(username, cfg.MaxAllocationsPerUser) {
				return true
			}
			log.Printf("turn: allocation quota (%d) reached for %q from %v",
				cfg.MaxAllocationsPerUser, username, srcAddr)
			return false
		},
		EventHandler: turn.EventHandler{
			OnAllocationCreated: func(_, _ net.Addr, _, username, _ string, _ net.Addr, _ int) {
				tracker.add(username)
			},
			OnAllocationDeleted: func(_, _ net.Addr, _, username, _ string) {
				tracker.remove(username)
			},
		},
		PacketConnConfigs: []turn.PacketConnConfig{{
			PacketConn:            conn,
			RelayAddressGenerator: relayGen,
		}},
	})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("turn: start server: %w", err)
	}

	log.Printf("✓ TURN relay listening on %s (public %s, relay ports %d-%d, realm %q)",
		conn.LocalAddr(), cfg.PublicIP, cfg.MinPort, cfg.MaxPort, cfg.Realm)
	log.Printf("  TURN needs inbound UDP on %s and %d-%d — open those in your firewall; "+
		"external reachability cannot be verified from here",
		boundPort, cfg.MinPort, cfg.MaxPort)

	return &Server{cfg: cfg, turn: ts, conn: conn, port: boundPort}, nil
}

// authHandler resolves a presented TURN username to its expected key.
//
// Returning false is the only failure signal pion offers, so an
// expired credential and a malformed one are indistinguishable to the
// client — deliberately. The alternative leaks whether a given user
// ID ever had a valid credential.
func authHandler(cfg Config) turn.AuthHandler {
	return func(username, realm string, srcAddr net.Addr) ([]byte, bool) {
		if _, ok := CheckUsername(username, time.Now()); !ok {
			return nil, false
		}
		password := PasswordFor(cfg.Secret, username)
		return turn.GenerateAuthKey(username, realm, password), true
	}
}

// ICEServersFor mints a fresh credential for userID and returns the
// RTCIceServer entries the browser needs.
//
// Both a stun: and a turn: entry are returned for the same port —
// pion answers STUN binding requests on the control port, so an
// operator running embedded TURN gets self-hosted STUN for free and
// no longer has to send users to Google's public server.
func (s *Server) ICEServersFor(userID string) []map[string]any {
	if s == nil {
		return nil
	}
	hostPort := net.JoinHostPort(s.cfg.PublicIP, s.port)
	creds := Mint(s.cfg.Secret, userID, s.cfg.CredentialTTL, time.Now())

	return []map[string]any{
		{"urls": "stun:" + hostPort},
		{
			"urls":       "turn:" + hostPort + "?transport=udp",
			"username":   creds.Username,
			"credential": creds.Password,
		},
	}
}

// Close stops the relay and releases the control port.
func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	err := s.turn.Close()
	// pion closes the PacketConn it was handed; closing again is
	// harmless but reports "use of closed network connection", which
	// isn't worth surfacing to the caller.
	if cerr := s.conn.Close(); cerr != nil && !strings.Contains(cerr.Error(), "use of closed") {
		log.Printf("turn: close listener: %v", cerr)
	}
	return err
}
