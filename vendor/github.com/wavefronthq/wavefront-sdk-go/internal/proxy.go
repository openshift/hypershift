package internal

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type ProxyConnectionHandler struct {
	// keep this as first element of struct to guarantee 64-bit alignment ton 32-bit machines.
	// atomic.* functions crash if the operand is not 64-bit aligned.
	// See https://github.com/golang/go/issues/599
	failures         int64
	address          string
	flushTicker      *time.Ticker
	done             chan struct{}
	mtx              sync.RWMutex
	conn             net.Conn
	writer           *bufio.Writer
	internalRegistry *MetricRegistry

	writeSuccesses *DeltaCounter
	writeErrors    *DeltaCounter
}

func NewProxyConnectionHandler(address string, flushInterval time.Duration, prefix string, internalRegistry *MetricRegistry) ConnectionHandler {
	proxyConnectionHandler := &ProxyConnectionHandler{
		address:          address,
		flushTicker:      time.NewTicker(flushInterval),
		internalRegistry: internalRegistry,
	}
	proxyConnectionHandler.writeSuccesses = internalRegistry.NewDeltaCounter(prefix + ".write.success")
	proxyConnectionHandler.writeErrors = internalRegistry.NewDeltaCounter(prefix + ".write.errors")
	return proxyConnectionHandler
}

func (handler *ProxyConnectionHandler) Start() {
	handler.done = make(chan struct{})

	go func() {
		for {
			select {
			case <-handler.flushTicker.C:
				err := handler.Flush()
				if err != nil {
					log.Println(err)
				}
			case <-handler.done:
				return
			}
		}
	}()
}

func (handler *ProxyConnectionHandler) Connect() error {
	handler.mtx.Lock()
	defer handler.mtx.Unlock()

	// Skip if already connected
	if handler.conn != nil {
		return nil
	}

	var err error
	handler.conn, err = net.DialTimeout("tcp", handler.address, time.Second*10)
	if err != nil {
		handler.conn = nil
		return fmt.Errorf("unable to connect to Wavefront proxy at address: %s, err: %q", handler.address, err)
	}
	log.Printf("connected to Wavefront proxy at address: %s", handler.address)
	handler.writer = bufio.NewWriter(handler.conn)
	return nil
}

func (handler *ProxyConnectionHandler) Connected() bool {
	handler.mtx.RLock()
	defer handler.mtx.RUnlock()
	return handler.conn != nil
}

func (handler *ProxyConnectionHandler) Close() {
	handler.flushTicker.Stop()
	handler.done <- struct{}{} // block until goroutine exits

	err := handler.Flush()
	if err != nil {
		log.Println(err)
	}

	handler.mtx.Lock()
	defer handler.mtx.Unlock()

	handler.done = nil
	if handler.conn != nil {
		handler.conn.Close()
		handler.conn = nil
		handler.writer = nil
	}
}

func (handler *ProxyConnectionHandler) Flush() error {
	handler.mtx.Lock()
	defer handler.mtx.Unlock()

	if handler.writer != nil {
		err := handler.writer.Flush()
		if err != nil {
			handler.resetConnection()
		}
		return err
	}
	return nil
}

func (handler *ProxyConnectionHandler) GetFailureCount() int64 {
	return atomic.LoadInt64(&handler.failures)
}

func (handler *ProxyConnectionHandler) SendData(lines string) error {
	// if the connection was closed or interrupted - don't cause a panic (we'll retry at next interval)
	defer func() {
		if r := recover(); r != nil {
			// we couldn't write the line so something is wrong with the connection
			log.Println("error sending data", r)
			handler.mtx.Lock()
			handler.resetConnection()
			handler.mtx.Unlock()
		}
	}()

	// bufio.Writer isn't thread safe
	handler.mtx.Lock()
	defer handler.mtx.Unlock()

	if handler.conn != nil {
		// Set a generous timeout to the write
		handler.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		_, err := fmt.Fprint(handler.writer, lines)
		if err != nil {
			handler.writeErrors.Inc()
			atomic.AddInt64(&handler.failures, 1)
		} else {
			handler.writeSuccesses.Inc()
		}
		return err
	}
	return fmt.Errorf("failed to send data: invalid wavefront proxy connection")
}

func (handler *ProxyConnectionHandler) resetConnection() {
	log.Println("resetting wavefront proxy connection")
	handler.conn.Close()
	handler.conn = nil
	handler.writer = nil
}
