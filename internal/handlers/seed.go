package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/yusufkaraaslan/play-more/internal/models"
	"github.com/yusufkaraaslan/play-more/internal/storage"
)

type seedGame struct {
	Title       string
	Genre       string
	Desc        string
	Tags        []string
	Price       float64
	Discount    int
	IsWebGPU    bool
	Color1      [3]uint8
	Color2      [3]uint8
	VideoURL    string
	CustomAbout string
	Features    []string
	SysReqMin   string
	SysReqRec   string
}

func SeedData(c *gin.Context) {
	// Seed is admin-only. To seed a fresh install, register first, then call this endpoint.
	if !isAdmin(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "admin only"})
		return
	}

	// Check if already seeded
	var count int
	storage.DB.QueryRow(`SELECT COUNT(*) FROM games`).Scan(&count)
	if count > 0 {
		c.JSON(http.StatusOK, gin.H{"message": "already seeded", "games": count})
		return
	}

	// Create demo user with a random password (admin can reset/login if needed)
	pwBytes := make([]byte, 16)
	rand.Read(pwBytes)
	demoPassword := hex.EncodeToString(pwBytes)
	user, err := models.CreateUser("playmore", "demo@playmore.dev", demoPassword)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create demo user"})
		return
	}
	fmt.Printf("[seed] created demo user 'playmore' with random password (use admin to reset if needed)\n")
	storage.DB.Exec(`UPDATE users SET is_developer = 1, bio = 'Official PlayMore demo account' WHERE id = ?`, user.ID)

	// Create developer page
	models.UpsertDeveloperPage(user.ID, "PlayMore Studios", "", "#66c0f4", "steam-dark",
		"Welcome to **PlayMore Studios**! We create demo games to showcase the platform.\n\n- High-quality web games\n- WebGPU support\n- Open source",
		"", "", "", []models.DeveloperLink{{Label: "GitHub", URL: "https://github.com/yusufkaraaslan/play-more"}}, []string{}, nil,
	)

	games := []seedGame{
		{
			Title: "Neon Overdrive", Genre: "racing", Price: 19.99, Discount: 20,
			Tags: []string{"Racing", "Multiplayer", "Cyberpunk"}, IsWebGPU: false,
			Color1: [3]uint8{180, 50, 200}, Color2: [3]uint8{20, 10, 40},
			Desc:        "Race through neon-lit cyberpunk cities at breakneck speeds.",
			CustomAbout: "Experience the thrill of high-speed racing through a stunning cyberpunk cityscape. Customize your vehicle with over 50 unique parts, compete against AI opponents or friends online, and become the ultimate street racer.",
			Features:    []string{"High-speed racing gameplay", "Vehicle customization system", "Online multiplayer (up to 8 players)", "Dynamic weather and day/night cycle", "12 unique tracks across 4 city districts"},
			SysReqMin:   "Browser: Chrome 90+, Firefox 88+\nWebGL: 2.0 required\nRAM: 4GB\nInternet: Broadband",
			SysReqRec:   "Browser: Chrome 110+, Firefox 115+\nWebGPU: Supported\nRAM: 8GB\nDisplay: 1920x1080",
			VideoURL:    "https://www.youtube.com/embed/dQw4w9WgXcQ",
		},
		{
			Title: "Void Echoes", Genre: "rpg", Price: 29.99, Discount: 15,
			Tags: []string{"RPG", "Space", "Story Rich"}, IsWebGPU: true,
			Color1: [3]uint8{40, 80, 180}, Color2: [3]uint8{5, 10, 30},
			Desc:        "An epic space RPG where your choices shape the galaxy.",
			CustomAbout: "Explore distant worlds, forge alliances with alien civilizations, and uncover the ancient mysteries of the Void. Every decision matters in this narrative-driven adventure spanning 40+ hours of gameplay.",
			Features:    []string{"Deep branching narrative", "Real-time tactical combat", "200+ unique items and weapons", "6 recruitable companions", "New Game+ with alternate storylines"},
			SysReqMin:   "Browser: Chrome 100+\nWebGL: 2.0 required\nRAM: 4GB",
			SysReqRec:   "Browser: Chrome 115+\nWebGPU: Recommended\nRAM: 8GB\nDisplay: 1920x1080",
		},
		{
			Title: "Shadow Tactics", Genre: "strategy", Price: 24.99, Discount: 30,
			Tags: []string{"Strategy", "Stealth", "Tactical"}, IsWebGPU: false,
			Color1: [3]uint8{50, 50, 50}, Color2: [3]uint8{10, 15, 10},
			Desc:        "Command an elite team in this tactical stealth game.",
			CustomAbout: "Plan your approach, execute precise maneuvers, and complete missions without leaving a trace. Each specialist has unique abilities that combine for creative solutions to every challenge.",
			Features:    []string{"5 playable specialists with unique skills", "30 hand-crafted missions", "Real-time tactics with pause", "Multiple completion paths per mission", "Challenge modes and leaderboards"},
			SysReqMin:   "Browser: Chrome 90+\nWebGL: 2.0\nRAM: 2GB",
			SysReqRec:   "Browser: Chrome 110+\nRAM: 4GB",
		},
		{
			Title: "XOX Challenge", Genre: "puzzle", Price: 0, Discount: 0,
			Tags: []string{"Puzzle", "Casual", "Classic"}, IsWebGPU: false,
			Color1: [3]uint8{102, 192, 244}, Color2: [3]uint8{20, 30, 50},
			Desc:        "The classic game of Tic-Tac-Toe, beautifully reimagined for the web.",
			CustomAbout: "A modern take on the timeless game. Play against a friend locally with smooth animations and an elegant dark theme. Perfect for quick gaming sessions!",
			Features:    []string{"Local 2-player gameplay", "Smooth animations", "Win detection with highlights", "Score tracking"},
			SysReqMin:   "Any modern browser",
			SysReqRec:   "Chrome, Firefox, or Safari",
		},
	}

	reviews := map[string][]struct {
		Username string
		Rating   int
		Text     string
	}{
		"Neon Overdrive": {
			{"SpeedDemon", 5, "Best racing game I've played in years! The graphics are stunning and the gameplay is incredibly smooth."},
			{"CyberRacer", 5, "Absolutely love the cyberpunk aesthetic. The customization options are endless!"},
			{"CasualPlayer", 4, "Great game but could use more tracks. Looking forward to updates!"},
		},
		"Void Echoes": {
			{"SpaceExplorer", 5, "This game is a masterpiece. The story had me hooked from start to finish."},
			{"RPGFanatic", 5, "Incredible depth and replayability. Every choice matters!"},
			{"StoryLover", 4, "Amazing narrative but combat could be improved."},
		},
		"Shadow Tactics": {
			{"TacticalMind", 5, "Perfect tactical game. Requires real thinking and planning."},
			{"StealthMaster", 4, "Challenging and rewarding. Great for strategy fans."},
		},
		"XOX Challenge": {
			{"CasualGamer99", 5, "Simple but addictive! Perfect for quick breaks."},
			{"PuzzleMaster", 4, "Clean design and smooth gameplay. Would love more game modes!"},
		},
	}

	created := 0
	for _, sg := range games {
		game, err := models.CreateGame(sg.Title, sg.Genre, sg.Desc, user.ID, sg.Price, sg.Tags, sg.IsWebGPU)
		if err != nil {
			continue
		}

		// Update extra fields
		videosJSON := "[]"
		if sg.VideoURL != "" {
			videosJSON = `["` + sg.VideoURL + `"]`
		}
		storage.DB.Exec(`UPDATE games SET discount=?, video_url=?, videos=?, custom_about=?, features=?, sys_req_min=?, sys_req_rec=? WHERE id=?`,
			sg.Discount, sg.VideoURL, videosJSON, sg.CustomAbout, mustJSON(sg.Features), sg.SysReqMin, sg.SysReqRec, game.ID)

		// Generate cover image
		coverPath := generateCoverImage(game.ID, sg.Title, sg.Color1, sg.Color2)
		if coverPath != "" {
			game.UpdateCover("/play/" + game.ID + "/" + filepath.Base(coverPath))
		}

		// Generate screenshot images
		screenshots := []string{}
		for i := 0; i < 3; i++ {
			c1 := sg.Color1
			c2 := sg.Color2
			// Slightly vary colors for each screenshot
			c1[0] = uint8(min255(int(c1[0]) + i*20))
			c2[1] = uint8(min255(int(c2[1]) + i*15))
			ssPath := generateScreenshot(game.ID, sg.Title, c1, c2, i)
			if ssPath != "" {
				screenshots = append(screenshots, "/play/"+game.ID+"/"+filepath.Base(ssPath))
			}
		}
		if len(screenshots) > 0 {
			storage.DB.Exec(`UPDATE games SET screenshots=? WHERE id=?`, mustJSON(screenshots), game.ID)
		}

		// Create XOX game file
		if sg.Title == "XOX Challenge" {
			xoxHTML := generateXOXGame()
			storage.SaveGameFile(game.ID, "index.html", []byte(xoxHTML))
			game.UpdateFiles(storage.GameDir(game.ID), "index.html")
		} else {
			// Create placeholder game file
			placeholder := generatePlaceholderGame(sg.Title, sg.Color1)
			storage.SaveGameFile(game.ID, "index.html", []byte(placeholder))
			game.UpdateFiles(storage.GameDir(game.ID), "index.html")
		}

		// Create reviewer accounts and reviews
		if gameReviews, ok := reviews[sg.Title]; ok {
			for _, r := range gameReviews {
				reviewer, _ := models.GetUserByUsername(r.Username)
				if reviewer == nil {
					reviewer, _ = models.CreateUser(r.Username, r.Username+"@playmore.dev", "demo123")
				}
				if reviewer != nil {
					models.CreateReview(game.ID, reviewer.ID, r.Rating, r.Text)
				}
			}
		}

		created++
	}

	c.JSON(http.StatusOK, gin.H{"message": "seeded", "games": created})
}

