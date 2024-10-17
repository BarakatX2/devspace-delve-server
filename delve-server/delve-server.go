package delve_server

import (
	"bufio"
	"context"
	"fmt"
	"github.com/go-delve/delve/pkg/gobuild"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/debugger"
	"github.com/go-delve/delve/service/rpccommon"
	"io"
	"log"
	"net"
)

type DelveServer struct {
	Context  context.Context
	Listener net.Listener
	Closed   bool

	// filenames
	Stdout string
	Stderr string

	srcConn net.Conn
	dstConn *net.TCPConn

	args []string
}

func New(ctx context.Context, listener net.Listener, args []string) *DelveServer {
	return &DelveServer{
		Context:  ctx,
		Listener: listener,
		args:     args,
	}
}

var ErrServerClosed = fmt.Errorf("server closed")

func (d *DelveServer) Accept() error {
	if d.Closed {
		return ErrServerClosed
	}
	srcConn, err := d.Listener.Accept()
	if err != nil {
		return fmt.Errorf("error accepting tcp connection: %w", err)
	}
	destListener, err := d.createProxyConnection(srcConn)
	if err != nil {
		return fmt.Errorf("error creating proxy connection: %w", err)
	}
	go d.startDelve(destListener)
	return nil
}

func (d *DelveServer) createProxyConnection(src net.Conn) (net.Listener, error) {
	ip := d.Listener.Addr().(*net.TCPAddr).IP
	listener, err := net.Listen("tcp", ip.String()+":0")
	if err != nil {
		return nil, fmt.Errorf("failed to create destination listener: %w", err)
	}
	destPort := listener.Addr().(*net.TCPAddr).Port
	tcpConn, err := net.DialTCP("tcp",
		nil,
		&net.TCPAddr{
			IP:   ip,
			Port: destPort,
		})
	if err != nil {
		return nil, fmt.Errorf("error dialing proxy connection: %w", err)
	}
	if d.srcConn != nil {
		_ = d.srcConn.Close()
		_ = d.dstConn.Close()
	}
	d.srcConn = src
	d.dstConn = tcpConn
	return listener, nil
}

func (d *DelveServer) startDelve(destListener net.Listener) {
	defer func() {
		_ = destListener.Close()
		_ = d.srcConn.Close()
	}()
	delve, err := d.runDelve(destListener)
	if err != nil {
		d.OnDelveFail(err)
		return
	}
	defer gobuild.Remove(delve.filename)
	if err := delve.server.Run(); err != nil {
		d.OnDelveFail(fmt.Errorf("error running delve: %w", err))
		return
	}
	defer delve.server.Stop()
	inputBuf := bufio.NewReader(d.srcConn)
	outputBuf := bufio.NewReader(d.dstConn)
	for {
		bytes := make([]byte, 64*1024) // max stack buffer
		n, err := inputBuf.Read(bytes)
		if err != nil {
			if err != io.EOF {
				log.Printf("error reading input connection: %s\n", err)
			}
			d.OnConnectionClose()
			return
		}
		if _, err := d.dstConn.Write(bytes[:n]); err != nil {
			d.OnDelveFail(err)
			return
		}
		n, err = outputBuf.Read(bytes)
		if err != nil {
			if err != io.EOF {
				log.Printf("error reading output connection: %s\n", err)
			}
			d.OnConnectionClose()
			return
		}
		if _, err := d.srcConn.Write(bytes[:n]); err != nil {
			d.OnDelveFail(err)
			return
		}
		select {
		case <-d.Context.Done():
			log.Println("stopping delve")
			return
		case <-delve.disconnectChan:
			log.Println("disconnect")
			return
		default:
			continue
		}
	}
}

type delveInstance struct {
	server         service.Server
	disconnectChan chan struct{}
	filename       string
}

func (d *DelveServer) runDelve(listener net.Listener) (*delveInstance, error) {
	filename, err := d.buildBinary(d.args[0])
	if err != nil {
		return nil, fmt.Errorf("error building binary: %w", err)
	}
	processArgs := append([]string{}, d.args...)
	processArgs[0] = filename
	disconnectChan := make(chan struct{})
	stdout := proc.OutputRedirect{Path: d.Stdout}
	stderr := proc.OutputRedirect{Path: d.Stderr}
	server := rpccommon.NewServer(&service.Config{
		Listener:           listener,
		ProcessArgs:        processArgs,
		AcceptMulti:        true,
		APIVersion:         2,
		CheckLocalConnUser: false,
		DisconnectChan:     disconnectChan,
		Debugger: debugger.Config{
			AttachPid:             0,
			WorkingDir:            ".",
			Backend:               "default",
			CoreFile:              "",
			Foreground:            true,
			Packages:              d.args,
			BuildFlags:            "",
			ExecuteKind:           debugger.ExecutingGeneratedFile,
			DebugInfoDirectories:  nil,
			CheckGoVersion:        true,
			TTY:                   "",
			Stdin:                 "",
			Stdout:                stdout,
			Stderr:                stderr,
			DisableASLR:           false,
			RrOnProcessPid:        0,
			AttachWaitFor:         "",
			AttachWaitForInterval: 1,
			AttachWaitForDuration: 0,
		},
	})
	return &delveInstance{
		server:         server,
		disconnectChan: disconnectChan,
		filename:       filename,
	}, nil
}

func (d *DelveServer) OnConnectionClose() {
	d.srcConn.Close()
	d.dstConn.Close()
}

func (d *DelveServer) OnDelveFail(err error) {
	log.Printf("error in delve thread: %s", err)
	_ = d.srcConn.Close()
}

func (d *DelveServer) buildBinary(args ...string) (string, error) {
	filename := gobuild.DefaultDebugBinaryPath("__debug_bin")
	err := gobuild.GoBuild(filename, args, "")
	return filename, err
}

func (d *DelveServer) Close() {
	d.Closed = true
	_ = d.Listener.Close()
}
