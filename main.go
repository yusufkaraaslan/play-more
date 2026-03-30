package main

import (
	"embed"
	"flag"
	"fmt"
	"log"

	"github.com/yusufkaraaslan/play-more/internal/server"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

//go:embed all:frontend
var frontendFS embed.FS

func main() {
	port := flag.Int("port", 8080, "server port")
	dataDir := flag.String("data", "data", "data directory path")
	flag.Parse()

	// Initialize storage
	if err := storage.InitDB(*dataDir); err != nil {
		log.Fatal("Failed to initialize database:", err)
	}
	if err := storage.InitFileStorage(*dataDir); err != nil {
		log.Fatal("Failed to initialize file storage:", err)
	}

	fmt.Printf("PlayMore server starting on http://localhost:%d\n", *port)
	fmt.Printf("Data directory: %s\n", *dataDir)

	r := server.New(frontendFS)
	if err := r.Run(fmt.Sprintf(":%d", *port)); err != nil {
		log.Fatal("Server failed:", err)
	}
}
