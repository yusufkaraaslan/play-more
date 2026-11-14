const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const html = fs.readFileSync('internal/handlers/demo_arena.go', 'utf8');
const source = html.match(/<script>\n([\s\S]*?)<\/script>/)[1];
const lobby = { code: 'ABCDEF', players: [
  { id: 'a', username: 'Alice', host: true }, { id: 'b', username: 'Bob', ready: true }
] };
function client(id) {
  const elements = new Map(), callbacks = {}, sent = [], commands = [], events = {}, arcs = [];
  let nextFrame, now = 0;
  const context = { clearRect() { arcs.length = 0; }, beginPath() {}, arc(x,y) { arcs.push({x,y}); },
    fill() {}, stroke() {}, fillText() {} };
  function element() {
    return { hidden: false, textContent: '', value: '', events: {},
      addEventListener(name, fn) { this.events[name] = fn; },
      getContext() { return context; }, focus() {}, setPointerCapture() {},
      getBoundingClientRect() { return { left:0, top:0, width:600, height:600 }; } };
  }
  const document = { getElementById(key) {
    if (!elements.has(key)) elements.set(key, element());
    return elements.get(key);
  } };
  const api = { recommendedThrottle: () => 66,
    sendUnreliable(data) { sent.push(structuredClone(data)); } };
  for (const event of ['Ready', 'LobbyState', 'Launch', 'Message', 'Closed', 'Error']) {
    api['on'+event] = fn => { callbacks[event] = fn; };
  }
  for (const name of ['createLobby','joinLobby','quickPlay','cancelMatchmake','readyUp','startGame','leaveLobby']) {
    api[name] = (...args) => commands.push({ name, args });
  }
  vm.runInNewContext(source, { document, PlayMore: api, performance: { now: () => now },
    window: { addEventListener: (name,fn) => { events[name] = fn; } },
    requestAnimationFrame: fn => { nextFrame = fn; } });
  callbacks.Ready({you:{id}});
  return { callbacks, sent, commands, events, arcs, el: document.getElementById,
    tick(t) { now=t; nextFrame(t); } };
}
function packet(seq, extras={}) { return {t:'ball',seq,x:.7,y:.7,vx:.1,vy:.1,...extras}; }

test('arena sends throttled absolute snapshots with increasing sequence numbers and bounded physics', () => {
  const c=client('a');c.callbacks.Launch(structuredClone(lobby));
  c.tick(100);c.tick(120);c.tick(165);
  assert.equal(c.sent.length,1);
  c.tick(166);assert.equal(c.sent.length,2);
  assert.ok(c.sent[1].seq>c.sent[0].seq);
  c.events.keydown({key:'ArrowRight',preventDefault(){}});
  for(let t=250;t<15000;t+=100)c.tick(t);
  for(const d of c.sent) {
    assert.ok(d.x>=.035 && d.x<=.965 && d.y>=.035 && d.y<=.965);
    assert.ok(Math.abs(d.vx)<=1 && Math.abs(d.vy)<=1);
  }
  c.el('leave').onclick();const n=c.sent.length;c.tick(16000);assert.equal(c.sent.length,n);
  assert.equal(c.el('menu').hidden,false);
});

test('arena ignores stale, malformed and nonmember snapshots and removes departed peers', () => {
  const c=client('a');c.callbacks.Launch(structuredClone(lobby));c.tick(100);
  for(const [from,data] of [
    ['intruder',packet(1)],['b',packet(1,{x:NaN})],['b',packet(1,{y:2})],
    ['b',packet(1,{vx:Infinity})],['b',packet(1,{seq:1.5})],['b',packet(1,{vy:2})]
  ]) c.callbacks.Message(from,data);
  c.tick(120);assert.equal(c.arcs.length,1);
  c.callbacks.Message('b',packet(3));c.tick(130);assert.equal(c.arcs.length,2);
  const x=c.arcs[1].x;
  c.callbacks.Message('b',packet(2,{x:.4}));c.tick(130);assert.equal(c.arcs[1].x,x);
  c.callbacks.LobbyState({...lobby,players:[lobby.players[0]]});c.tick(140);
  assert.equal(c.arcs.length,1);
  c.callbacks.Closed();assert.equal(c.el('game').hidden,true);
});

test('arena collision pushes only the local ball away and pointer input steers it', () => {
  const c=client('a');c.callbacks.Launch(structuredClone(lobby));c.tick(100);
  c.callbacks.Message('b',packet(1,{x:.28,y:.25,vx:0,vy:0}));c.tick(170);
  assert.ok(c.sent.at(-1).x<.25,'overlap should push the local ball left');
  c.callbacks.LobbyState({...lobby,players:[lobby.players[0]]});
  c.el('arena').events.pointerdown({clientX:550,clientY:550,pointerId:1});c.tick(240);
  assert.ok(c.sent.at(-1).vx>0 && c.sent.at(-1).vy>0);
  c.events.blur();
  c.el('create').onclick();assert.equal(c.commands.at(-1).args[0].maxPlayers,4);
});
