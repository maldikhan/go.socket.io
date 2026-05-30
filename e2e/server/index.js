'use strict';

// Minimal Socket.IO 4.x server used by the Go end-to-end tests.
// Exercises: connect, server-pushed events, acknowledgements and namespaces,
// over both HTTP long-polling and WebSocket (with upgrade enabled).

const { Server } = require('socket.io');

const PORT = parseInt(process.env.PORT || '3000', 10);

const io = new Server(PORT, {
  cors: { origin: '*' },
  // Offer both transports so the client can connect polling-only,
  // websocket-only, or start on polling and upgrade.
  transports: ['polling', 'websocket'],
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
console.log('socket.io e2e server listening on port ' + PORT);
