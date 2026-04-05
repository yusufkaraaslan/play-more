package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/middleware"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

type AchievementDef struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
}

type UserAchievement struct {
	AchievementDef
	UnlockedAt string `json:"unlocked_at"`
}

var Achievements = []AchievementDef{
	{"first_play", "First Steps", "Play your first game", "🎮"},
	{"collector_5", "Collector", "Add 5 games to your library", "📚"},
	{"collector_25", "Hoarder", "Add 25 games to your library", "🗄️"},
	{"critic", "Critic", "Write your first review", "✍️"},
	{"prolific_10", "Prolific Critic", "Write 10 reviews", "📝"},
	{"marathon_10", "Marathon Runner", "Play for 10 hours total", "⏱️"},
	{"marathon_100", "Hardcore Gamer", "Play for 100 hours total", "🔥"},
	{"creator", "Creator", "Upload your first game", "📤"},
	{"creator_5", "Publisher", "Upload 5 games", "🏢"},
	{"social_5", "Social Butterfly", "Follow 5 developers", "🦋"},
	{"popular_10", "Popular", "Get 10 followers", "⭐"},
	{"devlog", "Storyteller", "Write your first devlog", "📖"},
}

var achievementMap map[string]AchievementDef

func init() {
	achievementMap = make(map[string]AchievementDef)
	for _, a := range Achievements {
		achievementMap[a.ID] = a
	}
}

// CheckAchievements checks all achievement conditions for a user
// and awards any newly unlocked ones. Returns list of newly unlocked.
func CheckAchievements(userID string) []AchievementDef {
	var newly []AchievementDef

	// Get current unlocked set
	unlocked := make(map[string]bool)
	rows, err := storage.DB.Query(`SELECT achievement_id FROM user_achievements WHERE user_id = ?`, userID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id string
			rows.Scan(&id)
			unlocked[id] = true
		}
	}

	// Gather stats
	var libraryCount, reviewCount, gameCount, followingCount, followerCount, devlogCount int
	var totalPlaytime float64
	var playCount int

	storage.DB.QueryRow(`SELECT COUNT(*) FROM library WHERE user_id = ?`, userID).Scan(&libraryCount)
	storage.DB.QueryRow(`SELECT COUNT(*) FROM reviews WHERE user_id = ?`, userID).Scan(&reviewCount)
	storage.DB.QueryRow(`SELECT COUNT(*) FROM games WHERE developer_id = ?`, userID).Scan(&gameCount)
	storage.DB.QueryRow(`SELECT COUNT(*) FROM follows WHERE follower_id = ?`, userID).Scan(&followingCount)
	storage.DB.QueryRow(`SELECT COUNT(*) FROM follows WHERE followed_id = ?`, userID).Scan(&followerCount)
	storage.DB.QueryRow(`SELECT COUNT(*) FROM devlogs WHERE user_id = ?`, userID).Scan(&devlogCount)
	storage.DB.QueryRow(`SELECT COALESCE(SUM(total_seconds), 0), COALESCE(SUM(play_count), 0) FROM playtime WHERE user_id = ?`, userID).Scan(&totalPlaytime, &playCount)

	totalHours := totalPlaytime / 3600.0

	// Check each achievement
	checks := map[string]bool{
		"first_play":   playCount >= 1,
		"collector_5":  libraryCount >= 5,
		"collector_25": libraryCount >= 25,
		"critic":       reviewCount >= 1,
		"prolific_10":  reviewCount >= 10,
		"marathon_10":  totalHours >= 10,
		"marathon_100": totalHours >= 100,
		"creator":      gameCount >= 1,
		"creator_5":    gameCount >= 5,
		"social_5":     followingCount >= 5,
		"popular_10":   followerCount >= 10,
		"devlog":       devlogCount >= 1,
	}

	for id, earned := range checks {
		if earned && !unlocked[id] {
			_, err := storage.DB.Exec(
				`INSERT OR IGNORE INTO user_achievements (user_id, achievement_id) VALUES (?, ?)`,
				userID, id,
			)
			if err == nil {
				if def, ok := achievementMap[id]; ok {
					newly = append(newly, def)
					// Create notification
					CreateNotification(userID, "achievement", "Achievement unlocked: "+def.Icon+" "+def.Name, "", "")
				}
			}
		}
	}

	return newly
}

// GetUserAchievements returns all achievements with unlock status for a user.
func GetUserAchievements(c *gin.Context) {
	username := c.Param("username")
	var userID string
	err := storage.DB.QueryRow(`SELECT id FROM users WHERE username = ?`, username).Scan(&userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	// Get unlocked achievements
	unlocked := make(map[string]string) // id -> unlocked_at
	rows, _ := storage.DB.Query(`SELECT achievement_id, unlocked_at FROM user_achievements WHERE user_id = ?`, userID)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var id, at string
			rows.Scan(&id, &at)
			unlocked[id] = at
		}
	}

	// Build full list
	all := []UserAchievement{}
	for _, def := range Achievements {
		ua := UserAchievement{AchievementDef: def}
		if at, ok := unlocked[def.ID]; ok {
			ua.UnlockedAt = at
		}
		all = append(all, ua)
	}

	c.JSON(http.StatusOK, gin.H{
		"achievements": all,
		"unlocked":     len(unlocked),
		"total":        len(Achievements),
	})
}

// CheckMyAchievements triggers a check and returns newly unlocked.
func CheckMyAchievements(c *gin.Context) {
	user := middleware.GetUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	newly := CheckAchievements(user.ID)
	c.JSON(http.StatusOK, gin.H{"newly_unlocked": newly})
}
