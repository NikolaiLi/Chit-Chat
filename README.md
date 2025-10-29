# Chit-Chat
Project ITU - Distributed Systems

*How to Run*

You will need at least two separate terminal windows to run the application: one for the server and one for each client.

1. Start the Server

In your first terminal, navigate to the project root and run the following command to start the chat server :   

*go run server/server.go*

The server will start and listen for connections.

2. Start a Client

In a new terminal, run the following command to connect a client. Replace <name> with the desired username :

*go run client/client.go <name>*

For example:

*go run client/client.go Alice*

3. Start More Clients

Open additional terminals and run the client command again with different names to have them join the chat.

*go run client/client.go Bob*