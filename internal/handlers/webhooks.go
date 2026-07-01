package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
)

type createWebhookInput struct {
	URL    string   `json:"url" binding:"required"`
	Events []string `json:"events" binding:"required"`
}

type updateWebhookInput struct {
	URL    string   `json:"url"`
	Events []string `json:"events"`
	Active *bool    `json:"active"`
}

// CreateWebhookHandler handles POST /api/v1/webhooks.
// The plaintext secret is returned ONCE in the response —
// the user must store it now, because subsequent reads (list,
// get) never include it.
func CreateWebhookHandler(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	var input createWebhookInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
		return
	}
	if !strings.HasPrefix(input.URL, "http://") && !strings.HasPrefix(input.URL, "https://") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "url must be http(s)://"})
		return
	}
	w, err := models.CreateWebhook(user.ID, input.URL, input.Events)
	if err != nil {
		switch {
		case models.IsWebhookLimitError(err):
			c.JSON(http.StatusBadRequest, gin.H{"error": "webhook limit reached (max 20 per user)"})
		case models.IsInvalidWebhookEventError(err):
			c.JSON(http.StatusBadRequest, gin.H{"error": "one or more event names are unknown"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create webhook"})
		}
		return
	}
	c.JSON(http.StatusCreated, w)
}

// ListWebhooksHandler handles GET /api/v1/webhooks.
func ListWebhooksHandler(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	hooks, err := models.ListWebhooks(user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list webhooks"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"webhooks": hooks})
}

// GetWebhookHandler handles GET /api/v1/webhooks/:id.
func GetWebhookHandler(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	w, err := models.GetWebhook(c.Param("id"), user.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "webhook not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get webhook"})
		return
	}
	c.JSON(http.StatusOK, w)
}

// UpdateWebhookHandler handles PUT /api/v1/webhooks/:id.
// The secret is NOT updateable — rotate by revoke + create.
func UpdateWebhookHandler(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	var input updateWebhookInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
		return
	}
	if !strings.HasPrefix(input.URL, "http://") && !strings.HasPrefix(input.URL, "https://") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "url must be http(s)://"})
		return
	}
	if input.Active == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "active is required"})
		return
	}
	err := models.UpdateWebhook(c.Param("id"), user.ID, input.URL, input.Events, *input.Active)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			c.JSON(http.StatusNotFound, gin.H{"error": "webhook not found"})
		case models.IsInvalidWebhookEventError(err):
			c.JSON(http.StatusBadRequest, gin.H{"error": "one or more event names are unknown"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update webhook"})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// DeleteWebhookHandler handles DELETE /api/v1/webhooks/:id.
func DeleteWebhookHandler(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	err := models.DeleteWebhook(c.Param("id"), user.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "webhook not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete webhook"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ListWebhookDeliveriesHandler handles GET /api/v1/webhooks/:id/deliveries.
func ListWebhookDeliveriesHandler(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	limit := 50
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil {
			limit = n
		}
	}
	ds, err := models.ListDeliveries(c.Param("id"), user.ID, limit)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "webhook not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list deliveries"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deliveries": ds})
}
