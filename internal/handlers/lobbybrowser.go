package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/lobby"
)

// ListPublicLobbiesHandler handles GET /api/v1/games/:id/lobbies.
// Returns public, non-started lobbies for the lobby browser.
func ListPublicLobbiesHandler(c *gin.Context) {
	gameID := c.Param("id")
	lobbies := lobby.Default.ListPublicLobbies(gameID)
	if lobbies == nil {
		lobbies = []lobby.PublicLobbyInfo{}
	}
	c.JSON(http.StatusOK, gin.H{"lobbies": lobbies})
}
