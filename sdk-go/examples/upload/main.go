// upload pushes a game.zip to PlayMore using the chunked
// upload protocol. The same code works for re-uploads by
// setting Kind to "reupload" and supplying a GameID.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/yusufkaraaslan/play-more/sdk-go/sdk"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: upload <game.zip>")
	}
	path := os.Args[1]

	apiKey := os.Getenv("PLAYMORE_API_KEY")
	if apiKey == "" {
		log.Fatal("set PLAYMORE_API_KEY")
	}
	server := os.Getenv("PLAYMORE_SERVER")
	if server == "" {
		server = "https://playmore.world"
	}

	fi, err := os.Stat(path)
	if err != nil {
		log.Fatalf("stat: %v", err)
	}
	sha, err := sdk.FileSHA256(path)
	if err != nil {
		log.Fatalf("sha256: %v", err)
	}

	c := sdk.New(apiKey)
	c.BaseURL = server + "/api/v1"
	res, err := c.UploadChunked(context.Background(), sdk.ChunkedUploadOptions{
		Path:     path,
		Size:     fi.Size(),
		Filename: fi.Name(),
		Kind:     "new_game",
		Metadata: map[string]any{
			"title": "My New Game",
			"genre": "action",
		},
		SHA256: sha,
		OnProgress: func(written, total int64) {
			pct := float64(written) / float64(total) * 100
			fmt.Printf("\rUploading… %.0f%%", pct)
		},
	})
	if err != nil {
		log.Fatalf("upload: %v", err)
	}
	fmt.Printf("\nDone. Game ID: %s\n", res.GameID)
}
