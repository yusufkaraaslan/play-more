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
	"github.com/yusufkaraaslan/play-more/internal/middleware"
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
	Multiplayer bool
	Color1      [3]uint8
	Color2      [3]uint8
	VideoURL    string
	CustomAbout string
	Features    []string
	SysReqMin   string
	SysReqRec   string
}

func SeedData(c *gin.Context) {
	// Seed is admin-only and session-only (API keys rejected, matching
	// every other admin endpoint).
	if middleware.IsAPIKeyAuth(c) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
		return
	}
	if !isAdmin(c) {
		// Match admin endpoint convention — 404 hides admin/seed existence
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
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
			Tags: []string{"Racing", "Multiplayer", "Cyberpunk"}, IsWebGPU: false, Multiplayer: true,
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
		{
			Title: "Co-op Canvas", Genre: "experimental", Price: 0, Discount: 0,
			Tags: []string{"Multiplayer", "Casual", "Creative"}, IsWebGPU: false, Multiplayer: true,
			Color1: [3]uint8{80, 200, 160}, Color2: [3]uint8{15, 25, 30},
			Desc:        "A shared drawing board — create a lobby, share the code, and doodle together in real time.",
			CustomAbout: "A tiny working demo of PlayMore's multiplayer lobbies. Every player gets a live colored cursor; click to drop dots on a shared canvas that syncs to everyone in the lobby. Built with the embeddable playmore-mp.js client (~120 lines of game code) — the reference example for adding online multiplayer to your own games. See /docs for the protocol.",
			Features:    []string{"Real-time shared canvas (up to 8 players)", "Live colored cursors for every player", "Create/join lobbies with a shareable code", "Reference implementation of playmore-mp.js"},
			SysReqMin:   "Any modern browser",
			SysReqRec:   "Chrome, Firefox, or Safari",
		},
		{
			Title: "MP Test Arena", Genre: "experimental", Price: 0, Discount: 0,
			Tags: []string{"Multiplayer", "Diagnostic", "WebRTC"}, IsWebGPU: false, Multiplayer: true,
			Color1: [3]uint8{60, 140, 220}, Color2: [3]uint8{12, 18, 28},
			Desc:        "A multiplayer diagnostic dashboard — tests every feature: WebRTC P2P, relay fallback, transport switching, bandwidth stats, keepalive, play sessions.",
			CustomAbout: "The end-to-end test game for PlayMore's multiplayer stack. Shows live transport status (WebRTC vs relay) per peer, bandwidth stats, connection state changes, cursor sync, and message logging. Use this to verify that P2P data channels are working, keepalive is running, and relay fallback kicks in correctly.",
			Features:    []string{"Per-peer transport indicator (WebRTC/relay)", "Live bandwidth stats (sent/received)", "Connection state change log", "Cursor sync + click-to-ping test", "Broadcast + unicast message test", "Play session status display"},
			SysReqMin:   "Chrome 90+, Firefox 88+, Safari 15+",
			SysReqRec:   "Latest Chrome or Firefox",
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
		game, err := models.CreateGame(sg.Title, sg.Genre, sg.Desc, user.ID, sg.Price, sg.Tags, sg.IsWebGPU, sg.Multiplayer)
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
		} else if sg.Title == "Co-op Canvas" {
			storage.SaveGameFile(game.ID, "index.html", []byte(generateCoopCanvasGame()))
			game.UpdateFiles(storage.GameDir(game.ID), "index.html")
		} else if sg.Title == "MP Test Arena" {
			storage.SaveGameFile(game.ID, "index.html", []byte(generateMPTestArenaGame()))
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
					// Random password per reviewer — demo accounts, not used for login.
					pw := make([]byte, 8)
					rand.Read(pw)
					reviewer, _ = models.CreateUser(r.Username, r.Username+"@playmore.dev", hex.EncodeToString(pw))
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

// generateCoopCanvasGame returns the "Co-op Canvas" demo — a real
// working multiplayer game that uses the embeddable playmore-mp.js
// client. It is the reference example for the lobby protocol: every
// player is a live colored cursor, clicking drops a synced dot. Kept
// dependency-free and free of template literals so it lives cleanly in
// a Go raw string.
func generateCoopCanvasGame() string {
	return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no">
<title>Co-op Canvas</title>
<style>
  html,body{margin:0;height:100%;background:#0f191e;color:#e6f2ef;font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;overflow:hidden;}
  #wrap{position:fixed;inset:0;display:flex;flex-direction:column;}
  #bar{padding:10px 14px;display:flex;gap:16px;align-items:center;font-size:14px;background:rgba(0,0,0,.25);}
  #bar b{color:#50c8a0;}
  #status{margin-left:auto;color:#9fb4ae;}
  #board{flex:1;position:relative;cursor:crosshair;touch-action:none;}
  canvas{position:absolute;inset:0;width:100%;height:100%;}
  #hint{position:absolute;left:50%;top:14px;transform:translateX(-50%);color:#7f948e;font-size:13px;pointer-events:none;}
  #menu{position:fixed;inset:0;background:rgba(8,14,18,.94);display:flex;align-items:center;justify-content:center;z-index:10;}
  #menu.hidden{display:none;}
  #panel{background:#132229;border:1px solid #1f3640;border-radius:12px;padding:24px;width:min(360px,90vw);text-align:center;}
  #panel h1{margin:0 0 4px;font-size:20px;color:#50c8a0;}
  #panel p{margin:0 0 16px;color:#9fb4ae;font-size:13px;}
  #panel button{width:100%;padding:11px;margin:5px 0;border:0;border-radius:8px;background:#50c8a0;color:#08110d;font-size:14px;font-weight:600;cursor:pointer;}
  #panel button.sec{background:#1f3640;color:#e6f2ef;}
  #panel button:disabled{opacity:.5;cursor:default;}
  #joinrow{display:flex;gap:6px;margin-top:6px;}
  #joinrow input{flex:1;padding:10px;border-radius:8px;border:1px solid #1f3640;background:#0f191e;color:#e6f2ef;text-transform:uppercase;}
  #joinrow button{width:auto;padding:10px 16px;margin:0;}
  #plist{list-style:none;padding:0;margin:12px 0;text-align:left;}
  #plist li{padding:6px 8px;display:flex;justify-content:space-between;font-size:13px;border-bottom:1px solid #16262d;}
  .codebig{font-family:monospace;font-size:28px;letter-spacing:4px;color:#e6f2ef;margin:6px 0 2px;}
</style>
</head>
<body>
<div id="wrap">
  <div id="bar">
    <span>Lobby <b id="code">–</b></span>
    <span id="me">connecting…</span>
    <span id="status"></span>
  </div>
  <div id="board">
    <canvas id="dots"></canvas>
    <canvas id="cursors"></canvas>
    <div id="hint">Move to show your cursor · click to drop a dot · everyone sees it</div>
  </div>
</div>
<div id="menu"><div id="panel"><div id="panelbody"></div></div></div>
<script src="/playmore-mp.js"></script>
<script>
(function(){
  'use strict';
  var board=document.getElementById('board');
  var dots=document.getElementById('dots'), cursors=document.getElementById('cursors');
  var dctx=dots.getContext('2d'), cctx=cursors.getContext('2d');
  var peers={};           // id -> {x,y,color,name}
  var myColor='#50c8a0', myId=null;
  var lastSent=0;

  function resize(){
    var r=board.getBoundingClientRect();
    [dots,cursors].forEach(function(cv){ cv.width=r.width; cv.height=r.height; });
  }
  window.addEventListener('resize', function(){ resize(); redrawCursors(); });

  // Stable color per player id (hash -> hue). No coordination needed.
  function colorFor(id){
    var h=0; for(var i=0;i<id.length;i++){ h=(h*31+id.charCodeAt(i))>>>0; }
    return 'hsl('+(h%360)+',70%,60%)';
  }
  function drawDot(nx,ny,color){
    dctx.fillStyle=color;
    dctx.beginPath();
    dctx.arc(nx*dots.width, ny*dots.height, 6, 0, Math.PI*2);
    dctx.fill();
  }
  function redrawCursors(){
    cctx.clearRect(0,0,cursors.width,cursors.height);
    for(var id in peers){
      var p=peers[id];
      if(p.x==null) continue;
      var x=p.x*cursors.width, y=p.y*cursors.height;
      cctx.fillStyle=p.color;
      cctx.beginPath(); cctx.arc(x,y,5,0,Math.PI*2); cctx.fill();
      cctx.font='12px system-ui'; cctx.fillText(p.name||'', x+9, y+4);
    }
  }
  function setStatus(){
    var n=1+Object.keys(peers).length;
    document.getElementById('status').textContent=n+' player'+(n===1?'':'s')+' here';
  }

  function norm(ev){
    var r=board.getBoundingClientRect();
    return { x:Math.min(1,Math.max(0,(ev.clientX-r.left)/r.width)),
             y:Math.min(1,Math.max(0,(ev.clientY-r.top)/r.height)) };
  }
  board.addEventListener('pointermove', function(ev){
    var now=Date.now();
    if(now-lastSent<66) return;        // ~15/s, well under the 30/s relay cap
    lastSent=now;
    var p=norm(ev);
    PlayMore.send({ t:'cur', x:p.x, y:p.y });
  });
  board.addEventListener('pointerdown', function(ev){
    var p=norm(ev);
    drawDot(p.x, p.y, myColor);                 // draw locally now
    PlayMore.send({ t:'dot', x:p.x, y:p.y });   // and tell everyone
  });

  // ── Lobby menu (game-managed) ───────────────────────────────────────
  // The game owns its own lobby UI. onReady fires pre-lobby with an empty
  // code — show the menu; the player picks Quick Play / Create / Join, and
  // onLaunch fires when the match actually begins. See /docs for the protocol.
  var lobby=null, matchmaking=false;
  function esc(s){ return String(s==null?'':s).replace(/[&<>"]/g,function(c){return {'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]; }); }
  function showMenu(show){ document.getElementById('menu').classList.toggle('hidden', !show); }
  function renderMenu(){
    var body=document.getElementById('panelbody');
    if(matchmaking){
      body.innerHTML='<h1>Finding players…</h1><p id="mmstat">Searching…</p><button class="sec" id="mm-cancel">Cancel</button>';
      document.getElementById('mm-cancel').onclick=function(){ matchmaking=false; PlayMore.cancelMatchmake(); renderMenu(); };
      return;
    }
    if(!lobby){
      body.innerHTML='<h1>Co-op Canvas</h1><p>Play with friends or match with randoms.</p>'+
        '<button id="mm">Quick Play</button>'+
        '<button class="sec" id="create">Create Lobby</button>'+
        '<div id="joinrow"><input id="jcode" placeholder="CODE" maxlength="6" autocomplete="off"><button class="sec" id="join">Join</button></div>';
      document.getElementById('mm').onclick=function(){ matchmaking=true; PlayMore.quickPlay(2); renderMenu(); };
      document.getElementById('create').onclick=function(){ PlayMore.createLobby(); };
      document.getElementById('join').onclick=function(){ var c=document.getElementById('jcode').value; if(c) PlayMore.joinLobby(c); };
      return;
    }
    var me=null, allReady=true;
    (lobby.players||[]).forEach(function(p){ if(p.id===myId)me=p; if(!p.host&&!p.ready)allReady=false; });
    var isHost=!!(me&&me.host);
    var h='<h1>Lobby</h1><div class="codebig">'+esc(lobby.code)+'</div><p>Share this code with friends</p><ul id="plist">';
    (lobby.players||[]).forEach(function(p){
      h+='<li><span>'+esc(p.username)+'</span><span>'+(p.host?'HOST':(p.ready?'✔ ready':'…'))+'</span></li>';
    });
    h+='</ul>';
    h+=isHost ? '<button id="start"'+(allReady?'':' disabled')+'>Start Game</button>'
             : '<button id="ready">'+((me&&me.ready)?'Not Ready':'Ready Up')+'</button>';
    h+='<button class="sec" id="leave">Leave</button>';
    body.innerHTML=h;
    if(isHost){ document.getElementById('start').onclick=function(){ PlayMore.startGame(); }; }
    else { document.getElementById('ready').onclick=function(){ PlayMore.readyUp(!(me&&me.ready)); }; }
    document.getElementById('leave').onclick=function(){ PlayMore.leaveLobby(); lobby=null; renderMenu(); };
  }

  // ── PlayMore lobby wiring ───────────────────────────────────────────
  PlayMore.onReady(function(ctx){
    resize();
    myId = ctx.you ? ctx.you.id : null;
    myColor = myId ? colorFor(myId) : '#50c8a0';
    var meEl=document.getElementById('me');
    meEl.textContent = 'you are';
    var swatch=document.createElement('span');
    swatch.style.cssText='display:inline-block;width:12px;height:12px;border-radius:50%;margin-left:6px;vertical-align:middle;background:'+myColor;
    meEl.appendChild(swatch);
    showMenu(true); renderMenu();
  });
  PlayMore.onLobbyState(function(l){ lobby=l; matchmaking=false; renderMenu(); });
  PlayMore.onMatchmaking(function(m){ var el=document.getElementById('mmstat'); if(el) el.textContent='Searching… '+m.queueSize+'/'+m.targetCount; });
  PlayMore.onLaunch(function(l){
    lobby=l; matchmaking=false; showMenu(false);
    document.getElementById('code').textContent = l.code || '–';
    syncPeers(l.players);
  });
  PlayMore.onPlayers(function(players){ syncPeers(players); });
  PlayMore.onMessage(function(from, d){
    if(!d || from===myId) return;
    if(!peers[from]) peers[from]={ x:null,y:null,color:colorFor(from),name:'' };
    if(d.t==='cur'){ peers[from].x=d.x; peers[from].y=d.y; redrawCursors(); }
    else if(d.t==='dot'){ drawDot(d.x, d.y, peers[from].color); }
  });
  PlayMore.onClosed(function(){
    document.getElementById('status').textContent='lobby closed';
    peers={}; redrawCursors();
    lobby=null; matchmaking=false; showMenu(true); renderMenu();
  });

  function syncPeers(players){
    var live={};
    (players||[]).forEach(function(p){
      if(p.id===myId) return;
      live[p.id]=true;
      if(!peers[p.id]) peers[p.id]={ x:null,y:null,color:colorFor(p.id),name:p.username||'' };
      else peers[p.id].name=p.username||peers[p.id].name;
    });
    for(var id in peers){ if(!live[id]) delete peers[id]; }  // drop players who left
    setStatus(); redrawCursors();
  }

  // If opened outside a lobby (direct /play link), still render.
  resize();
})();
</script>
</body>
</html>`
}

func generateMPTestArenaGame() string {
	return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no">
<title>MP Test Arena</title>
<style>
  *{box-sizing:border-box;margin:0;padding:0;}
  html,body{height:100%;background:#0c121c;color:#c8d6e5;font-family:system-ui,-apple-system,sans-serif;overflow:hidden;}
  #app{display:flex;flex-direction:column;height:100%;}
  #top{padding:8px 14px;background:rgba(0,0,0,.3);display:flex;gap:14px;align-items:center;font-size:13px;border-bottom:1px solid #1a2735;}
  #top b{color:#3c8ce0;}
  .dot{display:inline-block;width:8px;height:8px;border-radius:50%;margin-right:4px;}
  .dot.on{background:#3c8ce0;box-shadow:0 0 6px #3c8ce0;}
  .dot.off{background:#555;}
  #main{flex:1;display:flex;overflow:hidden;}
  #canvas-area{flex:1;position:relative;cursor:crosshair;touch-action:none;background:#0a0f16;}
  canvas{position:absolute;inset:0;width:100%;height:100%;}
  #sidebar{width:300px;border-left:1px solid #1a2735;display:flex;flex-direction:column;overflow:hidden;}
  #sidebar h3{padding:8px 12px;font-size:11px;text-transform:uppercase;letter-spacing:1px;color:#5a7a99;background:rgba(0,0,0,.2);border-bottom:1px solid #1a2735;}
  #peers{flex:1;overflow-y:auto;padding:6px;}
  .peer{padding:6px 8px;margin-bottom:4px;border-radius:6px;background:rgba(255,255,255,.04);font-size:12px;}
  .peer .name{font-weight:bold;display:flex;align-items:center;gap:6px;}
  .peer .tport{font-size:10px;padding:1px 6px;border-radius:3px;margin-left:auto;}
  .tport.webrtc{background:#1a3a1a;color:#4ade80;}
  .tport.relay{background:#3a2a1a;color:#fbbf24;}
  .peer .bw{font-size:10px;color:#5a7a99;margin-top:3px;}
  #log{height:180px;border-top:1px solid #1a2735;overflow-y:auto;padding:4px 8px;font-family:monospace;font-size:11px;line-height:1.4;}
  #log .ts{color:#3a5070;}
  #log .ev{color:#5a7a99;}
  #log .ok{color:#4ade80;}
  #log .warn{color:#fbbf24;}
  #log .err{color:#f87171;}
  #controls{padding:8px 12px;border-top:1px solid #1a2735;display:flex;gap:8px;flex-wrap:wrap;}
  button{padding:5px 12px;border-radius:6px;border:1px solid #2a3a4a;background:#15212e;color:#c8d6e5;font-size:12px;cursor:pointer;}
  button:hover{background:#1d2d3a;border-color:#3c5a7a;}
  .btn-pri{background:#1a3a5a;border-color:#3c8ce0;color:#c8e0ff;}
  #stats{padding:6px 12px;font-size:11px;color:#5a7a99;border-top:1px solid #1a2735;}
  #menu{position:fixed;inset:0;background:rgba(6,10,16,.95);display:flex;align-items:center;justify-content:center;z-index:20;}
  #menu.hidden{display:none;}
  #panel{background:#101a26;border:1px solid #1e3247;border-radius:12px;padding:24px;width:min(360px,90vw);text-align:center;}
  #panel h1{margin:0 0 4px;font-size:20px;color:#3c8ce0;}
  #panel p{margin:0 0 16px;color:#5a7a99;font-size:13px;}
  #panel button{width:100%;padding:11px;margin:5px 0;border:1px solid #3c8ce0;border-radius:8px;background:#1a3a5a;color:#c8e0ff;font-size:14px;font-weight:600;cursor:pointer;}
  #panel button.sec{background:#15212e;border-color:#2a3a4a;color:#c8d6e5;}
  #panel button:disabled{opacity:.5;cursor:default;}
  #joinrow{display:flex;gap:6px;margin-top:6px;}
  #joinrow input{flex:1;padding:10px;border-radius:8px;border:1px solid #2a3a4a;background:#0a0f16;color:#c8d6e5;text-transform:uppercase;}
  #joinrow button{width:auto;padding:10px 16px;margin:0;}
  #plist{list-style:none;padding:0;margin:12px 0;text-align:left;}
  #plist li{padding:6px 8px;display:flex;justify-content:space-between;font-size:13px;border-bottom:1px solid #16242f;}
  .codebig{font-family:monospace;font-size:28px;letter-spacing:4px;color:#c8e0ff;margin:6px 0 2px;}
</style>
</head>
<body>
<div id="app">
  <div id="top">
    <span>Lobby <b id="code">--</b></span>
    <span><span class="dot off" id="dot-mp"></span>MP</span>
    <span id="me-label">connecting...</span>
    <span id="session-status" style="margin-left:auto;font-size:11px;color:#5a7a99;"></span>
  </div>
  <div id="main">
    <div id="canvas-area">
      <canvas id="cv-dots"></canvas>
      <canvas id="cv-cursors"></canvas>
    </div>
    <div id="sidebar">
      <h3>Peers</h3>
      <div id="peers"><div style="color:#5a7a99;font-size:12px;padding:8px;">No peers connected</div></div>
      <div id="log"></div>
      <div id="controls">
        <button class="btn-pri" id="btn-bcast">Broadcast Ping</button>
        <button id="btn-uni">Unicast to #1</button>
        <button id="btn-stats">Stats</button>
      </div>
      <div id="stats"></div>
    </div>
  </div>
</div>
<div id="menu"><div id="panel"><div id="panelbody"></div></div></div>
<script src="/playmore-mp.js"></script>
<script>
(function(){
  'use strict';
  var area=document.getElementById('canvas-area');
  var dotCv=document.getElementById('cv-dots'), curCv=document.getElementById('cv-cursors');
  var dctx=dotCv.getContext('2d'), cctx=curCv.getContext('2d');
  var peers={};
  var myId=null, myColor='#3c8ce0';
  var lastSent=0;
  var logEl=document.getElementById('log');
  var statsEl=document.getElementById('stats');

  function log(type,msg){
    var t=new Date().toLocaleTimeString();
    var d=document.createElement('div');
    d.innerHTML='<span class="ts">'+t+'</span> <span class="'+type+'">'+msg+'</span>';
    logEl.appendChild(d);
    logEl.scrollTop=logEl.scrollHeight;
    while(logEl.children.length>100) logEl.removeChild(logEl.firstChild);
  }

  function resize(){
    var r=area.getBoundingClientRect();
    [dotCv,curCv].forEach(function(c){c.width=r.width;c.height=r.height;});
  }
  window.addEventListener('resize',function(){resize();redrawCursors();});

  function colorFor(id){
    var h=0;for(var i=0;i<id.length;i++){h=(h*31+id.charCodeAt(i))>>>0;}
    return 'hsl('+(h%360)+',65%,60%)';
  }
  function drawDot(nx,ny,color){
    dctx.fillStyle=color;
    dctx.beginPath();
    dctx.arc(nx*dotCv.width,ny*dotCv.height,7,0,Math.PI*2);
    dctx.fill();
  }
  function redrawCursors(){
    cctx.clearRect(0,0,curCv.width,curCv.height);
    for(var id in peers){
      var p=peers[id];
      if(p.x==null)continue;
      var x=p.x*curCv.width,y=p.y*curCv.height;
      cctx.fillStyle=p.color;
      cctx.beginPath();cctx.arc(x,y,5,0,Math.PI*2);cctx.fill();
      cctx.font='11px system-ui';cctx.fillText(p.name||'',x+9,y+4);
    }
  }

  function norm(ev){
    var r=area.getBoundingClientRect();
    return{x:Math.min(1,Math.max(0,(ev.clientX-r.left)/r.width)),
           y:Math.min(1,Math.max(0,(ev.clientY-r.top)/r.height))};
  }
  area.addEventListener('pointermove',function(ev){
    var now=Date.now();
    if(now-lastSent<66)return;
    lastSent=now;
    var p=norm(ev);
    PlayMore.send({t:'cur',x:p.x,y:p.y});
  });
  area.addEventListener('pointerdown',function(ev){
    var p=norm(ev);
    drawDot(p.x,p.y,myColor);
    PlayMore.send({t:'dot',x:p.x,y:p.y});
    log('ok','sent dot to peers');
  });

  // ── Peer list rendering ────────────────────────────────────
  function renderPeers(){
    var el=document.getElementById('peers');
    var ids=Object.keys(peers);
    if(ids.length===0){
      el.innerHTML='<div style="color:#5a7a99;font-size:12px;padding:8px;">No peers connected</div>';
      return;
    }
    el.innerHTML=ids.map(function(id){
      var p=peers[id];
      var t=PlayMore.transport(id);
      var s=PlayMore.stats();
      var ps=s.peers[id]||{sent:0,received:0};
      return '<div class="peer"><div class="name"><span class="dot '+(t==='webrtc'?'on':'off')+'"></span>'+p.name+
        '<span class="tport '+t+'">'+t.toUpperCase()+'</span></div>'+
        '<div class="bw">sent '+ps.sent+'B | recv '+ps.received+'B</div></div>';
    }).join('');
  }

  // ── Lobby menu (game-managed) ──────────────────────────────
  // onReady now fires pre-lobby (empty code) — the game shows its own menu
  // and drives the lobby via createLobby/quickPlay/joinLobby; onLaunch fires
  // when play begins. See /docs for the protocol.
  var lobby=null, matchmaking=false;
  function esc(s){ return String(s==null?'':s).replace(/[&<>"]/g,function(c){return {'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]; }); }
  function showMenu(show){ document.getElementById('menu').classList.toggle('hidden', !show); }
  function renderMenu(){
    var body=document.getElementById('panelbody');
    if(matchmaking){
      body.innerHTML='<h1>Finding players…</h1><p id="mmstat">Searching…</p><button class="sec" id="mm-cancel">Cancel</button>';
      document.getElementById('mm-cancel').onclick=function(){ matchmaking=false; PlayMore.cancelMatchmake(); renderMenu(); };
      return;
    }
    if(!lobby){
      body.innerHTML='<h1>MP Test Arena</h1><p>Diagnostic lobby — match, create, or join.</p>'+
        '<button id="mm">Quick Play</button>'+
        '<button class="sec" id="create">Create Lobby</button>'+
        '<div id="joinrow"><input id="jcode" placeholder="CODE" maxlength="6" autocomplete="off"><button class="sec" id="join">Join</button></div>';
      document.getElementById('mm').onclick=function(){ matchmaking=true; PlayMore.quickPlay(2); renderMenu(); };
      document.getElementById('create').onclick=function(){ PlayMore.createLobby(); };
      document.getElementById('join').onclick=function(){ var c=document.getElementById('jcode').value; if(c) PlayMore.joinLobby(c); };
      return;
    }
    var me=null, allReady=true;
    (lobby.players||[]).forEach(function(p){ if(p.id===myId)me=p; if(!p.host&&!p.ready)allReady=false; });
    var isHost=!!(me&&me.host);
    var h='<h1>Lobby</h1><div class="codebig">'+esc(lobby.code)+'</div><p>Share this code with a tester</p><ul id="plist">';
    (lobby.players||[]).forEach(function(p){
      h+='<li><span>'+esc(p.username)+'</span><span>'+(p.host?'HOST':(p.ready?'✔ ready':'…'))+'</span></li>';
    });
    h+='</ul>';
    h+=isHost ? '<button id="start"'+(allReady?'':' disabled')+'>Start Game</button>'
             : '<button id="ready">'+((me&&me.ready)?'Not Ready':'Ready Up')+'</button>';
    h+='<button class="sec" id="leave">Leave</button>';
    body.innerHTML=h;
    if(isHost){ document.getElementById('start').onclick=function(){ PlayMore.startGame(); }; }
    else { document.getElementById('ready').onclick=function(){ PlayMore.readyUp(!(me&&me.ready)); }; }
    document.getElementById('leave').onclick=function(){ PlayMore.leaveLobby(); lobby=null; renderMenu(); };
  }

  function beginPlay(l){
    lobby=l; matchmaking=false; showMenu(false);
    document.getElementById('code').textContent=l.code||'--';
    document.getElementById('dot-mp').className='dot on';
    log('ok','Lobby launched: '+l.code+' | players: '+(l.players?l.players.length:0));
    syncPeers(l.players);
    renderPeers();
  }

  // ── PlayMore wiring ────────────────────────────────────────
  PlayMore.onReady(function(ctx){
    resize();
    myId=ctx.you?ctx.you.id:null;
    myColor=myId?colorFor(myId):'#3c8ce0';
    document.getElementById('me-label').textContent='you: '+(ctx.you?ctx.you.username:'?');
    if(ctx.sessionToken){
      log('ok','Session token received (pm_gs_) — pre-lobby');
      document.getElementById('session-status').textContent='pm_gs_ token active';
    }
    setInterval(renderPeers,2000);
    showMenu(true); renderMenu();
  });

  PlayMore.onLobbyState(function(l){
    lobby=l; matchmaking=false; renderMenu();
    log('ev','Lobby state: '+l.code+' | players: '+(l.players?l.players.length:0));
  });
  PlayMore.onMatchmaking(function(m){
    var el=document.getElementById('mmstat'); if(el) el.textContent='Searching… '+m.queueSize+'/'+m.targetCount;
  });
  PlayMore.onLaunch(function(l){ beginPlay(l); });

  PlayMore.onPlayers(function(players){
    syncPeers(players);
    renderPeers();
    log('ev','Players updated: '+(players?players.length:0));
  });

  PlayMore.onMessage(function(from,d){
    if(!d||from===myId)return;
    if(!peers[from])peers[from]={x:null,y:null,color:colorFor(from),name:''};
    if(d.t==='cur'){peers[from].x=d.x;peers[from].y=d.y;redrawCursors();}
    else if(d.t==='dot'){drawDot(d.x,d.y,peers[from].color);}
    else if(d.t==='ping'){
      PlayMore.send({t:'pong',n:d.n},from);
      log('ev','ping from '+peers[from].name+' (#'+d.n+')');
    }
    else if(d.t==='pong'){
      log('ok','pong from '+(peers[from]?peers[from].name:from)+' (#'+d.n+')');
    }
    else if(d.t==='bcast'){
      log('ok','broadcast from '+(peers[from]?peers[from].name:from)+': '+d.msg);
    }
    else if(d.t==='uni'){
      log('ok','unicast from '+(peers[from]?peers[from].name:from)+': '+d.msg);
    }
  });

  PlayMore.onTransportChange(function(peerId,transport){
    var name=peers[peerId]?peers[peerId].name:peerId;
    log(transport==='webrtc'?'ok':'warn',name+' transport -> '+transport.toUpperCase());
    renderPeers();
  });

  PlayMore.onClosed(function(){
    log('err','Lobby closed');
    peers={};redrawCursors();renderPeers();
    document.getElementById('dot-mp').className='dot off';
    lobby=null; matchmaking=false; showMenu(true); renderMenu();
  });

  function syncPeers(players){
    var live={};
    (players||[]).forEach(function(p){
      if(p.id===myId)return;
      live[p.id]=true;
      if(!peers[p.id])peers[p.id]={x:null,y:null,color:colorFor(p.id),name:p.username||''};
      else peers[p.id].name=p.username||peers[p.id].name;
    });
    for(var id in peers){if(!live[id])delete peers[id];}
    redrawCursors();
  }

  // ── Controls ───────────────────────────────────────────────
  var pingN=0;
  document.getElementById('btn-bcast').onclick=function(){
    pingN++;
    PlayMore.send({t:'bcast',msg:'hello #'+pingN});
    log('ev','broadcast sent #'+pingN);
  };
  document.getElementById('btn-uni').onclick=function(){
    var ids=Object.keys(peers);
    if(ids.length===0){log('warn','no peers to unicast to');return;}
    pingN++;
    PlayMore.send({t:'uni',msg:'unicast #'+pingN},ids[0]);
    log('ev','unicast to '+(peers[ids[0]].name)+' #'+pingN);
  };
  document.getElementById('btn-stats').onclick=function(){
    var s=PlayMore.stats();
    statsEl.innerHTML='total: sent '+s.sent+'B | recv '+s.received+'B | peers: '+Object.keys(s.peers).length;
    log('ev','stats: '+JSON.stringify(s));
  };

  resize();
})();
</script>
</body>
</html>`
}
