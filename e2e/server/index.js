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

  // "binEcho" echoes a binary Buffer back through the ack callback, exercising
  // the socket.io binary attachment (PacketBinaryAck) path.
  socket.on('binEcho', (buf, ack) => {
    if (typeof ack === 'function') {
      ack(buf);
    }
  });

  // "binMulti" acks with BOTH buffers, exercising multi-attachment encoding in
  // both directions (two placeholders in one packet).
  socket.on('binMulti', (a, b, ack) => {
    if (typeof ack === 'function') {
      ack(a, b);
    }
  });

  // "binNested" echoes an object that mixes a binary Buffer, a null and a
  // plain string back through the ack, exercising placeholder substitution at
  // nested positions and null handling next to real attachments.
  socket.on('binNested', (obj, ack) => {
    if (typeof ack === 'function') {
      ack({ file: obj.file, note: obj.note, name: obj.name });
    }
  });

  // "binPush" triggers a server-pushed "binWelcome" event carrying a binary
  // Buffer (PacketBinaryEvent), so the client receives []byte without an ack.
  socket.on('binPush', (name) => {
    socket.emit('binWelcome', Buffer.from(String(name)));
  });

  // eslint-disable-next-line no-console
  console.log('client connected on namespace ' + ns + ': ' + socket.id);
}

io.on('connection', (socket) => wire(socket, '/'));
io.of('/admin').on('connection', (socket) => wire(socket, '/admin'));

// eslint-disable-next-line no-console
console.log('socket.io e2e server listening on port ' + PORT);
