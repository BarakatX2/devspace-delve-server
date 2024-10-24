package devspace-delve-server

import (
	"context"
	delve_server "devspace-delve-server/delve-server"
	"errors"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	listenAddr := flag.String("listen", "127.0.0.1:2345", "bind address")
	stdout := flag.String("stdout", "", "file path to redirect stdout to")
	stderr := flag.String("stderr", "", "file path to redirect stderr to")
	flag.Parse()

	listener, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		panic(err)
	}

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGINT, syscall.SIGTERM)
	ctx, cancel := context.WithCancel(context.Background())

	server := delve_server.New(ctx, listener, flag.Args())
	server.Stdout = *stdout
	server.Stderr = *stderr
	go func() {
		<-interrupt
		log.Println("received interrupt, shutting down")
		server.Close()
		cancel()
	}()
	log.Println("starting server on " + listener.Addr().String())
	for {
		if err := server.Accept(); err != nil {
			if errors.Is(err, delve_server.ErrServerClosed) {
				log.Println("server closed, exiting")
				return
			}
			log.Printf("error accepting connection: %s\n", err)
		}
		select {
		case <-ctx.Done():
			log.Println("stopping server, context done")
			return
		case <-time.After(time.Millisecond * 50):
			continue
		}
	}
}
