'use strict';

// Minimal Socket.IO 4.x server used by the Go end-to-end tests.
// Exercises: connect, server-pushed events, acknowledgements and namespaces,
// over both HTTP long-polling and WebSocket (with upgrade enabled).

const { Server } = require('socket.io');

const PORT = parseInt(process.env.PORT || '3000', 10);
// When ALLOW_UPGRADES=false the server advertises no upgrades, so a
// polling-only client is never offered a websocket upgrade. This is used by
// the dedicated polling-only test instance.
const ALLOW_UPGRADES = (process.env.ALLOW_UPGRADES || 'true') !== 'false';
// The Go polling transport fetches data once per ping interval, so a short
// interval keeps server->client latency low for the polling-only test.
const PING_INTERVAL = parseInt(process.env.PING_INTERVAL || '25000', 10);

const io = new Server(PORT, {
  cors: { origin: '*' },
  allowUpgrades: ALLOW_UPGRADES,
  transports: ALLOW_UPGRADES ? ['polling', 'websocket'] : ['polling'],
  pingInterval: PING_INTERVAL,
});

function wire(socket, ns) {
  // "echo" replies through the ack callback with the same payload.
  socket.on('echo', (msg, ack) => {
    if (typeof ack === 'function') {
      ack(msg);
    }
  });

  // "hello" triggers a server-pushed "welcome" event.
  socket.on('hello', (name) => {
    socket.emit('welcome', 'hello ' + name);
  });

  // eslint-disable-next-line no-console
  console.log('client connected on namespace ' + ns + ': ' + socket.id);
}

io.on('connection', (socket) => wire(socket, '/'));
io.of('/admin').on('connection', (socket) => wire(socket, '/admin'));

// eslint-disable-next-line no-console
console.log('socket.io e2e server listening on port ' + PORT + ' (upgrades=' + ALLOW_UPGRADES + ')');
