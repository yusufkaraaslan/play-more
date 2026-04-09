package main

import (
	"embed"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/server"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

//go:embed all:frontend
var frontendFS embed.FS

func main() {
	// Default to release mode unless GIN_MODE is explicitly set
	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}

	port := flag.Int("port", 8080, "server port")
	dataDir := flag.String("data", "data", "data directory path")
	goatcounter := flag.String("goatcounter", "", "GoatCounter URL (e.g. https://mysite.goatcounter.com)")
	tlsCert := flag.String("tls-cert", "", "path to TLS certificate file")
	tlsKey := flag.String("tls-key", "", "path to TLS private key file")
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

	// Validate TLS: both or neither
	if (*tlsCert == "") != (*tlsKey == "") {
		log.Fatal("Both --tls-cert/PLAYMORE_TLS_CERT and --tls-key/PLAYMORE_TLS_KEY must be provided together")
	}

	// Initialize storage
	if err := storage.InitDB(*dataDir); err != nil {
		log.Fatal("Failed to initialize database:", err)
	}
	if err := storage.InitFileStorage(*dataDir); err != nil {
		log.Fatal("Failed to initialize file storage:", err)
	}

	middleware.StartRateLimitCleanup()
	middleware.StartAnalyticsWriter()

	scheme := "http"
	if *tlsCert != "" {
		scheme = "https"
	}
	fmt.Printf("PlayMore server starting on %s://localhost:%d\n", scheme, *port)
	fmt.Printf("Data directory: %s\n", *dataDir)
	if *goatcounter != "" {
		fmt.Printf("GoatCounter: %s\n", *goatcounter)
	}

	r := server.New(frontendFS, *goatcounter)
	addr := fmt.Sprintf(":%d", *port)
	if *tlsCert != "" {
		if err := r.RunTLS(addr, *tlsCert, *tlsKey); err != nil {
			log.Fatal("Server failed:", err)
		}
	} else {
		if err := r.Run(addr); err != nil {
			log.Fatal("Server failed:", err)
		}
	}
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
