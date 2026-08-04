package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"embed"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	emailpkg "github.com/yusufkaraaslan/play-more/internal/email"
	"github.com/yusufkaraaslan/play-more/internal/lobby"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/server"
	"github.com/yusufkaraaslan/play-more/internal/storage"
	"github.com/yusufkaraaslan/play-more/internal/turnserver"
	"github.com/yusufkaraaslan/play-more/internal/uploadgc"
	"github.com/yusufkaraaslan/play-more/internal/webhook"
	"golang.org/x/crypto/acme/autocert"
)

//go:embed all:frontend
var frontendFS embed.FS

// loadEnvFile reads a .env file and sets environment variables (does not override existing ones).
func loadEnvFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// Remove surrounding quotes
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}

// randomTURNSecret generates a shared secret for ephemeral TURN
// credentials. Used both by the setup wizard (written to .env) and at
// startup when --turn is on but no secret was supplied.
func randomTURNSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("Failed to generate TURN secret: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func runSetup() {
	reader := bufio.NewReader(os.Stdin)
	ask := func(prompt, def string) string {
		if def != "" {
			fmt.Printf("%s [%s]: ", prompt, def)
		} else {
			fmt.Printf("%s: ", prompt)
		}
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			return def
		}
		return line
	}
	askYN := func(prompt string, def bool) bool {
		d := "n"
		if def {
			d = "y"
		}
		ans := strings.ToLower(ask(prompt+" (y/n)", d))
		return ans == "y" || ans == "yes"
	}

	fmt.Println("╔══════════════════════════════════════╗")
	fmt.Println("║       PlayMore Production Setup      ║")
	fmt.Println("╚══════════════════════════════════════╝")
	fmt.Println()

	var lines []string

	// Basic
	port := ask("Server port", "8080")
	lines = append(lines, "PLAYMORE_PORT="+port)

	dataDir := ask("Data directory", "data")
	lines = append(lines, "PLAYMORE_DATA="+dataDir)

	baseURL := ask("Public URL (e.g. https://playmore.example.com)", "")
	if baseURL != "" {
		lines = append(lines, "PLAYMORE_BASE_URL="+baseURL)
	}

	// HTTPS
	fmt.Println()
	fmt.Println("── HTTPS ──")
	fmt.Println("  1) Auto (Let's Encrypt — needs ports 80+443 open)")
	fmt.Println("  2) Manual (provide cert+key files)")
	fmt.Println("  3) None (use reverse proxy like Caddy/nginx)")
	tlsChoice := ask("HTTPS mode", "3")

	switch tlsChoice {
	case "1":
		domain := ask("Domain name", "")
		if domain != "" {
			lines = append(lines, "PLAYMORE_AUTO_TLS=true")
			lines = append(lines, "PLAYMORE_DOMAIN="+domain)
			if baseURL == "" {
				lines = append(lines, "PLAYMORE_BASE_URL=https://"+domain)
			}
		}
	case "2":
		cert := ask("TLS certificate file path", "")
		key := ask("TLS private key file path", "")
		if cert != "" && key != "" {
			lines = append(lines, "PLAYMORE_TLS_CERT="+cert)
			lines = append(lines, "PLAYMORE_TLS_KEY="+key)
		}
	}

	// Email
	fmt.Println()
	if askYN("Enable email (verification + password reset)?", false) {
		smtpHost := ask("SMTP host", "smtp.gmail.com")
		smtpPort := ask("SMTP port", "587")
		smtpUser := ask("SMTP username", "")
		smtpPass := ask("SMTP password", "")
		smtpFrom := ask("From address", smtpUser)
		lines = append(lines, "PLAYMORE_SMTP_HOST="+smtpHost)
		lines = append(lines, "PLAYMORE_SMTP_PORT="+smtpPort)
		lines = append(lines, "PLAYMORE_SMTP_USER="+smtpUser)
		lines = append(lines, "PLAYMORE_SMTP_PASS="+smtpPass)
		lines = append(lines, "PLAYMORE_SMTP_FROM="+smtpFrom)
	}

	// Multiplayer — embedded TURN relay
	fmt.Println()
	fmt.Println("── Multiplayer (TURN) ──")
	fmt.Println("  WebRTC needs a TURN relay for players behind symmetric NAT or")
	fmt.Println("  strict firewalls. Without one they fall back to the WebSocket")
	fmt.Println("  relay, which is capped at 30 msg/s and always reliable+ordered —")
	fmt.Println("  fine for turn-based games, too slow for real-time ones.")
	fmt.Println()
	fmt.Println("  Requires inbound UDP on the control port and the relay range.")
	fmt.Println("  HTTP-only ingress (Cloudflare Tunnel, most PaaS) CANNOT carry it.")
	if askYN("Run the embedded TURN relay?", false) {
		turnIP := ask("Public IPv4 address of this server", "")
		if turnIP == "" {
			fmt.Println("  ⚠ Skipping TURN — a public IP is required (it can't be auto-detected behind NAT).")
		} else {
			turnPort := ask("TURN control port (UDP)", "3478")
			minPort := ask("Relay port range — lowest (UDP)", "49152")
			maxPort := ask("Relay port range — highest (UDP)", "49251")
			lines = append(lines, "PLAYMORE_TURN=true")
			lines = append(lines, "PLAYMORE_TURN_LISTEN=0.0.0.0:"+turnPort)
			lines = append(lines, "PLAYMORE_TURN_PUBLIC_IP="+turnIP)
			lines = append(lines, "PLAYMORE_TURN_MIN_PORT="+minPort)
			lines = append(lines, "PLAYMORE_TURN_MAX_PORT="+maxPort)
			lines = append(lines, "PLAYMORE_TURN_SECRET="+randomTURNSecret())
			fmt.Println()
			fmt.Println("  ✓ TURN configured. Open these in your firewall:")
			fmt.Printf("      udp/%s        (control)\n", turnPort)
			fmt.Printf("      udp/%s-%s  (relay allocations)\n", minPort, maxPort)
		}
	}

	// Analytics
	fmt.Println()
	gc := ask("GoatCounter URL (leave empty to skip)", "")
	if gc != "" {
		lines = append(lines, "PLAYMORE_GOATCOUNTER="+gc)
	}

	// Write .env
	envContent := "# PlayMore configuration — generated by ./playmore setup\n" + strings.Join(lines, "\n") + "\n"
	envPath := ".env"
	if err := os.WriteFile(envPath, []byte(envContent), 0600); err != nil {
		log.Fatal("Failed to write .env:", err)
	}

	fmt.Println()
	fmt.Println("✓ Saved to .env")
	fmt.Println()
	fmt.Println("Now just run:")
	fmt.Println("  ./playmore")
	fmt.Println()
}

