const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const seed = fs.readFileSync('internal/handlers/seed.go', 'utf8');
const html = seed.slice(seed.indexOf('func generateMultiplayerTicTacToeGame()'));
const source = html.match(/<script>\n([\s\S]*?)<\/script>/)[1];

function client(id) {
  const elements = new Map(), callbacks = {}, sent = [], commands = [];
  function element() {
    return { children: [], hidden: false, textContent: '', value: '',
      setAttribute() {}, appendChild(child) { this.children.push(child); } };
  }
  const document = { getElementById(key) {
    if (!elements.has(key)) elements.set(key, element());
    return elements.get(key);
  }, createElement: element };
  const api = { send(data, to) { sent.push({ data: structuredClone(data), to }); } };
  for (const event of ['Ready', 'LobbyState', 'Launch', 'Message', 'Error', 'Closed']) {
    api['on' + event] = fn => { callbacks[event] = fn; };
  }
  for (const name of ['setTopology', 'createLobby', 'joinLobby', 'quickPlay', 'cancelMatchmake', 'readyUp', 'startGame', 'leaveLobby']) {
    api[name] = (...args) => commands.push({ name, args });
  }
  vm.runInNewContext(source, { document, PlayMore: api, setInterval() {} });
  callbacks.Ready({ you: { id } });
  return { callbacks, sent, commands, el: document.getElementById, cells: document.getElementById('board').children };
}
const lobby = { code: 'ABCDEF', players: [
  { id: 'a', username: 'Alice', host: true }, { id: 'b', username: 'Bob', ready: true }
] };
function match() {
  const a = client('a'), b = client('b');
  a.callbacks.Launch(structuredClone(lobby)); b.callbacks.Launch(structuredClone(lobby));
  const flush = () => {
    for (const [from, sender, recipient] of [['a', a, b], ['b', b, a]]) {
      while (sender.sent.length) recipient.callbacks.Message(from, sender.sent.shift().data);
    }
    while (a.sent.length) b.callbacks.Message('a', a.sent.shift().data);
  };
  flush();
  return { a, b, flush, click(who, index) { who.cells[index].onclick(); flush(); } };
}

test('online Tic-Tac-Toe validates turns, occupied cells, replayed moves and authoritative snapshots', () => {
  const { a, b, flush, click } = match();
  assert.equal(a.commands[0].args[0], 'star');
  click(b, 4); // O cannot play first, even if it bypasses disabled UI.
  assert.equal(a.cells[4].textContent, '');
  click(a, 0);
  const before = a.cells.map(c => c.textContent);
  const stale = { t: 'move', cell: 4, round: 1, revision: 0 };
  a.callbacks.Message('b', stale); flush();
  assert.deepEqual(a.cells.map(c => c.textContent), before);
  click(b, 0); // occupied cell
  assert.equal(a.cells[0].textContent, 'X');
  click(b, 4);
  assert.equal(a.cells[4].textContent, 'O');
  const forged = { t: 'state', state: { board: Array(9).fill('O'), seats: ['a','b'], turn: 'O', round: 99, revision: 99 } };
  b.callbacks.Message('intruder', forged);
  a.callbacks.Message('b', forged);
  assert.equal(b.cells[0].textContent, 'X');
  assert.equal(a.cells[0].textContent, 'X');
  a.callbacks.Message('intruder', { t: 'move', cell: 2, round: 1, revision: 2 });
  assert.equal(a.cells[2].textContent, '');
});

test('online Tic-Tac-Toe synchronizes wins, rematches, draws and prevents post-game moves', () => {
  const { a, b, click, flush } = match();
  for (const [who, cell] of [[a,0],[b,3],[a,1],[b,4],[a,2]]) click(who,cell);
  for (const c of [a,b]) assert.equal(c.el('status').textContent, 'X wins!');
  click(b,8); assert.equal(a.cells[8].textContent, '');
  a.el('rematch').onclick(); flush();
  assert.ok(b.cells.every(c => c.textContent === ''));
  for (const [who,cell] of [[a,0],[b,1],[a,2],[b,4],[a,3],[b,5],[a,7],[b,6],[a,8]]) click(who,cell);
  for (const c of [a,b]) assert.equal(c.el('status').textContent, 'Draw!');
});

test('online Tic-Tac-Toe resynchronizes late launches and pauses when an opponent leaves', () => {
  const { a, b, click, flush } = match();
  click(a,0);
  b.callbacks.Launch(structuredClone(lobby));
  a.callbacks.Message('b', { t: 'sync' }); flush();
  assert.equal(b.cells[0].textContent, 'X');
  a.callbacks.LobbyState({ ...lobby, players: [lobby.players[0]] });
  assert.equal(a.el('status').textContent, 'Waiting for an opponent…');
  assert.ok(a.cells.every(c => c.disabled));
  a.callbacks.LobbyState(structuredClone(lobby)); flush();
  assert.ok(a.cells.every(c => c.textContent === ''));
  b.el('leave').onclick();
  assert.equal(b.commands.at(-1).name, 'leaveLobby');
  assert.equal(b.el('menu').hidden, false);
  b.callbacks.Closed();
  assert.equal(b.el('menu').hidden, false);
});
