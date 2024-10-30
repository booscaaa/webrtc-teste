// signalingServer.js
const WebSocket = require('ws');
const server = new WebSocket.Server({ port: 3000 });

const clients = {};

server.on('connection', (socket) => {
  let userName = null;

  socket.on('message', (message) => {
    const data = JSON.parse(message);

    switch (data.type) {
      case 'join':
        userName = data.name;
        clients[userName] = socket;
        // Envia a lista de usuários existentes para o novo usuário
        socket.send(JSON.stringify({ type: 'user-list', users: Object.keys(clients) }));
        // Notifica outros usuários sobre o novo usuário
        broadcast({ type: 'new-user', name: data.name }, userName);
        break;

      case 'offer':
      case 'answer':
      case 'candidate':
        if (clients[data.target]) {
          clients[data.target].send(JSON.stringify(data));
        }
        break;

      case 'leave':
        delete clients[userName];
        broadcast({ type: 'leave', name: userName }, userName);
        break;
    }
  });

  socket.on('close', () => {
    if (userName) {
      delete clients[userName];
      broadcast({ type: 'leave', name: userName }, userName);
    }
  });
});

function broadcast(message, exclude) {
  Object.keys(clients).forEach((client) => {
    if (client !== exclude) {
      clients[client].send(JSON.stringify(message));
    }
  });
}
