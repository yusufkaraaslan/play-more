# playmore-mp

PlayMore multiplayer lobby client for HTML5 games.

## Installation

```bash
npm install playmore-mp
```

## Usage

```typescript
import PlayMore from 'playmore-mp';

PlayMore.onReady(ctx => {
  console.log('Game ready', ctx.code, ctx.players);
});

PlayMore.onMessage((from, data) => {
  console.log('Message from', from, data);
});

PlayMore.onLaunch(lobby => {
  console.log('Game launched', lobby.code);
  // Start your game loop here
});

// Create a lobby (you become host)
PlayMore.createLobby({ maxPlayers: 4, public: true });

// Or join an existing lobby by code
PlayMore.joinLobby('ABCDEF');

// Or quick-play (auto-match with random players)
PlayMore.quickPlay(4);

// Send game state to all peers
PlayMore.send({ x: 10, y: 20 });

// Send to a specific player
PlayMore.send({ move: 'e4' }, 'player-id-here');

// Check transport (P2P vs relay)
PlayMore.transport('player-id'); // 'webrtc' or 'relay'
PlayMore.ping('player-id');     // RTT in ms, -1 if unknown
```

## Script tag usage (no bundler)

```html
<script src="/playmore-mp.js"></script>
<script>
  PlayMore.onReady(ctx => { ... });
  PlayMore.onLaunch(lobby => { ... });
</script>
```

## API

See the full documentation at [docs/sdk/api-reference.md](https://github.com/yusufkaraaslan/play-more/blob/main/docs/sdk/api-reference.md).

TypeScript definitions are available at `/playmore-mp.d.ts` on your PlayMore instance.