func main() {
	// Handle "setup" subcommand before flag parsing
	if len(os.Args) > 1 && os.Args[1] == "setup" {
		runSetup()
		return
	}

	// Load .env file (doesn't override existing env vars)
	loadEnvFile(".env")

	// Default to release mode unless GIN_MODE is explicitly set
	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}

	port := flag.Int("port", 8080, "server port")
	dataDir := flag.String("data", "data", "data directory path")
	goatcounter := flag.String("goatcounter", "", "GoatCounter URL (e.g. https://mysite.goatcounter.com)")
	tlsCert := flag.String("tls-cert", "", "path to TLS certificate file")
	tlsKey := flag.String("tls-key", "", "path to TLS private key file")
	autoTLS := flag.Bool("auto-tls", false, "enable automatic TLS via Let's Encrypt")
	domain := flag.String("domain", "", "domain name for auto-TLS certificate (required with --auto-tls)")
	smtpHost := flag.String("smtp-host", "", "SMTP server hostname")
	smtpPort := flag.Int("smtp-port", 587, "SMTP server port")
	smtpUser := flag.String("smtp-user", "", "SMTP username")
	smtpPass := flag.String("smtp-pass", "", "SMTP password")
	smtpFrom := flag.String("smtp-from", "", "From address for emails")
	baseURL := flag.String("base-url", "", "Public base URL (e.g. https://playmore.example.com)")
	gamesDomain := flag.String("games-domain", "", "Optional separate domain for game files (e.g. games.example.com) — strongest isolation against malicious uploaded games")
	trustedProxies := flag.String("trusted-proxies", "", "Comma-separated list of trusted proxy CIDRs (e.g. '127.0.0.1/32,10.0.0.0/8'). Empty = trust no proxy headers.")
	forceSecure := flag.Bool("behind-tls-proxy", false, "Always set Secure flag on cookies (use when behind a TLS-terminating reverse proxy)")
	stunServers := flag.String("stun-servers", "stun:stun.l.google.com:19302", "Comma-separated STUN server URLs for WebRTC NAT traversal")
	turnServers := flag.String("turn-servers", "", "Comma-separated TURN server URLs for WebRTC relay fallback (e.g. 'turn:user:pass@turn.example.com:3478')")
	turnEnable := flag.Bool("turn", false, "Run an embedded TURN relay for WebRTC NAT traversal. Requires inbound UDP — see docs/SETUP.md.")
	turnListen := flag.String("turn-listen", "0.0.0.0:3478", "UDP address for the embedded TURN control port")
	turnPublicIP := flag.String("turn-public-ip", "", "Public IPv4 address advertised in TURN relay candidates (required with --turn; cannot be auto-detected behind NAT)")
	turnRealm := flag.String("turn-realm", "", "TURN realm (defaults to --domain, else 'playmore')")
	turnSecret := flag.String("turn-secret", "", "Shared secret for ephemeral TURN credentials. Auto-generated if empty — set it explicitly to keep credentials valid across restarts.")
	turnMinPort := flag.Int("turn-min-port", 0, "Lowest UDP port for TURN relay allocations (default 49152)")
	turnMaxPort := flag.Int("turn-max-port", 0, "Highest UDP port for TURN relay allocations (default 49251)")
	uploadsGC := flag.Bool("uploads-gc", false, "Enable daily uploads/ directory sweep — deletes files unreferenced by any DB row and older than 90 days. Off by default; review --uploads-gc-dry-run output before enabling.")
	uploadsGCDryRun := flag.Bool("uploads-gc-dry-run", false, "Run uploads GC in dry-run mode — log what would be pruned but don't actually delete. Requires --uploads-gc.")
	flag.Parse()

	// Environment variables as fallback (flags take priority)
	if !isFlagSet("port") {
		if v := os.Getenv("PLAYMORE_PORT"); v != "" {
			if p, err := strconv.Atoi(v); err == nil {
				*port = p
			}
		}
	}
	if !isFlagSet("data") {
		if v := os.Getenv("PLAYMORE_DATA"); v != "" {
			*dataDir = v
		}
	}
	if !isFlagSet("goatcounter") {
		if v := os.Getenv("PLAYMORE_GOATCOUNTER"); v != "" {
			*goatcounter = v
		}
	}
	if !isFlagSet("tls-cert") {
		if v := os.Getenv("PLAYMORE_TLS_CERT"); v != "" {
			*tlsCert = v
		}
	}
	if !isFlagSet("tls-key") {
		if v := os.Getenv("PLAYMORE_TLS_KEY"); v != "" {
			*tlsKey = v
		}
	}
	if !isFlagSet("auto-tls") {
		if v := os.Getenv("PLAYMORE_AUTO_TLS"); v == "true" || v == "1" {
			*autoTLS = true
		}
	}
	if !isFlagSet("domain") {
		if v := os.Getenv("PLAYMORE_DOMAIN"); v != "" {
			*domain = v
		}
	}

	if !isFlagSet("smtp-host") {
		if v := os.Getenv("PLAYMORE_SMTP_HOST"); v != "" {
			*smtpHost = v
		}
	}
	if !isFlagSet("smtp-port") {
		if v := os.Getenv("PLAYMORE_SMTP_PORT"); v != "" {
			if p, err := strconv.Atoi(v); err == nil {
				*smtpPort = p
			}
		}
	}
	if !isFlagSet("smtp-user") {
		if v := os.Getenv("PLAYMORE_SMTP_USER"); v != "" {
			*smtpUser = v
		}
	}
	if !isFlagSet("smtp-pass") {
		if v := os.Getenv("PLAYMORE_SMTP_PASS"); v != "" {
			*smtpPass = v
		}
	}
	if !isFlagSet("smtp-from") {
		if v := os.Getenv("PLAYMORE_SMTP_FROM"); v != "" {
			*smtpFrom = v
		}
	}
	if !isFlagSet("base-url") {
		if v := os.Getenv("PLAYMORE_BASE_URL"); v != "" {
			*baseURL = v
		}
	}
	if !isFlagSet("games-domain") {
		if v := os.Getenv("PLAYMORE_GAMES_DOMAIN"); v != "" {
			*gamesDomain = v
		}
	}
	if !isFlagSet("trusted-proxies") {
		if v := os.Getenv("PLAYMORE_TRUSTED_PROXIES"); v != "" {
			*trustedProxies = v
		}
	}
	if !isFlagSet("behind-tls-proxy") {
		if v := os.Getenv("PLAYMORE_BEHIND_TLS_PROXY"); v == "true" || v == "1" {
			*forceSecure = true
		}
	}
	middleware.ForceSecureCookies = *forceSecure

	// STUN/TURN env var fallbacks
	if !isFlagSet("stun-servers") {
		if v := os.Getenv("PLAYMORE_STUN_SERVERS"); v != "" {
			*stunServers = v
		}
	}
	if !isFlagSet("turn-servers") {
		if v := os.Getenv("PLAYMORE_TURN_SERVERS"); v != "" {
			*turnServers = v
		}
	}
	// Embedded TURN relay env fallbacks
	if !isFlagSet("turn") {
		if v := os.Getenv("PLAYMORE_TURN"); v == "true" || v == "1" {
			*turnEnable = true
		}
	}
	if !isFlagSet("turn-listen") {
		if v := os.Getenv("PLAYMORE_TURN_LISTEN"); v != "" {
			*turnListen = v
		}
	}
	if !isFlagSet("turn-public-ip") {
		if v := os.Getenv("PLAYMORE_TURN_PUBLIC_IP"); v != "" {
			*turnPublicIP = v
		}
	}
	if !isFlagSet("turn-realm") {
		if v := os.Getenv("PLAYMORE_TURN_REALM"); v != "" {
			*turnRealm = v
		}
	}
	if !isFlagSet("turn-secret") {
		if v := os.Getenv("PLAYMORE_TURN_SECRET"); v != "" {
			*turnSecret = v
		}
	}
	if !isFlagSet("turn-min-port") {
		if v := os.Getenv("PLAYMORE_TURN_MIN_PORT"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				*turnMinPort = n
			}
		}
	}
	if !isFlagSet("turn-max-port") {
		if v := os.Getenv("PLAYMORE_TURN_MAX_PORT"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				*turnMaxPort = n
			}
		}
	}
	server.RTCIceServers = server.ParseIceServers(*stunServers, *turnServers)

	// Validate TLS options
	if (*tlsCert == "") != (*tlsKey == "") {
		log.Fatal("Both --tls-cert/PLAYMORE_TLS_CERT and --tls-key/PLAYMORE_TLS_KEY must be provided together")
	}
	if *autoTLS && *domain == "" {
		log.Fatal("--domain/PLAYMORE_DOMAIN is required when using --auto-tls")
	}
	if *autoTLS && *tlsCert != "" {
		log.Fatal("Cannot use --auto-tls together with --tls-cert/--tls-key")
	}

	// Initialize storage
	if err := storage.InitDB(*dataDir); err != nil {
		log.Fatal("Failed to initialize database:", err)
	}
	if err := storage.InitFileStorage(*dataDir); err != nil {
		log.Fatal("Failed to initialize file storage:", err)
	}

	// Configure email
	emailpkg.CurrentConfig = &emailpkg.Config{
		Host:    *smtpHost,
		Port:    *smtpPort,
		User:    *smtpUser,
		Pass:    *smtpPass,
		From:    *smtpFrom,
		BaseURL: *baseURL,
	}

	// SMTP health check (non-fatal — email is optional)
	if emailpkg.Configured() {
		if err := emailpkg.HealthCheck(); err != nil {
			fmt.Printf("⚠  SMTP health check failed (%s:%d): %v\n", emailpkg.CurrentConfig.Host, emailpkg.CurrentConfig.Port, err)
			if emailpkg.IsLocalBridge() {
				fmt.Println("   (local bridge detected — certificate verification skipped)")
				fmt.Println("   Email verification/reset will fail until SMTP is reachable.")
			} else {
				fmt.Println("   Email verification/reset will fail until SMTP is reachable.")
			}
		} else {
			fmt.Printf("✓  SMTP reachable at %s:%d\n", emailpkg.CurrentConfig.Host, emailpkg.CurrentConfig.Port)
		}
	} else {
		fmt.Println("⚠  SMTP not configured — uploads, reviews, and devlogs are BLOCKED")
		fmt.Println("    until users can verify their email. Configure SMTP in .env to allow")
		fmt.Println("    user content. See docs/SETUP.md.")
	}

	middleware.StartRateLimitCleanup()
	middleware.StartAnalyticsWriter()
	lobby.Default.RestoreLobbies()
	lobby.Default.StartCleanup(middleware.ShutdownCh)
	webhook.Start()
	uploadgc.UploadsGCEnabled = *uploadsGC
	uploadgc.UploadsGCDryRun = *uploadsGCDryRun
	uploadgc.Start(context.Background())

	// Embedded TURN relay — opt-in, because it needs inbound UDP that
	// most deployments (anything behind an HTTP-only tunnel) can't
	// provide. Failing to bind is fatal rather than a warning: the
	// operator asked for TURN explicitly, and silently continuing
	// would leave NAT-restricted players on the WebSocket relay while
	// the logs claim everything is fine.
	var turnSrv *turnserver.Server
	if *turnEnable {
		if *turnMinPort < 0 || *turnMinPort > 65535 || *turnMaxPort < 0 || *turnMaxPort > 65535 {
			log.Fatal("--turn-min-port and --turn-max-port must be between 0 and 65535")
		}
		secret := *turnSecret
		if secret == "" {
			secret = randomTURNSecret()
			fmt.Println("⚠  --turn-secret not set — generated a random one for this process.")
			fmt.Println("    Credentials issued before a restart stop working after it, and")
			fmt.Println("    multiple instances won't agree on them. Set PLAYMORE_TURN_SECRET")
			fmt.Println("    to pin it. See docs/SETUP.md.")
		}
		realm := *turnRealm
		if realm == "" {
			realm = *domain // falls back to turnserver.DefaultRealm when empty
		}
		var err error
		turnSrv, err = turnserver.Start(turnserver.Config{
			Listen:   *turnListen,
			PublicIP: *turnPublicIP,
			Realm:    realm,
			Secret:   secret,
			MinPort:  uint16(*turnMinPort),
			MaxPort:  uint16(*turnMaxPort),
		})
		if err != nil {
			log.Fatalf("Failed to start embedded TURN relay: %v", err)
		}
		server.TURNCredentialFunc = turnSrv.ICEServersFor
	}

	// Periodic cleanup of expired sessions, email tokens, and stale play sessions
	go func() {
		for {
			select {
			case <-time.After(1 * time.Hour):
				models.CleanupExpiredSessions()
				models.CleanupStalePlaySessions()
			case <-middleware.ShutdownCh:
				return
			}
		}
	}()

	scheme := "http"
	if *tlsCert != "" || *autoTLS {
		scheme = "https"
	}
	fmt.Printf("PlayMore server starting on %s://localhost:%d\n", scheme, *port)
	fmt.Printf("Data directory: %s\n", *dataDir)
	if *goatcounter != "" {
		fmt.Printf("GoatCounter: %s\n", *goatcounter)
	}
	if *autoTLS {
		fmt.Printf("Auto-TLS: enabled for %s\n", *domain)
	}

	r := server.New(frontendFS, *goatcounter, *gamesDomain, *baseURL, *trustedProxies)
	addr := fmt.Sprintf(":%d", *port)

	// Timeouts protect against slowloris and runaway connections.
	// Game uploads can be large (up to 500 MiB) so WriteTimeout is generous;
	// ReadHeaderTimeout is strict because headers should arrive in milliseconds.
	makeServer := func(addr string) *http.Server {
		return &http.Server{
			Addr:              addr,
			Handler:           r.Handler(),
			ReadTimeout:       0, // disabled — body read time depends on upload size
			ReadHeaderTimeout: 10 * time.Second,
			WriteTimeout:      0, // disabled — large game files take time to stream
			IdleTimeout:       120 * time.Second,
			MaxHeaderBytes:    1 << 20, // 1 MiB
		}
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	var mainSrv *http.Server
	var redirectSrv *http.Server

	if *autoTLS {
		certDir := filepath.Join(*dataDir, "certs")
		m := &autocert.Manager{
			Cache:      autocert.DirCache(certDir),
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(*domain),
		}
		mainSrv = makeServer(":443")
		mainSrv.TLSConfig = &tls.Config{
			GetCertificate: m.GetCertificate,
			MinVersion:     tls.VersionTLS13,
		}
		h := m.HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := *domain
			if host == "" {
				host = r.Host
			}
			target := "https://" + host + r.URL.Path
			if len(r.URL.RawQuery) > 0 {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusMovedPermanently)
		}))
		redirectSrv = &http.Server{
			Addr:              ":80",
			Handler:           h,
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
	} else if *tlsCert != "" {
		mainSrv = makeServer(addr)
		mainSrv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS13}
	} else {
		mainSrv = makeServer(addr)
	}

	// Start main server in background
	go func() {
		var err error
		if *autoTLS {
			err = mainSrv.ListenAndServeTLS("", "")
		} else if *tlsCert != "" {
			err = mainSrv.ListenAndServeTLS(*tlsCert, *tlsKey)
		} else {
			err = mainSrv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			log.Fatal("Server failed:", err)
		}
	}()

	if redirectSrv != nil {
		go func() {
			if err := redirectSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatal("Redirect server failed:", err)
			}
		}()
		fmt.Printf("Listening on :443 (auto-TLS) and :80 (redirect)\n")
	} else {
		fmt.Printf("PlayMore server listening on %s://localhost:%d\n", scheme, *port)
	}

	sig := <-sigCh
	fmt.Printf("\nReceived %v, shutting down gracefully...\n", sig)

	middleware.StopAnalyticsWriter()
	webhook.Stop()
	lobby.Default.Shutdown()
	if turnSrv != nil {
		if err := turnSrv.Close(); err != nil {
			log.Printf("Error stopping TURN relay: %v", err)
		}
	}
	close(middleware.ShutdownCh)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := mainSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Error shutting down main server: %v", err)
	}
	if redirectSrv != nil {
		if err := redirectSrv.Shutdown(shutdownCtx); err != nil {
			log.Printf("Error shutting down redirect server: %v", err)
		}
	}
	fmt.Println("Server stopped.")
}

// tryStartLocalBridge attempts to start protonmail-bridge via systemctl.
// Returns true if a start command was issued successfully.
func tryStartLocalBridge() bool {
	// Only try on Linux with systemd
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return false
	}
	// Try common service names (protonmail-bridge or proton-bridge)
	for _, svc := range []string{"protonmail-bridge", "proton-bridge"} {
		cmd := exec.Command("systemctl", "is-active", "--quiet", svc)
		if err := cmd.Run(); err == nil {
			// Already active
			return true
		}
		// Try starting
		cmd = exec.Command("systemctl", "start", svc)
		if err := cmd.Run(); err == nil {
			fmt.Printf("→  Started %s via systemctl\n", svc)
			return true
		}
		// Try user-level service
		cmd = exec.Command("systemctl", "--user", "start", svc)
		if err := cmd.Run(); err == nil {
			fmt.Printf("→  Started user service %s via systemctl\n", svc)
			return true
		}
	}
	return false
}

func isFlagSet(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}
