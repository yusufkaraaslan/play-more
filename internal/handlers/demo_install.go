package handlers

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

// InstallDemoGames adds missing playable examples without changing existing rows.
// AdminRequired on the route enforces session-only administrator access.
func InstallDemoGames(c *gin.Context) {
	demoInstallMu.Lock()
	defer demoInstallMu.Unlock()
	installed, skipped := []gin.H{}, []gin.H{}
	for _, sg := range demoCatalog() {
		// Exclude the fictional storefront placeholders.
		if sg.Key == "" {
			continue
		}
		var id string
		err := storage.DB.QueryRow(`SELECT id FROM games WHERE demo_key = ? OR title = ? ORDER BY CASE WHEN demo_key = ? THEN 0 ELSE 1 END LIMIT 1`, sg.Key, sg.Title, sg.Key).Scan(&id)
		if err == nil {
			skipped = append(skipped, gin.H{"key": sg.Key, "id": id, "title": sg.Title})
			continue
		}
		if err != sql.ErrNoRows {
			log.Printf("install demo lookup: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to inspect demo games", "installed": installed, "skipped": skipped})
			return
		}
		game, err := createDemoGame(sg, middleware.GetUser(c).ID)
		if err != nil {
			log.Printf("install demo %s: %v", sg.Key, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to install demo game", "installed": installed, "skipped": skipped})
			return
		}
		installed = append(installed, gin.H{"key": sg.Key, "id": game.ID, "title": sg.Title})
		adminLog(c, "install_demo", "game", game.ID)
	}
	c.JSON(http.StatusOK, gin.H{"installed": installed, "skipped": skipped})
}

// Publish only after all assets and readiness checks succeed. Failed installs
// remove their own new draft and files; already installed demos are untouched.
func createDemoGame(sg seedGame, ownerID string) (_ *models.Game, err error) {
	game, err := models.CreateGame(sg.Title, sg.Genre, sg.Desc, ownerID, sg.Price, sg.Tags, sg.IsWebGPU, sg.Multiplayer)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			if e := game.Delete(); e != nil {
				log.Printf("cleanup demo row %s: %v", game.ID, e)
			}
			if e := storage.DeleteGameFiles(game.ID); e != nil {
				log.Printf("cleanup demo files %s: %v", game.ID, e)
			}
		}
	}()
	var key any
	if sg.Key != "" {
		key = sg.Key
	}
	videos := []string{}
	if sg.VideoURL != "" {
		videos = append(videos, sg.VideoURL)
	}
	_, err = storage.DB.Exec(`UPDATE games SET demo_key=?, discount=?, video_url=?, videos=?, custom_about=?, features=?, sys_req_min=?, sys_req_rec=? WHERE id=?`,
		key, sg.Discount, sg.VideoURL, mustJSON(videos), sg.CustomAbout, mustJSON(sg.Features), sg.SysReqMin, sg.SysReqRec, game.ID)
	if err != nil {
		return nil, err
	}
	cover := generateCoverImage(game.ID, sg.Title, sg.Color1, sg.Color2)
	if cover == "" {
		return nil, fmt.Errorf("generate cover")
	}
	if err = game.UpdateCover("/play/" + game.ID + "/" + filepath.Base(cover)); err != nil {
		return nil, err
	}
	shots := []string{}
	for i := 0; i < 3; i++ {
		shot := generateScreenshot(game.ID, sg.Title, sg.Color1, sg.Color2, i)
		if shot == "" {
			return nil, fmt.Errorf("generate screenshot")
		}
		shots = append(shots, "/play/"+game.ID+"/"+filepath.Base(shot))
	}
	if _, err = storage.DB.Exec(`UPDATE games SET screenshots=? WHERE id=?`, mustJSON(shots), game.ID); err != nil {
		return nil, err
	}
	html := ""
	if sg.HTML != nil {
		html = sg.HTML()
	} else {
		html = generatePlaceholderGame(sg.Title, sg.Color1)
	}
	if err = storage.SaveGameFile(game.ID, "index.html", []byte(html)); err != nil {
		return nil, err
	}
	if err = game.UpdateFiles(storage.GameDir(game.ID), "index.html"); err != nil {
		return nil, err
	}
	refreshed, err := models.GetGameByID(game.ID)
	if err != nil {
		return nil, err
	}
	if !models.StoreReadiness(refreshed).Ready {
		return nil, fmt.Errorf("incomplete demo store page")
	}
	if _, err = storage.DB.Exec(`UPDATE users SET is_developer=1 WHERE id=?`, ownerID); err != nil {
		return nil, err
	}
	_, err = storage.DB.Exec(`UPDATE games SET published=1 WHERE id=?`, game.ID)
	if err != nil {
		return nil, err
	}
	refreshed.Published = true
	return refreshed, nil
}
