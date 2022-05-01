package connector

import (
	"context"
	"errors"
	"log"
	"sync"
)

const (
	// ClientStateConnected represents a new connection that is waiting for the auth message from the peer.
	// A Client instance begins at this state and then transition to either ClientStateAuthorized or ClientStateClosed.
	ClientStateConnected ClientState = iota
	ClientStateAuthorized
	// ClientStateClosed represents a closed connection. This is a terminal state.
	// After entering this state, a Client instance will not receive any message and can not send any message.
	ClientStateClosed
)

var (
	ErrExceedMaxClients = errors.New("ppcserver: exceed maximum number of clients")
)

type (
	// ClientState represents the state of a Client instance, uint8 is used for save memory usage.
	ClientState uint8

	// Client represents a Client connection to a server.
	Client struct {
		transport Transport
		mu        sync.Mutex         // mu guards state.
		state     ClientState        // state is guarded by mu.
		cancelCtx context.CancelFunc // cancelCtx cancels the Client-level context that creates inside StartClient and result in Client.Close() being called.
		readCh    chan []byte
		writeCh   chan []byte // writeCh is the buffered channel of messages waiting to write to the transport.
	}
)

// StartClient creates a new Client with ClientStateConnected as the initial state,
//
func StartClient(ctx context.Context, transport Transport) error {
	if ExceedMaxClients() {
		// TODO, do we need to send special reason when close the transport
		if err := transport.Close(); err != nil {
			log.Println("ppcserver: exceed max clients in StartClient then call transport.Close() error:", err)
			return nil
		}
		return ErrExceedMaxClients
	}

	// The ctx.Done channel returns from context.WithCancel() is closed when the cancelCtx() function is called
	// or when the parent context's Done channel is closed, whichever happens first.
	ctx, cancelCtx := context.WithCancel(ctx)
	defer cancelCtx() // Call cancelCtx when StartClient exits to ensure the current Client's resources are released.

	c := &Client{
		transport: transport,
		state:     ClientStateConnected,
		cancelCtx: cancelCtx,
		readCh:    make(chan []byte),      // TODO, what is the buffer size?
		writeCh:   make(chan []byte, 256), // TODO, buffer size is configurable
	}

	defer func() {
		decrNumClients()
		_ = c.Close()
	}()
	incrNumClients()

	// if !allowToConnect() {
	// 	return
	// }
	// if err := handshake(); err != nil {
	// 	return
	// }

	// Since per connection support only one concurrent reader and one concurrent writer,
	// we execute all writes from the `writeLoop` goroutine and all reads from the `readLoop` goroutine.
	// Reference https://pkg.go.dev/github.com/gorilla/websocket#hdr-Concurrency for the concurrency usage details.
	go c.writeLoop(ctx)
	go c.readLoop(ctx)

	// Block StartClient until ctx.Done channel is closed.
	<-ctx.Done()
	return nil
}

// Close closes the connection with the peer.
func (c *Client) Close() (err error) {
	defer func() {
		if err != nil {
			log.Println("ppcserver: Client.Close() error:", err)
			return
		}
		log.Println("ppcserver: Client.Close() complete")
	}()

	// Change to the closed state should be guarded by mu. Skip if already in the closed state.
	c.mu.Lock()
	if c.state == ClientStateClosed {
		c.mu.Unlock()
		return nil
	}
	c.state = ClientStateClosed
	c.mu.Unlock()

	// Close the readCh to notify readers to stop reading from it.
	close(c.readCh)

	// transport.Close() closes the underlying network connection.
	// It can be called concurrently, and it's OK to call Close more than once.
	return c.transport.Close()
}

// readLoop keep reading from the transport until transport.Read() errored.
// The connection must be closed When readLoop exits by calling cancelCtx().
// readLoop must execute by a single goroutine to ensure that there is at most one concurrent reader on a connection.
func (c *Client) readLoop(ctx context.Context) {
	defer c.cancelCtx()

	for {
		// TODO, here we actually use read timeout to break the loop

		message, err := c.transport.Read()

		// The connection must be closed once Read returns any error.
		if err != nil {
			log.Printf("ppcserver: Client.transport.Read() error: %v", err)
			return
		}

		log.Printf("ppcserver: Client.transport.Read() receive: %s", message)

		// Caution: selects the `ctx.Done` case to ensure not sending on a closed readCh, which will cause panic.
		select {
		case <-ctx.Done():
			return // Caution: should not 'break' be used which will only exit `select` rather than `for`.
		// TODO, send to readCh, block when readCh is full
		// case c.readCh <- message:
		default:
		}
	}

	// TODO, wait auth request from the peer.
}

func (c *Client) writeLoop(ctx context.Context) {

}

// State returns the current state of the Client.
func (c *Client) State() ClientState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

func (c *Client) Write(data []byte) error {
	if err := c.transport.Write(data); err != nil {
		return err
	}
	return nil
}

func (c *Client) heartbeat() {

}
