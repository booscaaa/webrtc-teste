// signalingServer.js
const WebSocket = require('ws');
const server = new WebSocket.Server({ port: 3000 });

const clients = {};

server.on('connection', (socket) => {
  socket.on('message', (message) => {
    const data = JSON.parse(message);
    
    switch (data.type) {
      case 'join':
        clients[data.name] = socket;
        broadcast({ type: 'new-user', name: data.name });
        break;

      case 'offer':
      case 'answer':
      case 'candidate':
        if (clients[data.target]) {
          clients[data.target].send(JSON.stringify(data));
        }
        break;

      case 'leave':
        delete clients[data.name];
        broadcast({ type: 'user-left', name: data.name });
        break;
    }
  });

  socket.on('close', () => {
    const name = Object.keys(clients).find((key) => clients[key] === socket);
    if (name) {
      delete clients[name];
      broadcast({ type: 'user-left', name });
    }
  });
});

function broadcast(message) {
  Object.values(clients).forEach(client => client.send(JSON.stringify(message)));
}
