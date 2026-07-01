# PlayMore Go SDK

Hand-written Go SDK for the PlayMore API. Idiomatic, no third-party
HTTP client, mirrors the OpenAPI spec at /openapi.yaml.

## Install

```bash
go get github.com/yusufkaraaslan/play-more/sdk-go
```

## Quick start

```go
import "github.com/yusufkaraaslan/play-more/sdk-go/sdk"

c := sdk.New("pm_k_…") // generate from Settings → API Keys
c.BaseURL = "https://playmore.example.com/api/v1"

games, err := c.ListGames(ctx)
```

## Chunked upload

```go
sha, _ := sdk.FileSHA256("./build.zip")
res, err := c.UploadChunked(ctx, sdk.ChunkedUploadOptions{
    Path:     "./build.zip",
    Size:     fi.Size(),
    Filename: "build.zip",
    Kind:     "new_game",
    Metadata: map[string]any{"title": "My Game", "genre": "action"},
    SHA256:   sha,
    OnProgress: func(w, t int64) { fmt.Printf("\r%0.f%%", 100*float64(w)/float64(t)) },
})
```

## Webhook signature verification

Use the helpers from the `webhooks.go` file on the receiving end
(your Lambda, your HTTP server) to verify the request really came
from PlayMore:

```go
import "github.com/yusufkaraaslan/play-more/sdk-go/sdk"

http.HandleFunc("/hooks/playmore", func(w http.ResponseWriter, r *http.Request) {
    if err := sdk.VerifySignatureFromRequest(secret, r); err != nil {
        http.Error(w, err.Error(), 400)
        return
    }
    // body is verified — process the event
})
```

## Examples

- `examples/upload/` — CLI uploader for a single game zip

## Hand-written vs auto-generated

This package is **hand-written** to be idiomatic and small. The
Python and JavaScript SDKs are auto-generated from the OpenAPI
spec by `.github/workflows/sdk-generate.yml`. The Go SDK is the
reference; the others follow it.
