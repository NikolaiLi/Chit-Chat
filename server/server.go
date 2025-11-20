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
	mu    sync.Mutex
	value int64
}

func (c *LamportClock) Increment() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value++
	return c.value
}

func (c *LamportClock) Update(remote int64) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if remote > c.value {
		c.value = remote
	}
	c.value++
	return c.value
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
		clock:        &LamportClock{value: 0},
	}
}

func (s *server) ChatStream(stream pb.ChitChatService_ChatStreamServer) error {
	initialMsg, err := stream.Recv()
	if err!= nil {
		log.Printf("Failed to receive initial message: %v", err)
		return err
	}

	joinReq := initialMsg.GetJoinRequest()
	if joinReq == nil {
		return fmt.Errorf("first message from client was not a JoinRequest")
	}

	s.clock.Update(joinReq.LamportTimestamp)

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

		ts := s.clock.Increment()

		leaveMsg := &pb.ServerMessage{
			LamportTimestamp: ts,
			Content:          fmt.Sprintf("Participant %s left Chit Chat", participant.name),
		}
		s.broadcast(leaveMsg)
	}()

	ts := s.clock.Increment()

	joinBroadcast := &pb.ServerMessage{
		LamportTimestamp: ts,
		Content:          fmt.Sprintf("Participant %s joined Chit Chat", participant.name),
	}
	s.broadcast(joinBroadcast)

	errCh := make(chan error)

	go s.receiveMessages(stream, participant, errCh)
	go s.sendMessages(stream, participant, errCh)

	return <-errCh
}

func (s *server) receiveMessages(stream pb.ChitChatService_ChatStreamServer, p *Participant, errCh chan error) {
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			errCh <- nil
			return
		}
		if err!= nil {
			log.Printf("Error receiving from %s: %v", p.name, err)
			errCh <- err
			return
		}

		if pubReq := msg.GetPublishRequest(); pubReq!= nil {
			if len(pubReq.Content) > 128 {
				log.Printf("Message from %s is too long.", p.name)
				continue
			}

			receiveTime := s.clock.Update(pubReq.LamportTimestamp)

			log.Printf("Received msg from %s at T=%d (Server updated to %d)", p.name, pubReq.LamportTimestamp, receiveTime)

			broadcastTime := s.clock.Increment()

			broadcastMsg := &pb.ServerMessage{
				LamportTimestamp: broadcastTime,
				Content:          fmt.Sprintf("%s: %s", p.name, pubReq.Content),
			}
			s.broadcast(broadcastMsg)
		}
	}
}

func (s *server) sendMessages(stream pb.ChitChatService_ChatStreamServer, p *Participant, errCh chan error) {
	for msg := range p.ch {
		if err := stream.Send(msg); err!= nil {
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
		select {
		case p.ch <- msg:
		default:
			log.Printf("Warning: Client %s channel full, dropping message", p.name)
		}
	}
}

func main() {
	port := ":50051"
	lis, err := net.Listen("tcp", port)
	if err!= nil {
		log.Fatalf("Could not listen on port %s: %v", port, err)
	}

	grpcServer := grpc.NewServer()
	chatServer := newServer()

	pb.RegisterChitChatServiceServer(grpcServer, chatServer)

	log.Printf("Server starting on %s", port)
	if err := grpcServer.Serve(lis); err!= nil {
		log.Fatalf("Failed to start gRPC server: %v", err)
	}
}