package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"

	pb "github.com/NikolaiLi/Chit-Chat/grpc"
	"github.com/google/uuid"
	"google.golang.org/grpc"
)

type LamportClock struct {
	time atomic.Int64
}

func (l *LamportClock) Tick() int64 {
	return l.time.Add(1)
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
		clock:        &LamportClock{},
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

	defer func() {
		log.Printf("Client disconnected: %s (ID: %s)", participant.name, participant.id)

		s.mu.Lock()
		delete(s.participants, participant.id)
		s.mu.Unlock()

		leaveMsg := &pb.ServerMessage{
			LamportTimestamp: s.clock.Tick(),
			Content:          fmt.Sprintf("Participant %s left Chit Chat", participant.name),
		}
		s.broadcast(leaveMsg)
	}()

	joinMsg := &pb.ServerMessage{
		LamportTimestamp: s.clock.Tick(),
		Content:          fmt.Sprintf("Participant %s joined Chit Chat", participant.name),
	}
	s.broadcast(joinMsg)

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
				log.Printf("Message from %s is too long.", p.name)
				continue
			}

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
