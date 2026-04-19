package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type apiEndpoint struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Desc   string `json:"desc"`
	Auth   bool   `json:"auth"`
	Rate   string `json:"rate,omitempty"`
}

type apiGroup struct {
	Name      string        `json:"name"`
	Endpoints []apiEndpoint `json:"endpoints"`
}

func APIDocs(c *gin.Context) {
	accept := c.GetHeader("Accept")
	if accept == "application/json" {
		c.JSON(http.StatusOK, gin.H{"groups": apiGroups()})
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(docsHTML()))
}

func apiGroups() []apiGroup {
	return []apiGroup{
		{Name: "Auth", Endpoints: []apiEndpoint{
			{Method: "POST", Path: "/api/auth/register", Desc: "Create a new account", Rate: "5/hour"},
			{Method: "POST", Path: "/api/auth/login", Desc: "Login with email and password", Rate: "10/5min"},
			{Method: "POST", Path: "/api/auth/logout", Desc: "Logout (clear session)"},
			{Method: "GET", Path: "/api/auth/me", Desc: "Get current user + stats", Auth: true},
			{Method: "GET", Path: "/api/auth/verify/:token", Desc: "Verify email address"},
			{Method: "POST", Path: "/api/auth/forgot-password", Desc: "Request password reset email", Rate: "5/hour"},
			{Method: "POST", Path: "/api/auth/reset-password", Desc: "Reset password with token", Rate: "10/hour"},
		}},
		{Name: "Games", Endpoints: []apiEndpoint{
			{Method: "GET", Path: "/api/games", Desc: "List games (query: genre, search, sort, page, limit)"},
			{Method: "GET", Path: "/api/games/:id", Desc: "Get game details (also accepts slug)"},
			{Method: "POST", Path: "/api/games", Desc: "Upload a new game (multipart form)", Auth: true, Rate: "10/hour"},
			{Method: "PUT", Path: "/api/games/:id", Desc: "Update game (all fields)", Auth: true},
			{Method: "DELETE", Path: "/api/games/:id", Desc: "Delete a game", Auth: true},
			{Method: "POST", Path: "/api/games/:id/reupload", Desc: "Replace game files", Auth: true},
			{Method: "PUT", Path: "/api/games/:id/visibility", Desc: "Publish/unpublish game", Auth: true},
			{Method: "POST", Path: "/api/games/:id/screenshots", Desc: "Add screenshots to game", Auth: true},
			{Method: "DELETE", Path: "/api/games/:id/screenshots/:index", Desc: "Remove a screenshot", Auth: true},
		}},
		{Name: "Reviews", Endpoints: []apiEndpoint{
			{Method: "GET", Path: "/api/games/:id/reviews", Desc: "List reviews for a game"},
			{Method: "POST", Path: "/api/games/:id/reviews", Desc: "Submit a review (rating 1-5 + text)", Auth: true, Rate: "20/hour"},
			{Method: "DELETE", Path: "/api/reviews/:id", Desc: "Delete your review", Auth: true},
		}},
		{Name: "Library", Endpoints: []apiEndpoint{
			{Method: "GET", Path: "/api/library", Desc: "Get your library", Auth: true},
			{Method: "POST", Path: "/api/library/:game_id", Desc: "Add game to library", Auth: true},
			{Method: "DELETE", Path: "/api/library/:game_id", Desc: "Remove from library", Auth: true},
		}},
		{Name: "Wishlist", Endpoints: []apiEndpoint{
			{Method: "GET", Path: "/api/wishlist", Desc: "Get your wishlist", Auth: true},
			{Method: "POST", Path: "/api/wishlist/:game_id", Desc: "Add to wishlist", Auth: true},
			{Method: "DELETE", Path: "/api/wishlist/:game_id", Desc: "Remove from wishlist", Auth: true},
		}},
		{Name: "Profile", Endpoints: []apiEndpoint{
			{Method: "GET", Path: "/api/profile/:username", Desc: "Get user profile"},
			{Method: "PUT", Path: "/api/profile", Desc: "Update your profile", Auth: true, Rate: "10/5min"},
			{Method: "GET", Path: "/api/activity", Desc: "Get your activity", Auth: true},
			{Method: "POST", Path: "/api/playtime", Desc: "Record play time", Auth: true},
		}},
		{Name: "Developer", Endpoints: []apiEndpoint{
			{Method: "GET", Path: "/api/developer/:username", Desc: "Get developer page"},
			{Method: "PUT", Path: "/api/developer", Desc: "Update developer page", Auth: true, Rate: "10/5min"},
			{Method: "GET", Path: "/api/developer/:username/games", Desc: "List developer's games"},
		}},
		{Name: "Social", Endpoints: []apiEndpoint{
			{Method: "POST", Path: "/api/follow/:username", Desc: "Follow a developer", Auth: true, Rate: "30/hour"},
			{Method: "DELETE", Path: "/api/follow/:username", Desc: "Unfollow", Auth: true, Rate: "30/hour"},
			{Method: "GET", Path: "/api/following", Desc: "List who you follow", Auth: true},
			{Method: "GET", Path: "/api/followers/:username", Desc: "Get follower count"},
		}},
		{Name: "Collections", Endpoints: []apiEndpoint{
			{Method: "GET", Path: "/api/collections", Desc: "List your collections", Auth: true},
			{Method: "GET", Path: "/api/collections/public", Desc: "Browse public lists"},
			{Method: "GET", Path: "/api/collections/:id", Desc: "Get collection detail + games"},
			{Method: "POST", Path: "/api/collections", Desc: "Create collection", Auth: true},
			{Method: "PUT", Path: "/api/collections/:id", Desc: "Update collection", Auth: true},
			{Method: "DELETE", Path: "/api/collections/:id", Desc: "Delete collection", Auth: true},
			{Method: "POST", Path: "/api/collections/:id/games", Desc: "Add game to collection", Auth: true},
			{Method: "DELETE", Path: "/api/collections/:id/games/:game_id", Desc: "Remove from collection", Auth: true},
		}},
		{Name: "Feed & Devlogs", Endpoints: []apiEndpoint{
			{Method: "GET", Path: "/api/feed", Desc: "Get activity feed", Auth: true},
			{Method: "GET", Path: "/api/games/:id/devlogs", Desc: "List devlogs for game"},
			{Method: "POST", Path: "/api/games/:id/devlogs", Desc: "Create devlog", Auth: true},
			{Method: "DELETE", Path: "/api/devlogs/:id", Desc: "Delete devlog", Auth: true},
			{Method: "GET", Path: "/api/devlogs/:id/comments", Desc: "List comments"},
			{Method: "POST", Path: "/api/devlogs/:id/comments", Desc: "Add comment", Auth: true},
			{Method: "DELETE", Path: "/api/comments/:id", Desc: "Delete comment", Auth: true},
		}},
		{Name: "Other", Endpoints: []apiEndpoint{
			{Method: "GET", Path: "/api/achievements/:username", Desc: "Get user achievements"},
			{Method: "POST", Path: "/api/achievements/check", Desc: "Check/unlock achievements", Auth: true},
			{Method: "GET", Path: "/api/notifications", Desc: "Get notifications", Auth: true},
			{Method: "POST", Path: "/api/notifications/read", Desc: "Mark notifications read", Auth: true},
			{Method: "POST", Path: "/api/games/:id/view", Desc: "Track game view"},
			{Method: "POST", Path: "/api/upload/image", Desc: "Upload an image", Auth: true},
			{Method: "POST", Path: "/api/seed", Desc: "Seed demo data"},
			{Method: "GET", Path: "/avatar/:username", Desc: "Get generated avatar image"},
		}},
		{Name: "API Keys", Endpoints: []apiEndpoint{
			{Method: "GET", Path: "/api/api-keys", Desc: "List your API keys", Auth: true},
			{Method: "POST", Path: "/api/api-keys", Desc: "Generate new API key (session only)", Auth: true, Rate: "10/hour"},
			{Method: "DELETE", Path: "/api/api-keys/:id", Desc: "Revoke an API key (session only)", Auth: true},
			{Method: "GET", Path: "/deploy.sh", Desc: "Download CLI deploy script"},
		}},
		{Name: "Admin", Endpoints: []apiEndpoint{
			{Method: "GET", Path: "/api/admin/stats", Desc: "Site statistics", Auth: true},
			{Method: "GET", Path: "/api/admin/users", Desc: "List all users", Auth: true},
			{Method: "DELETE", Path: "/api/admin/users/:id", Desc: "Delete user", Auth: true, Rate: "10/hour"},
			{Method: "GET", Path: "/api/admin/games", Desc: "List all games", Auth: true},
			{Method: "DELETE", Path: "/api/admin/games/:id", Desc: "Delete game", Auth: true, Rate: "10/hour"},
			{Method: "PUT", Path: "/api/admin/games/:id/publish", Desc: "Toggle publish status", Auth: true},
			{Method: "GET", Path: "/api/admin/analytics", Desc: "Site analytics", Auth: true},
		}},
	}
}

