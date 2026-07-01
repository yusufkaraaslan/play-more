package handlers

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/webhook"
)

// listBuildsResponse is the JSON shape returned by GET
// /api/v1/games/:id/builds.
type listBuildsResponse struct {
	Builds []*models.Build `json:"builds"`
}

// ListBuildsHandler handles GET /api/v1/games/:id/builds.
// Optional ?channel=internal|beta|stable filter.
func ListBuildsHandler(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	gameID := c.Param("id")
	channel := c.Query("channel")
	if channel != "" && !models.IsValidBuildChannel(channel) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel"})
		return
	}
	builds, err := models.ListBuilds(gameID, user.ID, channel)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "game not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list builds"})
		return
	}
	c.JSON(http.StatusOK, listBuildsResponse{Builds: builds})
}

// getBuildResponse is the JSON shape for GET /api/v1/games/:id/builds/:build_id.
type getBuildResponse struct {
	Build *models.Build `json:"build"`
}

// GetBuildHandler handles GET /api/v1/games/:id/builds/:build_id.
func GetBuildHandler(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	build, err := models.GetBuild(c.Param("build_id"), c.Param("id"), user.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "build not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get build"})
		return
	}
	c.JSON(http.StatusOK, getBuildResponse{Build: build})
}

// activateBuildResponse is the JSON shape for PUT
// /api/v1/games/:id/builds/:build_id/activate.
type activateBuildResponse struct {
	OK bool `json:"ok"`
}

// ActivateBuildHandler handles PUT /api/v1/games/:id/builds/:build_id/activate.
// Promotes a build to active for its channel, demoting the
// previous active build (if any).
func ActivateBuildHandler(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	if err := models.SetActiveBuild(c.Param("build_id"), c.Param("id"), user.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "build not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to activate build"})
		return
	}
	// Look up the activated build to include channel + build
	// number in the webhook payload. Best-effort: if the
	// lookup fails the dispatch still fires with what we
	// have.
	build, _ := models.GetBuild(c.Param("build_id"), c.Param("id"), user.ID)
	payload := gin.H{
		"build_id": c.Param("build_id"),
		"game_id":  c.Param("id"),
		"via":      "activate",
	}
	if build != nil {
		payload["build_number"] = build.BuildNumber
		payload["channel"] = build.Channel
	}
	webhook.Dispatch(models.WebhookEventBuildPromoted, user.ID, payload)
	c.JSON(http.StatusOK, activateBuildResponse{OK: true})
}

// RollbackBuildHandler handles POST /api/v1/games/:id/builds/:build_id/rollback.
// Rolls back the active build for the build's channel to the
// previous (most recent non-active) build.
func RollbackBuildHandler(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	gameID := c.Param("id")
	// The :build_id in the path is the CURRENT active build to
	// roll back from. We look up its channel, find the previous
	// build in that channel, and activate it.
	current, err := models.GetBuild(c.Param("build_id"), gameID, user.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "build not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get build"})
		return
	}
	if !current.IsActive {
		c.JSON(http.StatusBadRequest, gin.H{"error": "build is not the active build for its channel"})
		return
	}
	prev, err := models.PreviousActiveBuild(gameID, current.Channel)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no previous build to roll back to"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to find previous build"})
		return
	}
	if err := models.SetActiveBuild(prev.ID, gameID, user.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to roll back"})
		return
	}
	webhook.Dispatch(models.WebhookEventBuildRolledBack, user.ID, gin.H{
		"build_id":                  prev.ID,
		"game_id":                   gameID,
		"build_number":              prev.BuildNumber,
		"channel":                   current.Channel,
		"rolled_back_from_build_id": current.ID,
	})
	c.JSON(http.StatusOK, activateBuildResponse{OK: true})
}

// DeleteBuildHandler handles DELETE /api/v1/games/:id/builds/:build_id.
// Refuses to delete the active build (caller must activate a
// different build first).
func DeleteBuildHandler(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	err := models.DeleteBuild(c.Param("build_id"), c.Param("id"), user.ID)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			c.JSON(http.StatusNotFound, gin.H{"error": "build not found"})
		case err.Error() == "cannot delete the active build for a channel":
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete build"})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