func generateCoverImage(gameID, title string, c1, c2 [3]uint8) string {
	dir := storage.GameDir(gameID)
	os.MkdirAll(dir, 0755)

	img := image.NewRGBA(image.Rect(0, 0, 460, 215))
	for y := 0; y < 215; y++ {
		for x := 0; x < 460; x++ {
			t := float64(x+y) / float64(460+215)
			r := uint8(float64(c1[0])*(1-t) + float64(c2[0])*t)
			g := uint8(float64(c1[1])*(1-t) + float64(c2[1])*t)
			b := uint8(float64(c1[2])*(1-t) + float64(c2[2])*t)
			img.SetRGBA(x, y, color.RGBA{r, g, b, 255})
		}
	}
	// Simple text rendering (pixel blocks for title)
	drawText(img, title, 230, 107, color.RGBA{255, 255, 255, 220})

	path := filepath.Join(dir, "cover.png")
	f, err := os.Create(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	png.Encode(f, img)
	return path
}

func generateScreenshot(gameID, title string, c1, c2 [3]uint8, idx int) string {
	dir := storage.GameDir(gameID)
	os.MkdirAll(dir, 0755)

	img := image.NewRGBA(image.Rect(0, 0, 800, 450))
	for y := 0; y < 450; y++ {
		for x := 0; x < 800; x++ {
			t := float64(x) / 800.0
			s := math.Sin(float64(y+idx*50) * 0.02)
			r := uint8(math.Min(255, float64(c1[0])*(1-t)+float64(c2[0])*t+s*20))
			g := uint8(math.Min(255, float64(c1[1])*(1-t)+float64(c2[1])*t+s*15))
			b := uint8(math.Min(255, float64(c1[2])*(1-t)+float64(c2[2])*t+s*25))
			img.SetRGBA(x, y, color.RGBA{r, g, b, 255})
		}
	}

	name := "screenshot_" + string(rune('a'+idx)) + ".png"
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	png.Encode(f, img)
	return path
}

// Simple pixel-block text renderer (no font dependency)
func drawText(img *image.RGBA, text string, cx, cy int, col color.RGBA) {
	charW, charH := 8, 12
	totalW := len(text) * charW
	startX := cx - totalW/2
	startY := cy - charH/2
	for i, ch := range text {
		if ch == ' ' {
			continue
		}
		x := startX + i*charW
		for dy := 0; dy < charH; dy++ {
			for dx := 0; dx < charW-2; dx++ {
				px := x + dx
				py := startY + dy
				if px >= 0 && px < img.Bounds().Dx() && py >= 0 && py < img.Bounds().Dy() {
					// Simple block letter outline
					if dy == 0 || dy == charH-1 || dx == 0 || dx == charW-3 {
						img.SetRGBA(px, py, col)
					}
				}
			}
		}
	}
}

func generatePlaceholderGame(title string, c [3]uint8) string {
	return `<!DOCTYPE html><html><head><style>
*{margin:0;padding:0;box-sizing:border-box}
body{display:flex;flex-direction:column;align-items:center;justify-content:center;height:100vh;background:linear-gradient(135deg,rgb(` + itoa(int(c[0])) + `,` + itoa(int(c[1])) + `,` + itoa(int(c[2])) + `),#0a0a1a);color:#c7d5e0;font-family:Segoe UI,sans-serif}
h1{font-size:48px;margin-bottom:20px;color:#66c0f4}
p{color:#8f98a0;font-size:18px;margin-bottom:10px}
.spin{width:50px;height:50px;border:3px solid rgba(102,192,244,0.3);border-top-color:#66c0f4;border-radius:50%;animation:s 1s linear infinite;margin-top:30px}
@keyframes s{to{transform:rotate(360deg)}}
</style></head><body>
<h1>` + title + `</h1>
<p>Game demo — upload your own game to replace this</p>
<p style="font-size:14px;">This is a placeholder from PlayMore seed data</p>
<div class="spin"></div>
</body></html>`
}

func generateXOXGame() string {
	return `<!DOCTYPE html><html><head><style>
*{margin:0;padding:0;box-sizing:border-box}
body{display:flex;flex-direction:column;align-items:center;justify-content:center;height:100vh;background:linear-gradient(135deg,#1b2838,#171a21);color:#c7d5e0;font-family:Segoe UI,sans-serif}
h1{color:#66c0f4;margin-bottom:20px;font-size:36px}
.status{color:#8f98a0;margin-bottom:30px;font-size:18px}
.board{display:grid;grid-template-columns:repeat(3,120px);gap:15px;margin-bottom:30px}
.cell{width:120px;height:120px;background:rgba(255,255,255,0.05);border:2px solid rgba(255,255,255,0.1);border-radius:12px;display:flex;align-items:center;justify-content:center;font-size:56px;cursor:pointer;transition:all 0.3s}
.cell:hover{background:rgba(102,192,244,0.15);border-color:#66c0f4}
.x{color:#66c0f4}.o{color:#a1cd44}
button{padding:15px 40px;background:#66c0f4;border:none;color:white;font-size:16px;font-weight:bold;border-radius:3px;cursor:pointer;transition:all 0.3s}
button:hover{filter:brightness(1.1);transform:translateY(-2px)}
</style></head><body>
<h1>XOX Challenge</h1>
<p class="status" id="status">Player X's turn</p>
<div class="board" id="board"></div>
<button onclick="resetGame()">New Game</button>
<script>
let board=['','','','','','','','',''],cur='X',active=true;
const boardEl=document.getElementById('board');
for(let i=0;i<9;i++){const c=document.createElement('div');c.className='cell';c.onclick=()=>play(i,c);boardEl.appendChild(c);}
function play(i,c){if(board[i]||!active)return;board[i]=cur;c.textContent=cur;c.classList.add(cur.toLowerCase());if(checkWin()){document.getElementById('status').textContent='Player '+cur+' wins!';active=false;}else if(board.every(x=>x)){document.getElementById('status').textContent="Draw!";active=false;}else{cur=cur==='X'?'O':'X';document.getElementById('status').textContent="Player "+cur+"'s turn";}}
function checkWin(){const w=[[0,1,2],[3,4,5],[6,7,8],[0,3,6],[1,4,7],[2,5,8],[0,4,8],[2,4,6]];return w.some(([a,b,c])=>board[a]&&board[a]===board[b]&&board[a]===board[c]);}
function resetGame(){board=['','','','','','','','',''];cur='X';active=true;document.getElementById('status').textContent="Player X's turn";document.querySelectorAll('.cell').forEach(c=>{c.textContent='';c.classList.remove('x','o');});}
</script></body></html>`
}

func mustJSON(v []string) string {
	if len(v) == 0 {
		return "[]"
	}
	s := "["
	for i, item := range v {
		if i > 0 {
			s += ","
		}
		s += `"` + item + `"`
	}
	s += "]"
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func min255(n int) int {
	if n > 255 {
		return 255
	}
	return n
}
