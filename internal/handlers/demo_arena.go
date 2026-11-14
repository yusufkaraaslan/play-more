package handlers

// generateBouncyBallArenaGame is an owner-authoritative real-time sync example.
func generateBouncyBallArenaGame() string {
	return `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Bouncy Ball Arena</title><style>
*{box-sizing:border-box}body{margin:0;background:#101c29;color:#eef6ff;font:16px system-ui;text-align:center}
main{max-width:820px;margin:auto;padding:16px}h1{margin:8px}p{color:#bbd2e8;line-height:1.5}
button,input{font:inherit;padding:10px;border:1px solid #527995;border-radius:8px;background:#203d52;color:inherit;margin:4px}button{cursor:pointer}button:disabled{opacity:.5}input{width:140px;text-transform:uppercase}
canvas{display:block;width:min(100%,65vh);height:auto;margin:auto;aspect-ratio:1;background:#152b3c;border:2px solid #527995;border-radius:16px;touch-action:none;}
#notice{min-height:24px;color:#ffd296}#code{font-size:26px;letter-spacing:4px}[hidden]{display:none!important}
</style></head><body><main><h1>Bouncy Ball Arena</h1><p id="status" role="status">Connecting to PlayMore…</p>
<section id="menu" hidden><button id="quick">Quick Play (2)</button><button id="create">Create Lobby (up to 4)</button><div><input id="joincode" aria-label="Lobby code" placeholder="CODE" maxlength="6"><button id="join">Join</button></div></section>
<section id="waiting" hidden><p id="code"></p><p id="roster"></p><button id="ready">Ready Up</button><button id="start">Start Arena</button></section>
<section id="search" hidden><p>Finding an opponent…</p><button id="cancel">Cancel</button></section>
<section id="game" hidden><canvas id="arena" width="600" height="600" tabindex="0" aria-label="Arena: steer with arrow keys, WASD, or drag"></canvas><p>Arrow keys / WASD to steer. Drag or touch to steer toward your pointer.<br>Walls and other balls bounce you away. Your ball has a white outline.</p></section>
<button id="leave" hidden>Leave Lobby</button><p id="notice" role="alert"></p></main>
<script src="/playmore-mp.js"></script><script>
(function(){
  'use strict';
  var me=null,lobby=null,active=false,searching=false,balls=Object.create(null),keys={},target=null;
  var local=null,seq=0,last=0,lastSent=-Infinity,radius=.035;
  function el(id){return document.getElementById(id);}
  var canvas=el('arena'),ctx=canvas.getContext('2d');
  function roster(){return lobby?lobby.players.filter(function(p){return !p.spectator;}):[];}
  function mine(){return roster().find(function(p){return p.id===me;});}
  function clamp(v){return Math.max(radius,Math.min(1-radius,v));}
  function color(id){var h=0;for(var c of id)h=(h*31+c.charCodeAt(0))>>>0;return 'hsl('+((h*137)%360)+',80%,65%)';}
  function render(){
    el('menu').hidden=!me||!!lobby||searching;el('waiting').hidden=!lobby||active;el('search').hidden=!searching;
    el('game').hidden=!active;el('leave').hidden=!lobby;
    if(!lobby){el('status').textContent=me?'Bounce with friends or find an opponent.':'Connecting to PlayMore…';return;}
    el('code').textContent=lobby.code;
    el('roster').textContent=roster().map(function(p){return p.username+(p.host?' (host)':p.ready?' (ready)':' (not ready)');}).join(' · ');
    var p=mine();el('ready').hidden=!!(p&&p.host);el('ready').textContent=p&&p.ready?'Not Ready':'Ready Up';
    el('start').hidden=!(p&&p.host);el('start').disabled=roster().length<2||roster().some(function(p){return !p.host&&!p.ready;});
    el('status').textContent=active?roster().length+' players · you are '+(p?p.username:'spectating'):'Share the code, ready up, then start.';
  }
  function reconcile(){
    var ids=roster().map(function(p){return p.id;});
    for(var id of Object.keys(balls))if(!ids.includes(id))delete balls[id];
    if(!ids.includes(me))local=null;
    render();
  }
  function closeMatch(){lobby=null;active=false;searching=false;balls=Object.create(null);local=null;keys={};target=null;render();}
  el('create').onclick=function(){PlayMore.createLobby({maxPlayers:4});};
  el('join').onclick=function(){var code=el('joincode').value.trim().toUpperCase();if(code)PlayMore.joinLobby(code);};
  el('quick').onclick=function(){searching=true;PlayMore.quickPlay(2);render();};
  el('cancel').onclick=function(){searching=false;PlayMore.cancelMatchmake();render();};
  el('ready').onclick=function(){PlayMore.readyUp(!(mine()&&mine().ready));};
  el('start').onclick=function(){PlayMore.startGame();};
  el('leave').onclick=function(){PlayMore.leaveLobby();closeMatch();};
  var controls=['arrowup','arrowdown','arrowleft','arrowright','w','a','s','d'];
  window.addEventListener('keydown',function(e){var k=e.key.toLowerCase();if(active&&controls.includes(k)){e.preventDefault();keys[k]=true;target=null;}});
  window.addEventListener('keyup',function(e){delete keys[e.key.toLowerCase()];});
  window.addEventListener('blur',function(){keys={};target=null;});
  function point(e){var r=canvas.getBoundingClientRect();target={x:clamp((e.clientX-r.left)/r.width),y:clamp((e.clientY-r.top)/r.height)};}
  canvas.addEventListener('pointerdown',function(e){if(!active)return;canvas.focus();canvas.setPointerCapture(e.pointerId);point(e);});
  canvas.addEventListener('pointermove',function(e){if(e.buttons)point(e);});
  canvas.addEventListener('pointerup',function(){target=null;});canvas.addEventListener('pointercancel',function(){target=null;});
  PlayMore.onReady(function(c){me=c.you.id;render();});
  PlayMore.onLobbyState(function(l){lobby=l;searching=false;reconcile();});
  PlayMore.onLaunch(function(l){
    lobby=l;active=true;searching=false;balls=Object.create(null);keys={};target=null;last=0;lastSent=-Infinity;
    var i=roster().findIndex(function(p){return p.id===me;});
    local=i<0?null:{x:.25+(i%2)*.5,y:.25+Math.floor(i/2)*.5,vx:.18,vy:.14};
    render();canvas.focus();
  });
  PlayMore.onMessage(function(from,d){
    if(!active||from===me||!d||d.t!=='ball'||!roster().some(function(p){return p.id===from;}))return;
    if(!Number.isSafeInteger(d.seq)||d.seq<0||![d.x,d.y,d.vx,d.vy].every(Number.isFinite))return;
    if(d.x<radius||d.x>1-radius||d.y<radius||d.y>1-radius||Math.abs(d.vx)>1||Math.abs(d.vy)>1)return;
    if(balls[from]&&d.seq<=balls[from].seq)return;
    balls[from]={x:d.x,y:d.y,vx:d.vx,vy:d.vy,seq:d.seq,seen:performance.now()};
  });
  PlayMore.onClosed(closeMatch);
  PlayMore.onError(function(error){searching=false;el('notice').textContent=String(error);render();});
  function tick(now){
    var dt=last?Math.min((now-last)/1000,.05):0;last=now;
    if(active){
      if(local){
        var dx=(keys.arrowright||keys.d?1:0)-(keys.arrowleft||keys.a?1:0),dy=(keys.arrowdown||keys.s?1:0)-(keys.arrowup||keys.w?1:0);
        if(target){dx=target.x-local.x;dy=target.y-local.y;}
        var len=Math.hypot(dx,dy);if(len>.01){local.vx=dx/len*.32;local.vy=dy/len*.32;}
        local.x+=local.vx*dt;local.y+=local.vy*dt;
        for(var axis of ['x','y'])if(local[axis]<radius||local[axis]>1-radius){local[axis]=clamp(local[axis]);local['v'+axis]*=-1;}
        // Each owner resolves only its own ball. This is casual physics, not lockstep.
        for(var id of Object.keys(balls)){
          var b=balls[id];if(now-b.seen>3000)continue;
          var x=local.x-b.x,y=local.y-b.y,dist=Math.hypot(x,y);
          if(dist<radius*2){var nx=dist?x/dist:1,ny=dist?y/dist:0;
            local.x=clamp(b.x+nx*radius*2);local.y=clamp(b.y+ny*radius*2);
            var dot=local.vx*nx+local.vy*ny;if(dot<0){local.vx-=2*dot*nx;local.vy-=2*dot*ny;}}
        }
        if(now-lastSent>=Math.max(50,PlayMore.recommendedThrottle())){
          lastSent=now;seq=Math.max(seq+1,Date.now());
          PlayMore.sendUnreliable({t:'ball',seq:seq,x:local.x,y:local.y,vx:local.vx,vy:local.vy});
        }
      }
      ctx.clearRect(0,0,600,600);
      roster().forEach(function(p){
        var b=p.id===me?local:balls[p.id];if(!b)return;
        var age=p.id===me?0:Math.max(0,now-b.seen),ahead=Math.min(age/1000,.1);
        var x=clamp(b.x+b.vx*ahead)*600,y=clamp(b.y+b.vy*ahead)*600;
        ctx.globalAlpha=age>3000?.3:1;ctx.fillStyle=color(p.id);ctx.beginPath();ctx.arc(x,y,radius*600,0,Math.PI*2);ctx.fill();
        if(p.id===me){ctx.strokeStyle='white';ctx.lineWidth=3;ctx.stroke();}
        ctx.font='14px system-ui';ctx.textAlign='center';ctx.fillStyle='white';ctx.fillText(p.username,x,y-radius*600-10);
      });ctx.globalAlpha=1;
    }
    requestAnimationFrame(tick);
  }
  render();requestAnimationFrame(tick);
})();
</script></body></html>`
}
