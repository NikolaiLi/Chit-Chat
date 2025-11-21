package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	pb "github.com/NikolaiLi/Chit-Chat/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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

func main() {
	if len(os.Args) < 2 {
		log.Fatalf("Usage: go run client/client.go <name>")
	}
	name := os.Args[1]

	log.SetFlags(log.Ltime)
	log.SetPrefix(fmt.Sprintf("CLIENT %s: ", name))

	clock := &LamportClock{timestamp: 0}

	conn, err := grpc.Dial("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Could not connect to server: %v", err)
	}
	defer conn.Close()

	client := pb.NewChitChatServiceClient(conn)
	stream, err := client.ChatStream(context.Background())
	if err != nil {
		log.Fatalf("Could not open stream: %v", err)
	}

	log.Printf("Connected to Chit-Chat server")

	joinMsg := &pb.ClientMessage{
		Event: &pb.ClientMessage_JoinRequest{
			JoinRequest: &pb.JoinRequest{Name: name},
		},
	}

	stream.Send(joinMsg)

	go func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				log.Printf("Connection to server lost: %v", err)
				os.Exit(1)
			}

			clock.Sync(msg.LamportTimestamp)

			fmt.Printf("[%d] %s\n", msg.LamportTimestamp, msg.Content)

			log.Printf("Received message: [msg=%d] %s", msg.LamportTimestamp, msg.Content)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Closing stream...")
		stream.CloseSend()
		os.Exit(0)
	}()

	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("Enter your messages (press Ctrl+C to exit):")
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		if len(line) > 128 {
			fmt.Println("Error: Message must be 128 characters or less.")
			continue
		}

		currentTimestamp := clock.Tick()

		publishMsg := &pb.ClientMessage{
			Event: &pb.ClientMessage_PublishRequest{
				PublishRequest: &pb.PublishRequest{
					Content:          line,
					LamportTimestamp: currentTimestamp,
				},
			},
		}
		if err := stream.Send(publishMsg); err != nil {
			log.Printf("Error sending message: %v", err)
			break
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatalf("Error reading input: %v", err)
	}
}
