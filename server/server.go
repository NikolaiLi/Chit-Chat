package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"sync"

	pb "github.com/NikolaiLi/Chit-Chat/grpc"
	"github.com/google/uuid"
	"google.golang.org/grpc"
)

type LamportClock struct {
	timestamp int64
	mu        sync.Mutex
}

func (l *LamportClock) Tick() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.timestamp++
	return l.timestamp
}

func (l *LamportClock) Sync(remoteTimestamp int64) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	if remoteTimestamp > l.timestamp {
		l.timestamp = remoteTimestamp
	}
	l.timestamp++
	return l.timestamp
}

type Participant struct {
	id   string
	name string
	ch   chan *pb.ServerMessage
}

type server struct {
	pb.UnimplementedChitChatServiceServer
	participants map[string]*Participant
	mu           sync.RWMutex
	clock        *LamportClock
}

func newServer() *server {
	return &server{
		participants: make(map[string]*Participant),
		clock:        &LamportClock{timestamp: 0},
	}
}

func (s *server) ChatStream(stream pb.ChitChatService_ChatStreamServer) error {
	initialMsg, err := stream.Recv()
	if err != nil {
		log.Printf("Failed to receive initial message: %v", err)
		return err
	}
	joinReq := initialMsg.GetJoinRequest()
	if joinReq == nil {
		return fmt.Errorf("first message from client was not a JoinRequest")
	}

	participant := &Participant{
		id:   uuid.New().String(),
		name: joinReq.Name,
		ch:   make(chan *pb.ServerMessage, 10),
	}

	s.mu.Lock()
	s.participants[participant.id] = participant
	s.mu.Unlock()

	log.Printf("Client connected: %s (ID: %s)", participant.name, participant.id)

	ts := s.clock.Tick()
	joinMsg := &pb.ServerMessage{
		LamportTimestamp: ts,
		Content:          fmt.Sprintf("Participant %s joined Chit Chat at logical time %d", participant.name, ts),
	}
	s.broadcast(joinMsg)

	defer func() {
		log.Printf("Client disconnected: %s (ID: %s)", participant.name, participant.id)

		s.mu.Lock()
		delete(s.participants, participant.id)
		s.mu.Unlock()

		ts := s.clock.Tick()
		leaveMsg := &pb.ServerMessage{
			LamportTimestamp: ts,
			Content:          fmt.Sprintf("Participant %s left Chit Chat at logical time %d", participant.name, ts),
		}
		s.broadcast(leaveMsg)
	}()

	errCh := make(chan error)

	go s.receiveMessages(stream, participant, errCh)
	go s.sendMessages(stream, participant, errCh)

	return <-errCh
}

func (s *server) receiveMessages(stream pb.ChitChatService_ChatStreamServer, p *Participant, errCh chan error) {
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			log.Printf("Client %s closed the stream.", p.name)
			errCh <- nil
			return
		}
		if err != nil {
			log.Printf("Error receiving from %s: %v", p.name, err)
			errCh <- err
			return
		}

		if pubReq := msg.GetPublishRequest(); pubReq != nil {
			if len(pubReq.Content) > 128 {
				log.Printf("Message from %s is too long, ignoring.", p.name)
				continue
			}

			s.clock.Sync(pubReq.LamportTimestamp)

			broadcastMsg := &pb.ServerMessage{
				LamportTimestamp: s.clock.Tick(),
				Content:          fmt.Sprintf("%s: %s", p.name, pubReq.Content),
			}
			s.broadcast(broadcastMsg)
		}
	}
}

func (s *server) sendMessages(stream pb.ChitChatService_ChatStreamServer, p *Participant, errCh chan error) {
	for msg := range p.ch {
		if err := stream.Send(msg); err != nil {
			log.Printf("Error sending to %s: %v", p.name, err)
			errCh <- err
			return
		}
	}
}

func (s *server) broadcast(msg *pb.ServerMessage) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	log.Printf("Broadcasting message (T=%d): %s", msg.LamportTimestamp, msg.Content)

	for _, p := range s.participants {
		p.ch <- msg
	}
}

func main() {
	log.SetFlags(log.Ltime)
	log.SetPrefix("SERVER: ")

	port := ":50051"
	lis, err := net.Listen("tcp", port)
	if err != nil {
		log.Fatalf("Could not listen on port %s: %v", port, err)
	}

	grpcServer := grpc.NewServer()
	chatServer := newServer()

	pb.RegisterChitChatServiceServer(grpcServer, chatServer)

	log.Printf("Server starting on %s", port)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Failed to start gRPC server: %v", err)
	}
}