func docsHTML() string {
	groups := apiGroups()
	html := `<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1.0"><title>PlayMore API Docs</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}body{font-family:'Segoe UI',sans-serif;background:#1b2838;color:#c7d5e0;padding:40px 20px;max-width:900px;margin:0 auto}
h1{color:#66c0f4;margin-bottom:8px}h2{color:#66c0f4;font-size:18px;margin:30px 0 12px;padding-bottom:8px;border-bottom:1px solid rgba(255,255,255,0.1)}
.ep{display:grid;grid-template-columns:70px 1fr auto;gap:10px;padding:10px 12px;border-radius:4px;margin-bottom:4px;align-items:center;background:rgba(0,0,0,0.2)}
.ep:hover{background:rgba(0,0,0,0.35)}.method{font-weight:700;font-size:13px;text-align:center;padding:3px 8px;border-radius:3px;font-family:monospace}
.GET{background:#2a475e;color:#66c0f4}.POST{background:#4c6b22;color:#a1cd44}.PUT{background:#5a4a0a;color:#d2a960}.DELETE{background:#4a1a1a;color:#c15757}
.path{font-family:monospace;font-size:13px;color:#e0e0e0}.desc{font-size:13px;color:#8f98a0}.badge{font-size:10px;padding:2px 6px;border-radius:3px;margin-left:6px}
.auth-badge{background:rgba(102,192,244,0.15);color:#66c0f4}.rate-badge{background:rgba(210,169,96,0.15);color:#d2a960}
.try-btn{background:#66c0f4;color:#1b2838;border:none;padding:4px 10px;border-radius:3px;cursor:pointer;font-size:11px;font-weight:600}
.try-btn:hover{background:#417a9b}.try-panel{display:none;grid-column:1/-1;background:rgba(0,0,0,0.3);padding:12px;border-radius:4px;margin-top:6px}
.try-panel.active{display:block}input,textarea{background:rgba(0,0,0,0.3);border:1px solid rgba(255,255,255,0.1);color:#c7d5e0;padding:6px 10px;border-radius:3px;width:100%;font-family:monospace;font-size:12px;margin:4px 0}
pre{background:rgba(0,0,0,0.4);padding:10px;border-radius:4px;overflow-x:auto;font-size:12px;margin-top:8px;color:#a1cd44;white-space:pre-wrap}
a{color:#66c0f4;text-decoration:none}.subtitle{color:#8f98a0;margin-bottom:30px}
</style></head><body>
<h1>PlayMore API</h1><p class="subtitle">Interactive API documentation &mdash; <a href="/">Back to app</a></p>`

	for _, g := range groups {
		html += `<h2>` + g.Name + `</h2>`
		for i, ep := range g.Endpoints {
			id := g.Name + "_" + string(rune('0'+i))
			html += `<div class="ep"><span class="method ` + ep.Method + `">` + ep.Method + `</span><div><span class="path">` + ep.Path + `</span>`
			if ep.Auth {
				html += `<span class="badge auth-badge">Auth</span>`
			}
			if ep.Rate != "" {
				html += `<span class="badge rate-badge">` + ep.Rate + `</span>`
			}
			html += `<div class="desc">` + ep.Desc + `</div></div>`
			if ep.Method == "GET" || ep.Method == "POST" || ep.Method == "PUT" {
				html += `<button class="try-btn" onclick="toggleTry('` + id + `')">Try</button>`
			}
			html += `</div>`
			if ep.Method == "GET" || ep.Method == "POST" || ep.Method == "PUT" {
				html += `<div class="try-panel" id="try-` + id + `">`
				if ep.Method != "GET" {
					html += `<textarea rows="3" id="body-` + id + `" placeholder='{"key": "value"}'></textarea>`
				}
				html += `<button class="try-btn" style="margin-top:6px;" onclick="tryAPI('` + id + `','` + ep.Method + `','` + ep.Path + `')">Send</button>`
				html += `<pre id="res-` + id + `" style="display:none;"></pre></div>`
			}
		}
	}

	html += `<script>
function toggleTry(id){document.getElementById('try-'+id).classList.toggle('active')}
async function tryAPI(id,method,path){
  const resEl=document.getElementById('res-'+id);resEl.style.display='block';resEl.textContent='Loading...';
  const opts={method,credentials:'same-origin',headers:{}};
  if(method!=='GET'){const b=document.getElementById('body-'+id);if(b&&b.value.trim()){opts.body=b.value;opts.headers['Content-Type']='application/json';}}
  try{const r=await fetch(path,opts);const t=await r.text();try{resEl.textContent=JSON.stringify(JSON.parse(t),null,2)}catch{resEl.textContent=t}}
  catch(e){resEl.textContent='Error: '+e.message}}
</script></body></html>`
	return html
}
