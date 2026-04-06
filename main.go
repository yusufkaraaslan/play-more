package main

import (
	"embed"
	"flag"
	"fmt"
	"log"
	"os"

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
	flag.Parse()

	// Initialize storage
	if err := storage.InitDB(*dataDir); err != nil {
		log.Fatal("Failed to initialize database:", err)
	}
	if err := storage.InitFileStorage(*dataDir); err != nil {
		log.Fatal("Failed to initialize file storage:", err)
	}

	middleware.StartRateLimitCleanup()
	middleware.StartAnalyticsWriter()

	fmt.Printf("PlayMore server starting on http://localhost:%d\n", *port)
	fmt.Printf("Data directory: %s\n", *dataDir)
	if *goatcounter != "" {
		fmt.Printf("GoatCounter: %s\n", *goatcounter)
	}

	r := server.New(frontendFS, *goatcounter)
	if err := r.Run(fmt.Sprintf(":%d", *port)); err != nil {
		log.Fatal("Server failed:", err)
	}
}
