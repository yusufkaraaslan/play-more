const { test } = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const source = fs.readFileSync('internal/handlers/playmore-mp.js', 'utf8');

function client() {
  const sent = [], sockets = [], timers = new Map(), listeners = {}, requests = [];
  let nextTimer = 0;
  const parent = { postMessage(message) { sent.push(message); } };
  class Socket {
    constructor(url) {
      this.url = url; this.readyState = 0; this.sent = []; sockets.push(this);
      queueMicrotask(() => { this.readyState = 1; this.onopen?.(); });
    }
    send(data) { this.sent.push(JSON.parse(data)); }
    close() { this.readyState = 3; queueMicrotask(() => this.onclose?.()); }
    receive(message) { this.onmessage({ data: JSON.stringify(message) }); }
  }
  const window = { parent, addEventListener(name, fn) { listeners[name] = fn; } };
  vm.runInNewContext(source, { window, URL, WebSocket: Socket, console,
    setTimeout(fn, ms) { const id = ++nextTimer; timers.set(id, { fn, ms }); return id; },
    clearTimeout(id) { timers.delete(id); }, setInterval() { return 1; }, clearInterval() {},
    fetch: async (url, opts) => {
      requests.push({ url, opts });
      return { ok: true, json: async () => ({ you: { id: 'player', username: 'P' }, game_id: 'game', iceServers: [] }) };
    },
  });
  return { api: window.PlayMore, sent, sockets, timers, requests, parent,
    message: data => listeners.message({ source: parent, origin: 'https://store.example', data }) };
}

test('embedded transport still receives init and forwards game commands', () => {
  const c = client();
  c.message({ playmore: 'init', game_id: 'game', you: { id: 'p' }, session_token: 'pm_gs_token' });
  c.api.createLobby({ maxPlayers: 4 });
  assert.equal(c.sent.at(-1).playmore, 'create_lobby');
  assert.equal(c.sent.at(-1).max_players, 4);
  assert.equal(c.api.sessionToken(), 'pm_gs_token');
});

test('standalone transport authenticates, scopes commands, refreshes and disconnects', async () => {
  const c = client(); let count = 0;
  await c.api.connect({ server: 'https://mp.example', gameId: 'game', getToken: () => 'pm_gs_' + (++count) });
  const ws = c.sockets[0];
  assert.equal(ws.url, 'wss://mp.example/ws?token=pm_gs_1');
  assert.equal(c.requests[0].opts.credentials, 'omit');
  c.message({ playmore: 'init', game_id: 'foreign' });
  assert.equal(c.api.gameId(), 'game', 'parent must not override a direct connection');
  c.api.createLobby({ maxPlayers: 4 });
  assert.equal(ws.sent[0].game_id, 'game');
  assert.equal(ws.sent[0].type, 'create');
  const timer = [...c.timers.values()].find(t => t.ms === 240000);
  await timer.fn();
  assert.equal(ws.sent.at(-1).type, 'auth');
  assert.equal(ws.sent.at(-1).token, 'pm_gs_2');
  assert.equal(c.api.sessionToken(), 'pm_gs_2');
  let code;
  c.api.onLobbyState(state => { code = state.code; });
  ws.receive({ type: 'lobby', lobby: { code: 'ABCDEF', game_id: 'game', host_id: 'player', players: [] } });
  assert.equal(code, 'ABCDEF');
  c.api.disconnect();
  assert.equal(ws.readyState, 3);
  assert.equal(c.api.sessionToken(), undefined);
});

test('standalone mode rejects account secrets, wrong games and plaintext remote servers', async () => {
  for (const options of [
    { server: 'http://remote.example', gameId: 'game', getToken: () => 'pm_gs_token' },
    { server: 'https://mp.example', gameId: 'game', getToken: () => 'pm_k_secret' },
    { server: 'https://mp.example', gameId: 'foreign', getToken: () => 'pm_gs_token' },
  ]) {
    const c = client();
    await assert.rejects(c.api.connect(options));
    assert.equal(c.sockets.length, 0);
  }
});

test('disconnect cancels an in-flight token request', async () => {
  const c = client(); let supply;
  const pending = c.api.connect({ server: 'https://mp.example', gameId: 'game', getToken: () => new Promise(resolve => { supply = resolve; }) });
  c.api.disconnect();
  supply('pm_gs_token');
  await assert.rejects(pending, /cancelled/);
  assert.equal(c.sockets.length, 0);
});

test('hosted game sockets never fall back to account-cookie authentication', () => {
  const html = fs.readFileSync('frontend/index.html', 'utf8');
  const start = html.indexOf('        function mpConnect()');
  const end = html.indexOf('        function mpSend(', start);
  let connections = 0;
  const sandbox = { mpGameToken: null, mpSocket: null, location: { protocol: 'https:', host: 'store.example' },
    WebSocket: class { constructor(url) { connections++; this.url = url; } } };
  vm.createContext(sandbox);
  vm.runInContext(html.slice(start, end), sandbox);
  vm.runInContext('mpConnect()', sandbox);
  assert.equal(connections, 0);
  sandbox.mpGameToken = 'pm_gs_runtime';
  vm.runInContext('mpConnect()', sandbox);
  assert.equal(connections, 1);
  assert.match(sandbox.mpSocket.url, /token=pm_gs_runtime/);
});
