package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	pb "github.com/NikolaiLi/Chit-Chat/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatalf("Usage: go run client/client.go <name>")
	}
	name := os.Args[1]

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
	log.Printf("Connected to Chit-Chat as %s", name)

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
				os.Exit(1) // Exit if the server closes the stream.
			}
			fmt.Printf("[%d] %s\n", msg.LamportTimestamp, msg.Content)
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

		publishMsg := &pb.ClientMessage{
			Event: &pb.ClientMessage_PublishRequest{
				PublishRequest: &pb.PublishRequest{Content: line},
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
