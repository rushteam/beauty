package main

import (
	"context"
	"log"
	"os"
	"time"

	"gitlab.meitu.com/golang/beauty/example/grpc/service/helloworld"
	"google.golang.org/grpc"
)

func main() {
	addr := "127.0.0.1:50000"
	// Set up a connection to the server.
	conn, err := grpc.Dial(addr, grpc.WithTimeout(time.Second), grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()
	c := helloworld.NewGreeterClient(conn)

	// Contact the server and print out its response.
	name := "test"
	if len(os.Args) > 1 {
		name = os.Args[1]
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, err := c.SayHello(ctx, &helloworld.HelloRequest{Name: name})
	if err != nil {
		log.Fatalf("could not greet: %v", err)
	}
	log.Printf("Greeting: %s", r.GetMessage())
}
