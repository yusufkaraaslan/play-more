package handlers

import (
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/models"
)

type registerInput struct {
	Username string `json:"username" binding:"required,min=3,max=30"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
}

func Register(c *gin.Context) {
	var input registerInput
	if err := c.ShouldBindJSON(&input); err != nil {
		log.Printf("Validation error in Register: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input. Please check all fields and try again."})
		return
	}

	input.Username = strings.TrimSpace(input.Username)
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))

	user, err := models.CreateUser(input.Username, input.Email, input.Password)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			// Return generic error to prevent user enumeration
			c.JSON(http.StatusConflict, gin.H{"error": "Registration failed. Please try again with different information."})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create user"})
		return
	}

	token, err := models.CreateSession(user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session"})
		return
	}

	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("session", token, 30*24*3600, "/", "", true, true)
	c.JSON(http.StatusCreated, gin.H{"user": user})
}

type loginInput struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func Login(c *gin.Context) {
	var input loginInput
	if err := c.ShouldBindJSON(&input); err != nil {
		log.Printf("Validation error in Login: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input. Please check all fields and try again."})
		return
	}

	user, err := models.GetUserByEmail(strings.ToLower(strings.TrimSpace(input.Email)))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	if !user.CheckPassword(input.Password) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	token, err := models.CreateSession(user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session"})
		return
	}

	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("session", token, 30*24*3600, "/", "", true, true)
	c.JSON(http.StatusOK, gin.H{"user": user})
}

func Logout(c *gin.Context) {
	token, _ := c.Cookie("session")
	if token != "" {
		models.DeleteSession(token)
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("session", "", -1, "/", "", true, true)
	c.JSON(http.StatusOK, gin.H{"message": "logged out"})
}

func Me(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	stats, _ := models.GetUserStats(user.ID)
	c.JSON(http.StatusOK, gin.H{"user": user, "stats": stats})
}
